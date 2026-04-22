package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/httputil"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/pairing"
	"github.com/justinlindh/digits/server/internal/ratelimit"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// TemplateFS returns the embedded template filesystem for external template parsing.
func TemplateFS() embed.FS {
	return templateFS
}

// devStaticDirDefault is the disk path (relative to the process CWD) used
// for /static/ when DevMode is on and no explicit override is supplied.
// It matches the Makefile's dev-up target, which runs signald with CWD
// set to the server/ module root.
const devStaticDirDefault = "internal/web/static"

// staticFileServer returns the handler that serves /static/. In devMode it
// serves from disk so CSS and JS edits are visible on reload without a
// rebuild; diskDir falls back to devStaticDirDefault when empty. In
// production mode it serves the embedded FS, keeping the binary
// self-contained. Both code paths route /static/dialup.css to the same
// file content for a given checkout.
func staticFileServer(devMode bool, diskDir string) http.Handler {
	if devMode {
		if diskDir == "" {
			diskDir = devStaticDirDefault
		}
		return http.StripPrefix("/static/", http.FileServer(http.Dir(diskDir)))
	}
	// fs.Sub strips the "static" prefix from the embedded FS so request
	// paths align with the disk-mode handler's StripPrefix treatment.
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// embed declares the "static" directory above; sub-rooting to it
		// cannot fail at runtime. A panic here would indicate a programmer
		// error in the embed declaration.
		panic(fmt.Errorf("fs.Sub(staticFS, \"static\"): %w", err))
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}

type Handler struct {
	upgrader    websocket.Upgrader
	lineStore   *line.Store
	deviceStore *device.Store
	hub         *signaling.Hub
	tracker     *calls.Tracker
	relay       *signaling.Relay
	healthStore *calls.HealthStore
	// Per-page template sets to avoid {{define}} name conflicts
	tmplDashboard      *template.Template
	tmplPhones         *template.Template
	tmplCalls          *template.Template
	tmplSettings       *template.Template
	tmplOnboard        *template.Template
	tmplPhoneDetail    *template.Template
	tmplLinks          *template.Template
	tmplConnecting     *template.Template
	tmplCallLivePanel  *template.Template
	tmplCallLiveDetail *template.Template
	cfg                HandlerConfig
	// Auth
	authStore    *auth.Store
	authHandlers *auth.Handlers
	googleAuth   *auth.GoogleAuth
	// Household
	householdStore *household.Store
	// Pairing
	pairingStore *pairing.Store
	// Household links
	linkStore *household.LinkStore
	// Email
	emailSender email.Sender
	baseURL     string // app URL, e.g. https://app.digits.family
	// Admin
	adminSecret string
	// Rate limiters
	authLimiter    *ratelimit.Limiter
	pairingLimiter *ratelimit.Limiter
	// Updates
	Releases *updates.GitHubReleases
}

// segDesc drives bar segment rendering. Lit is the count (0..10) of
// segments that should be rendered as lit; Severity ("" | "warn" | "bad")
// controls their color via CSS classes.
type segDesc struct {
	Lit      int
	Severity string
}

type HandlerConfig struct {
	Addr string
	// DevMode enables development-only conveniences. Today that means
	// serving /static/ from disk instead of the embedded FS, so CSS and
	// JS edits don't require a signald rebuild. When false, the embedded
	// FS is used. DevMode also registers the /dev/seed-firmware test
	// helper used by e2e.
	DevMode bool
	// DevStaticDir is the disk path served for /static/ when DevMode is
	// true. Empty falls back to devStaticDirDefault ("internal/web/static"),
	// which matches the layout the Makefile's dev-up target runs from.
	// The field exists mainly so tests can point at a temp directory.
	DevStaticDir string
}

func NewHandler(lineStore *line.Store, deviceStore *device.Store, hub *signaling.Hub, tracker *calls.Tracker, relay *signaling.Relay, cfg HandlerConfig, authStore *auth.Store, authHandlers *auth.Handlers, googleAuth *auth.GoogleAuth, householdStore *household.Store, pairingStore *pairing.Store, linkStore *household.LinkStore, emailSender email.Sender, baseURL string, adminSecret string, healthStore *calls.HealthStore) (*Handler, error) {
	funcMap := template.FuncMap{
		"fmtPhone": line.FormatNumber,
		"fmtDuration": func(seconds int) string {
			if seconds < 60 {
				return fmt.Sprintf("%ds", seconds)
			}
			return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
		},
		"derefFloat32": func(p *float32) float32 {
			if p == nil {
				return 0
			}
			return *p
		},
		"derefInt64": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"iter": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i
			}
			return out
		},
		"humanBytes": func(n int64) string {
			switch {
			case n > 1024*1024:
				return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
			case n > 1024:
				return fmt.Sprintf("%.1fK", float64(n)/1024)
			default:
				return fmt.Sprintf("%dB", n)
			}
		},
		"pctToSegments": func(pct float32) segDesc {
			lit := int(pct / 10.0)
			if pct > 0 && lit == 0 {
				lit = 1
			}
			if lit > 10 {
				lit = 10
			}
			sev := ""
			switch {
			case pct >= 2.0:
				sev = "bad"
			case pct >= 0.5:
				sev = "warn"
			}
			return segDesc{Lit: lit, Severity: sev}
		},
		"msToSegments": func(ms float32) segDesc {
			lit := int(ms / 6.0)
			if ms > 0 && lit == 0 {
				lit = 1
			}
			if lit > 10 {
				lit = 10
			}
			sev := ""
			switch {
			case ms >= 40.0:
				sev = "bad"
			case ms >= 20.0:
				sev = "warn"
			}
			return segDesc{Lit: lit, Severity: sev}
		},
		"renderNotes": renderNotes,
	}
	// parsePage closes over the layout + shared-partials file list so each
	// page only names itself. Adding a new layout or partial touches one line.
	parsePage := func(page string) (*template.Template, error) {
		return template.New("").Funcs(funcMap).ParseFS(templateFS,
			"templates/_partials.html",
			"templates/layout-v2.html",
			"templates/layout-dialup.html",
			"templates/"+page,
		)
	}

	tmplDashboard, err := parsePage("dashboard.html")
	if err != nil {
		return nil, err
	}
	tmplPhones, err := parsePage("phones.html")
	if err != nil {
		return nil, err
	}
	tmplCalls, err := parsePage("calls.html")
	if err != nil {
		return nil, err
	}
	tmplSettings, err := parsePage("settings.html")
	if err != nil {
		return nil, err
	}
	tmplOnboard, err := parsePage("onboard.html")
	if err != nil {
		return nil, err
	}
	tmplPhoneDetail, err := parsePage("phone-detail.html")
	if err != nil {
		return nil, err
	}
	tmplLinks, err := parsePage("links.html")
	if err != nil {
		return nil, err
	}
	tmplConnecting, err := parsePage("connecting.html")
	if err != nil {
		return nil, err
	}
	tmplCallLivePanel, err := template.New("call-live-panel").Funcs(funcMap).ParseFS(templateFS, "templates/_call-live-panel.html")
	if err != nil {
		return nil, fmt.Errorf("parse call-live-panel: %w", err)
	}
	tmplCallLiveDetail, err := parsePage("call-live-detail.html")
	if err != nil {
		return nil, fmt.Errorf("parse call-live-detail: %w", err)
	}
	// Merge the panel partial so {{template "call-live-panel"}} resolves inside the detail page.
	if _, err := tmplCallLiveDetail.ParseFS(templateFS, "templates/_call-live-panel.html"); err != nil {
		return nil, fmt.Errorf("parse call-live-panel partial into detail: %w", err)
	}

	u := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Non-browser clients (e.g. Pi daemon) send no Origin; allow them.
				return true
			}
			return origin == baseURL
		},
	}

	return &Handler{
		upgrader:           u,
		lineStore:          lineStore,
		deviceStore:        deviceStore,
		hub:                hub,
		tracker:            tracker,
		relay:              relay,
		healthStore:        healthStore,
		tmplDashboard:      tmplDashboard,
		tmplPhones:         tmplPhones,
		tmplCalls:          tmplCalls,
		tmplSettings:       tmplSettings,
		tmplOnboard:        tmplOnboard,
		tmplPhoneDetail:    tmplPhoneDetail,
		tmplLinks:          tmplLinks,
		tmplConnecting:     tmplConnecting,
		tmplCallLivePanel:  tmplCallLivePanel,
		tmplCallLiveDetail: tmplCallLiveDetail,
		cfg:                cfg,
		authStore:          authStore,
		authHandlers:       authHandlers,
		googleAuth:         googleAuth,
		householdStore:     householdStore,
		pairingStore:       pairingStore,
		linkStore:          linkStore,
		emailSender:        emailSender,
		baseURL:            baseURL,
		adminSecret:        adminSecret,
		authLimiter:        ratelimit.New(5, time.Minute),
		pairingLimiter:     ratelimit.New(5, time.Minute),
	}, nil
}

// Hub returns the signaling hub (used in tests).
func (h *Handler) Hub() *signaling.Hub {
	return h.hub
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	// Static assets — no auth required. In DevMode, serve from disk so
	// CSS/JS edits don't require a rebuild; otherwise serve the embedded
	// FS so the production binary is self-contained.
	mux.Handle("GET /static/", staticFileServer(h.cfg.DevMode, h.cfg.DevStaticDir))

	// Health check — no auth required
	mux.HandleFunc("GET /healthz", httputil.Healthz())

	// Public routes — no auth required
	mux.HandleFunc("GET /auth/login", h.authHandlers.HandleLoginPage)
	mux.Handle("POST /auth/magic", h.authLimiter.Middleware(http.HandlerFunc(h.authHandlers.HandleMagicLinkRequest)))
	mux.Handle("GET /auth/magic/{token}", ratelimit.New(10, time.Minute).Middleware(http.HandlerFunc(h.authHandlers.HandleMagicLinkVerify)))
	mux.HandleFunc("POST /auth/logout", h.authHandlers.HandleLogout)
	mux.HandleFunc("GET /auth/dev-session", h.authHandlers.HandleDevSession)
	mux.Handle("GET /auth/google/login", ratelimit.New(10, time.Minute).Middleware(http.HandlerFunc(h.googleAuth.HandleLogin)))
	mux.HandleFunc("GET /auth/google/callback", h.googleAuth.HandleCallback)
	mux.HandleFunc("GET /api/version", h.handleAPIVersion)
	mux.HandleFunc("GET /internal/stats", h.handleInternalStats)
	mux.HandleFunc("GET /ws", h.handleWS)

	// Update release index endpoint (unauthenticated — phones fetch this)
	if h.Releases != nil {
		mux.HandleFunc("GET /api/updates/releases", h.Releases.ServeReleases())
		slog.Info("updates: serving release index from GitHub")
	}
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/test-client.html")
	})

	// Dev-only routes: gated by DevMode, not registered in production builds.
	if h.cfg.DevMode {
		// Harness endpoint for /call/live e2e tests to seed an active call
		// without going through the full signaling dance.
		mux.HandleFunc("POST /test-harness/start-call", h.handleTestStartCall)
		// Seed a fake hub entry so e2e tests can exercise the firmware update
		// chip without a real device connection.
		mux.HandleFunc("POST /dev/seed-firmware", h.handleDevSeedFirmware)
	}

	// Protected routes — require valid session
	protected := http.NewServeMux()
	protected.HandleFunc("GET /", h.handleDashboard)
	protected.HandleFunc("GET /connecting", h.handleConnecting)
	protected.HandleFunc("GET /onboard", h.handleOnboardGet)
	protected.HandleFunc("POST /onboard", h.handleOnboardPost)
	protected.HandleFunc("GET /phones", h.handlePhonesGet)
	protected.HandleFunc("POST /phones", h.handlePhonesPost)
	protected.Handle("POST /phones/pair", h.pairingLimiter.Middleware(http.HandlerFunc(h.handlePhonesPairPost)))
	protected.HandleFunc("GET /phones/{number}", h.handlePhoneDetail)
	protected.HandleFunc("GET /phones/{number}/edit", h.handlePhoneEditGet)
	protected.HandleFunc("POST /phones/{number}/edit", h.handlePhoneEditPost)
	protected.HandleFunc("POST /phones/{number}/voice-style", h.handlePhoneVoiceStylePost)
	protected.HandleFunc("POST /phones/{number}/silent-mode", h.handlePhoneSilentModePost)
	protected.HandleFunc("POST /phones/{number}/delete", h.handlePhoneDelete)
	protected.HandleFunc("POST /phones/{number}/update", h.handlePhoneUpdate)
	protected.HandleFunc("GET /phones/{number}/online", h.handlePhoneOnline)
	protected.HandleFunc("GET /phones/{number}/update-status", h.handlePhoneUpdateStatus)
	protected.HandleFunc("POST /phones/{number}/factory-reset", h.handlePhoneFactoryReset)
	protected.HandleFunc("POST /phones/{number}/restart", h.handlePhoneRestart)
	protected.HandleFunc("GET /calls", h.handleCalls)
	protected.HandleFunc("GET /settings", h.handleSettings)
	protected.HandleFunc("POST /settings/household", h.handleSettingsHouseholdPost)
	protected.HandleFunc("POST /settings/call-history", h.handleSettingsCallHistory)
	protected.HandleFunc("POST /settings/timezone", h.handleSettingsTimezone)
	protected.HandleFunc("POST /settings/theme", h.handleSettingsTheme)
	protected.HandleFunc("POST /settings/crt-mode", h.handleSettingsCRTMode)
	protected.HandleFunc("GET /links", h.handleLinksGet)
	protected.HandleFunc("POST /links/invite", h.handleLinksInvitePost)
	protected.HandleFunc("POST /links/accept", h.handleLinksAcceptPost)
	protected.HandleFunc("POST /links/{id}/revoke", h.handleLinksRevokePost)
	protected.HandleFunc("GET /api/status", h.handleAPIStatus)
	protected.HandleFunc("GET /api/active-calls", h.handleAPIActiveCalls)
	protected.HandleFunc("GET /call/live/{id}", h.handleCallLiveDetail)
	protected.HandleFunc("GET /api/call/{id}/link-health", h.handleCallLinkHealth)
	protected.HandleFunc("GET /api/call/{id}/link-health/stream", h.handleCallLinkHealthStream)
	protected.HandleFunc("POST /api/call/{id}/disconnect", h.handleCallDisconnect)
	protected.HandleFunc("GET /api/lines/number-available", h.handleAPINumberAvailable)

	// Onboarding gate: redirect users without a household to /onboard
	// Only active when householdStore is set (nil means feature disabled)
	protectedHandler := h.authStore.RequireAuth(protected)
	if h.householdStore != nil {
		protectedHandler = h.authStore.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Don't gate the onboard routes themselves
			if r.URL.Path == "/onboard" || strings.HasPrefix(r.URL.Path, "/auth/") {
				protected.ServeHTTP(w, r)
				return
			}
			user := auth.UserFromContext(r.Context())
			if user != nil {
				if h.householdStore.NeedsOnboarding(user.ID) {
					http.Redirect(w, r, "/onboard", http.StatusSeeOther)
					return
				}
			}
			protected.ServeHTTP(w, r)
		}))
	}
	mux.Handle("/", protectedHandler)

	// Wrap with root-domain redirect before security headers.
	return rootDomainRedirect(h.baseURL, securityHeadersMiddleware(mux))
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' wss:; frame-ancestors 'none'")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// rootDomainRedirect redirects requests arriving on the bare root domain
// (e.g. digits.family) to the app URL (e.g. https://app.digits.family).
func rootDomainRedirect(appURL string, next http.Handler) http.Handler {
	if appURL == "" {
		return next
	}
	appHost := appURL
	if i := strings.Index(appURL, "://"); i >= 0 {
		appHost = appURL[i+3:]
	}
	if i := strings.Index(appHost, "/"); i >= 0 {
		appHost = appHost[:i]
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqHost := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			reqHost = h
		}
		appHostStripped := appHost
		if h, _, err := net.SplitHostPort(appHost); err == nil {
			appHostStripped = h
		}

		if reqHost == appHostStripped || reqHost == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Don't redirect WebSocket or API paths
		if strings.HasPrefix(r.URL.Path, "/ws") || strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		target := appURL + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

// ---- Onboarding ----
