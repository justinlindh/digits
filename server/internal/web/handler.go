package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
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
	"github.com/justinlindh/digits/server/internal/version"
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
	upgrader websocket.Upgrader
	lineStore   *line.Store
	deviceStore *device.Store
	hub         *signaling.Hub
	tracker     *calls.Tracker
	relay       *signaling.Relay
	healthStore *calls.HealthStore
	// Per-page template sets to avoid {{define}} name conflicts
	tmplDashboard    *template.Template
	tmplPhones       *template.Template
	tmplCalls        *template.Template
	tmplSettings     *template.Template
	tmplOnboard      *template.Template
	tmplPhoneDetail  *template.Template
	tmplLinks        *template.Template
	tmplConnecting   *template.Template
	tmplCallLivePanel  *template.Template
	tmplCallLiveDetail *template.Template
	cfg             HandlerConfig
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
		upgrader:        u,
		lineStore:       lineStore,
		deviceStore:     deviceStore,
		hub:             hub,
		tracker:         tracker,
		relay:             relay,
		healthStore:       healthStore,
		tmplDashboard:     tmplDashboard,
		tmplPhones:        tmplPhones,
		tmplCalls:         tmplCalls,
		tmplSettings:      tmplSettings,
		tmplOnboard:       tmplOnboard,
		tmplPhoneDetail:   tmplPhoneDetail,
		tmplLinks:         tmplLinks,
		tmplConnecting:    tmplConnecting,
		tmplCallLivePanel:  tmplCallLivePanel,
		tmplCallLiveDetail: tmplCallLiveDetail,
		cfg:             cfg,
		authStore:       authStore,
		authHandlers:    authHandlers,
		googleAuth:      googleAuth,
		householdStore:  householdStore,
		pairingStore:    pairingStore,
		linkStore:       linkStore,
		emailSender:     emailSender,
		baseURL:         baseURL,
		adminSecret:     adminSecret,
		authLimiter:     ratelimit.New(5, time.Minute),
		pairingLimiter:  ratelimit.New(5, time.Minute),
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

type onboardData struct {
	Page               string
	CallHistoryEnabled bool
	HouseholdName      string
	SuggestedName      string
	Version            string
	User               *auth.User
}

func (h *Handler) handleOnboardGet(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	suggested := "My Family"
	if user != nil && user.Name != "" {
		suggested = user.Name + "'s Family"
	}
	renderWith(w, h.tmplOnboard, layoutFor(r), onboardData{
		Page:               "onboard",
		Version:            version.Version,
		CallHistoryEnabled: h.callHistoryEnabled(r),
		SuggestedName:      suggested,
		User:               user,
	})
}

func (h *Handler) handleOnboardPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	if h.householdStore == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "My Family"
	}
	_, err := h.householdStore.Create(name, user.ID)
	if err != nil {
		slog.Error("create household failed", "err", err)
		http.Error(w, "failed to create household", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---- Dashboard ----

type dashboardData struct {
	Page                 string
	Version              string
	CallHistoryEnabled   bool
	HouseholdName        string
	Stats                dashStats
	Lines                []lineRow
	CallsTodayRecent     []callRow
	CallsTodayTotalMin   int
	LinkedFamilies       []linkedFamilyRow
	User                 *auth.User
}

type callRow struct {
	StartedAt time.Time
	LineName  string
	PeerName  string
	Direction string // "in" or "out"
	DurationS int
}

type dashStats struct {
	TotalLines  int
	OnlineLines int
	ActiveCalls int
}

type activePair struct {
	Caller string
	Callee string
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	active := h.tracker.Active()
	ld := h.buildLinesData(r, "")
	user := auth.UserFromContext(r.Context())
	hhName, callHistoryEnabled, loc := h.householdContext(r)

	// Determine current household ID for linked-family lookup.
	var householdID string
	if h.householdStore != nil && user != nil {
		if households, err := h.householdStore.GetForUser(user.ID); err == nil && len(households) > 0 {
			householdID = households[0].ID
		}
	}

	// Build set of own line numbers for active-call resolution and for
	// scoping call-history queries to this household.
	ownLineByNumber := make(map[string]*lineRow, len(ld.Lines))
	ownNumbers := make([]string, 0, len(ld.Lines))
	for i := range ld.Lines {
		ownLineByNumber[ld.Lines[i].Line.Number] = &ld.Lines[i]
		ownNumbers = append(ownNumbers, ld.Lines[i].Line.Number)
	}

	// Build linked-family index for peer-name resolution.
	linkedFamilies := h.buildLinkedFamilies(householdID)
	linkedLineIndex := buildLinkedLineIndex(linkedFamilies)

	// Annotate lines with active-call state and count household-scoped active
	// calls. When both sides of the call are own lines (intra-household),
	// each card uses the other local line's name as its peer instead of a
	// phone-number fallback.
	var activeCount int
	for _, pair := range active {
		if ownLineByNumber[pair.Caller] != nil || ownLineByNumber[pair.Callee] != nil {
			activeCount++
		}
		callerRow := ownLineByNumber[pair.Caller]
		calleeRow := ownLineByNumber[pair.Callee]
		elapsed := fmtElapsed(time.Since(pair.StartedAt))
		if callerRow != nil {
			callerRow.OnCall = true
			callerRow.OnCallElapsed = elapsed
			if calleeRow != nil {
				callerRow.OnCallPeerName = calleeRow.Line.Name
			} else {
				callerRow.OnCallPeerName = resolvePeerName(pair.Callee, linkedLineIndex)
			}
			if id, ok := h.tracker.CallIDFor(callerRow.Line.Number); ok {
				callerRow.OnCallID = id
			}
		}
		if calleeRow != nil {
			calleeRow.OnCall = true
			calleeRow.OnCallElapsed = elapsed
			if callerRow != nil {
				calleeRow.OnCallPeerName = callerRow.Line.Name
			} else {
				calleeRow.OnCallPeerName = resolvePeerName(pair.Caller, linkedLineIndex)
			}
			if id, ok := h.tracker.CallIDFor(calleeRow.Line.Number); ok {
				calleeRow.OnCallID = id
			}
		}
	}

	// Build today's call rows when history is enabled. Scoped to this
	// household's own line numbers so one family never sees another's
	// activity. "Today" is relative to the household's configured timezone,
	// not server-UTC.
	var callsTodayRecent []callRow
	var callsTodayTotalSec int
	if callHistoryEnabled && len(ownNumbers) > 0 {
		recent, _ := h.tracker.RecentForPhones(ownNumbers, 20)
		now := time.Now().In(loc)
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		for _, c := range recent {
			if !c.StartedAt.After(today) {
				continue
			}
			var direction, lineName, peerNumber string
			if lr := ownLineByNumber[c.Caller]; lr != nil {
				direction = "out"
				lineName = lr.Line.Name
				peerNumber = c.Callee
			} else if lr := ownLineByNumber[c.Callee]; lr != nil {
				direction = "in"
				lineName = lr.Line.Name
				peerNumber = c.Caller
			} else {
				continue
			}
			callsTodayRecent = append(callsTodayRecent, callRow{
				StartedAt: c.StartedAt.In(loc),
				LineName:  lineName,
				PeerName:  resolvePeerName(peerNumber, linkedLineIndex),
				Direction: direction,
				DurationS: c.DurationS,
			})
			callsTodayTotalSec += c.DurationS
		}
	}

	data := dashboardData{
		Page:               "dashboard",
		Version:            version.Version,
		CallHistoryEnabled: callHistoryEnabled,
		HouseholdName:      hhName,
		Stats: dashStats{
			TotalLines:  len(ld.Lines),
			OnlineLines: countOnline(ld.Lines),
			ActiveCalls: activeCount,
		},
		Lines:              ld.Lines,
		CallsTodayRecent:   callsTodayRecent,
		CallsTodayTotalMin: (callsTodayTotalSec + 30) / 60, // +30 to round to nearest minute
		LinkedFamilies:     linkedFamilies,
		User:               user,
	}
	renderWith(w, h.tmplDashboard, layoutFor(r), data)
}

// buildLinkedFamilies fetches the list of linked households and their lines
// for the given householdID. Returns an empty slice if householdID is empty or
// the lookup fails.
func (h *Handler) buildLinkedFamilies(householdID string) []linkedFamilyRow {
	if householdID == "" || h.linkStore == nil {
		return nil
	}
	activeLinks, err := h.linkStore.GetLinkedHouseholds(householdID)
	if err != nil {
		slog.Error("buildLinkedFamilies: get linked households failed", "err", err)
		return nil
	}
	otherIDs := make([]string, 0, len(activeLinks))
	for _, l := range activeLinks {
		otherID := l.HouseholdAID
		if otherID == householdID && l.HouseholdBID != nil {
			otherID = *l.HouseholdBID
		}
		otherIDs = append(otherIDs, otherID)
	}
	linesByHousehold, err := h.lineStore.ListByHouseholds(otherIDs)
	if err != nil {
		slog.Error("buildLinkedFamilies: batch list lines failed", "err", err)
	}
	var families []linkedFamilyRow
	for i, l := range activeLinks {
		otherID := otherIDs[i]
		otherName := otherID
		if other, err := h.householdStore.GetByID(otherID); err == nil {
			otherName = other.Name
		}
		families = append(families, linkedFamilyRow{
			ID:         l.ID,
			Name:       otherName,
			Lines:      linesByHousehold[otherID],
			Status:     l.Status,
			AcceptedAt: l.AcceptedAt,
		})
	}
	return families
}

// buildLinkedLineIndex flattens a slice of linkedFamilyRow into a map from
// line number to "FamilyName · LineName" for fast peer-name resolution.
func buildLinkedLineIndex(families []linkedFamilyRow) map[string]string {
	index := make(map[string]string)
	for _, f := range families {
		for _, l := range f.Lines {
			index[l.Number] = f.Name + " · " + l.Name
		}
	}
	return index
}

// resolvePeerName returns the friendly name for a peer number using the linked
// line index, falling back to fmtPhone formatting.
func resolvePeerName(number string, linkedLines map[string]string) string {
	if name, ok := linkedLines[number]; ok {
		return name
	}
	return line.FormatNumber(number)
}

func countOnline(lines []lineRow) int {
	n := 0
	for _, l := range lines {
		if l.Online {
			n++
		}
	}
	return n
}

// fmtElapsed renders a duration as "m:ss" for short calls and "h:mm:ss"
// for calls that run past an hour. Used by the Dashboard active-call callout.
func fmtElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// ---- Lines (Phones) ----

type pairSuccess struct {
	Name            string
	FirmwareVersion string
}

type linesData struct {
	Page                  string
	Version               string
	CallHistoryEnabled    bool
	HouseholdName         string
	Lines                 []lineRow
	Error                 string
	PairError             string
	PairSuccess           *pairSuccess
	User                  *auth.User
	LatestPiVersion       string
	LatestFirmwareVersion string
}

type lineRow struct {
	Line                line.Line
	Online              bool
	OnCall              bool
	OnCallPeerName      string
	OnCallElapsed       string // "mm:ss" for the Dashboard room-card callout
	OnCallID            int64  // 0 when not on a call; otherwise the active call id
	DeviceInfo          *signaling.DeviceInfoSnapshot
	FirmwareUpdateNotes []updates.Release
	PiUpdateNotes       []updates.Release
}

func (h *Handler) buildLinesData(r *http.Request, errMsg string) linesData {
	var user *auth.User
	if r != nil {
		user = auth.UserFromContext(r.Context())
	}

	var lines []line.Line

	// Scope to household if user has one and householdStore is available
	if user != nil && h.householdStore != nil {
		households, err := h.householdStore.GetForUser(user.ID)
		if err == nil && len(households) > 0 {
			lines, _ = h.lineStore.ListByHousehold(households[0].ID)
		}
	}

	// If household lookup failed, show empty list rather than leaking all lines
	if lines == nil {
		lines = []line.Line{}
	}

	online := h.hub.OnlineNumbers()
	onlineSet := make(map[string]bool, len(online))
	for _, n := range online {
		onlineSet[n] = true
	}

	var (
		idx                *updates.ReleaseIndex
		latestPi, latestFw string
	)
	if h.Releases != nil {
		idx = h.Releases.ReleaseIndex()
	}
	if idx != nil {
		latestPi = idx.Pi.Latest
		latestFw = idx.Firmware.Latest
	}

	rows := make([]lineRow, len(lines))
	for i, l := range lines {
		info := h.hub.DeviceInfo(l.Number)
		row := lineRow{Line: l, Online: onlineSet[l.Number], DeviceInfo: info}
		if idx != nil && info != nil {
			if latestFw != "" && info.FirmwareVersion != "" && updates.CompareSemver(info.FirmwareVersion, latestFw) < 0 {
				row.FirmwareUpdateNotes = idx.RangeReleases(updates.ComponentFirmware, info.FirmwareVersion, latestFw)
			}
			if latestPi != "" && info.PiVersion != "" && updates.CompareSemver(info.PiVersion, latestPi) < 0 {
				row.PiUpdateNotes = idx.RangeReleases(updates.ComponentPi, info.PiVersion, latestPi)
			}
		}
		rows[i] = row
	}
	return linesData{
		Page:                  "phones",
		Version:               version.Version,
		CallHistoryEnabled:    h.callHistoryEnabled(r),
		HouseholdName:         h.householdNameFromContext(r),
		Lines:                 rows,
		Error:                 errMsg,
		User:                  user,
		LatestPiVersion:       latestPi,
		LatestFirmwareVersion: latestFw,
	}
}

func (h *Handler) handlePhonesGet(w http.ResponseWriter, r *http.Request) {
	data := h.buildLinesData(r, "")
	if pairedName := r.URL.Query().Get("paired"); pairedName != "" {
		data.PairSuccess = &pairSuccess{
			Name:            pairedName,
			FirmwareVersion: r.URL.Query().Get("fw"),
		}
	}
	renderWith(w, h.tmplPhones, layoutFor(r), data)
}

func (h *Handler) handlePhonesPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	number := line.StripNumber(strings.TrimSpace(r.FormValue("number")))
	name := strings.TrimSpace(r.FormValue("name"))

	// Get user's household to associate the new line
	var householdID string
	if h.householdStore != nil {
		user := auth.UserFromContext(r.Context())
		if user != nil {
			households, err := h.householdStore.GetForUser(user.ID)
			if err == nil && len(households) > 0 {
				householdID = households[0].ID
			}
		}
	}

	if err := line.ValidateNumber(number); err != nil {
		data := h.buildLinesData(r, err.Error())
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	_, err := h.lineStore.Add(number, name, householdID)
	data := h.buildLinesData(r, "")
	if err != nil {
		data = h.buildLinesData(r, err.Error())
	}

	if isHTMX(r) {
		renderWith(w, h.tmplPhones, "phones-table", data)
		return
	}
	if err != nil {
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}
	http.Redirect(w, r, "/phones", http.StatusSeeOther)
}

func (h *Handler) handlePhonesPairPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))
	number := line.StripNumber(strings.TrimSpace(r.FormValue("number")))
	name := strings.TrimSpace(r.FormValue("name"))

	if err := line.ValidateNumber(number); err != nil {
		data := h.buildLinesData(r, "")
		data.PairError = "invalid phone number: " + err.Error()
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	if h.pairingStore == nil {
		data := h.buildLinesData(r, "")
		data.PairError = "pairing is not enabled"
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	// Get user's household
	var householdID string
	if h.householdStore != nil {
		user := auth.UserFromContext(r.Context())
		if user != nil {
			households, err := h.householdStore.GetForUser(user.ID)
			if err == nil && len(households) > 0 {
				householdID = households[0].ID
			}
		}
	}
	if householdID == "" {
		data := h.buildLinesData(r, "")
		data.PairError = "no household found — please complete onboarding first"
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	token, hwID, err := h.pairingStore.ClaimDevice(code, number, name, householdID)
	if err != nil {
		data := h.buildLinesData(r, "")
		data.PairError = err.Error()
		renderWith(w, h.tmplPhones, layoutFor(r), data)
		return
	}

	if hwID != "" {
		if err := h.hub.SendToHardware(hwID, &signaling.Message{
			Type:        signaling.TypePaired,
			DeviceToken: token,
			Number:      number,
		}); err != nil {
			slog.Warn("could not notify device of pairing", "hardware_id", hwID, "err", err)
		}
	}

	v := url.Values{}
	v.Set("paired", name)
	http.Redirect(w, r, "/phones?"+v.Encode(), http.StatusSeeOther)
}

type lineDetailData struct {
	Page                  string
	Version               string
	CallHistoryEnabled    bool
	HouseholdName         string
	Line                  line.Line
	Online                bool
	Devices               []device.Device
	DeviceInfo            *signaling.DeviceInfoSnapshot
	LastSeenAt            *time.Time
	LatestPiVersion       string
	LatestFirmwareVersion string
	PiReleases            []updates.Release
	FWReleases            []updates.Release
	User                  *auth.User
}

func (h *Handler) handlePhoneDetail(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	online := h.hub.Get(number) != nil

	var devices []device.Device
	if h.deviceStore != nil {
		var err error
		devices, err = h.deviceStore.ListByLine(ln.ID)
		if err != nil {
			slog.Error("failed to list devices by line", "err", err, "line_id", ln.ID)
		}
	}

	// For online devices, use the real-time in-memory timestamp from the Hub.
	// For offline devices, fall back to the last disconnect time from the DB.
	var lastSeenAt *time.Time
	if online {
		lastSeenAt = h.hub.LastSeenAt(number)
	} else {
		for _, d := range devices {
			if d.LastSeenAt != nil && (lastSeenAt == nil || d.LastSeenAt.After(*lastSeenAt)) {
				lastSeenAt = d.LastSeenAt
			}
		}
	}

	var latestPi, latestFw string
	var piReleases, fwReleases []updates.Release
	if h.Releases != nil {
		if idx := h.Releases.ReleaseIndex(); idx != nil {
			latestPi = idx.Pi.Latest
			latestFw = idx.Firmware.Latest
			piReleases = idx.SortedReleases(updates.ComponentPi)
			fwReleases = idx.SortedReleases(updates.ComponentFirmware)
		}
	}

	hhName, callHistory, loc := h.householdContext(r)

	if lastSeenAt != nil {
		t := lastSeenAt.In(loc)
		lastSeenAt = &t
	}

	devInfo := h.hub.DeviceInfo(number)

	user := auth.UserFromContext(r.Context())

	renderWith(w, h.tmplPhoneDetail, layoutFor(r), lineDetailData{
		Page:                  "phones",
		Version:               version.Version,
		CallHistoryEnabled:    callHistory,
		HouseholdName:         hhName,
		Line:                  *ln,
		Online:                online,
		Devices:               devices,
		DeviceInfo:            devInfo,
		LastSeenAt:            lastSeenAt,
		LatestPiVersion:       latestPi,
		LatestFirmwareVersion: latestFw,
		PiReleases:            piReleases,
		FWReleases:            fwReleases,
		User:                  user,
	})
}

func (h *Handler) handlePhoneOnline(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.requireNumberOwnership(w, r, number) {
		return
	}
	online := h.hub.Get(number) != nil
	if isHTMX(r) {
		renderWith(w, h.tmplPhoneDetail, "phone-status", struct{ Online bool }{online})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"online": online}); err != nil {
		slog.Error("encode online status failed", "err", err)
	}
}

func (h *Handler) handlePhoneEditGet(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	online := h.hub.Get(number) != nil
	renderWith(w, h.tmplPhones, "phone-edit-row", lineRow{Line: *ln, Online: online})
}

func (h *Handler) handlePhoneEditPost(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))

	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}

	if err := h.lineStore.Update(ln.ID, number, name); err != nil {
		slog.Error("line update failed", "err", err, "line_id", ln.ID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := h.buildLinesData(r, "")
	if isHTMX(r) {
		renderWith(w, h.tmplPhones, "phones-table", data)
		return
	}
	http.Redirect(w, r, "/phones", http.StatusSeeOther)
}

func (h *Handler) handlePhoneVoiceStylePost(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.FormValue("voice_style"))
	if raw == "" {
		http.Error(w, "missing voice_style", http.StatusBadRequest)
		return
	}
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	next := ln.Settings
	next.VoiceStyle = raw
	next = next.Normalize()
	if next.VoiceStyle != ln.Settings.VoiceStyle {
		if err := h.lineStore.UpdateSettings(ln.ID, next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.pushLineSettings(number, next); err != nil {
			slog.Warn("push line settings failed", "number", number, "err", err)
		}
		ln.Settings = next
	}
	if isHTMX(r) {
		renderWith(w, h.tmplPhoneDetail, "voice-style-section", struct {
			Line line.Line
		}{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneSilentModePost(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	silent := strings.TrimSpace(r.FormValue("silent_mode")) == "on"

	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	next := ln.Settings
	next.SilentMode = silent
	next = next.Normalize()
	if next.SilentMode != ln.Settings.SilentMode {
		if err := h.lineStore.UpdateSettings(ln.ID, next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.pushLineSettings(number, next); err != nil {
			slog.Warn("push line settings failed", "number", number, "err", err)
		}
		ln.Settings = next
	}
	if isHTMX(r) {
		renderWith(w, h.tmplPhoneDetail, "silent-mode-section", struct {
			Line line.Line
		}{Line: *ln})
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

// pushLineSettings sends the updated settings to the device currently
// registered as the given number, if any. A missing device is not an error;
// the next time that device reconnects it will receive the latest settings
// via the registration push in relay.OnRegistered.
func (h *Handler) pushLineSettings(number string, settings line.Settings) error {
	err := h.hub.SendTo(number, &signaling.Message{
		Type: signaling.TypeLineSettings,
		To:   number,
		LineSettings: &signaling.LineSettings{
			VoiceStyle: settings.VoiceStyle,
			SilentMode: settings.SilentMode,
		},
	})
	if errors.Is(err, signaling.ErrNotConnected) {
		return nil
	}
	return err
}

func (h *Handler) handlePhoneUpdate(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.requireNumberOwnership(w, r, number) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	targetPi := strings.TrimSpace(r.FormValue("target_pi_version"))
	targetFW := strings.TrimSpace(r.FormValue("target_fw_version"))

	// Clear any stale status before sending new trigger
	h.hub.ClearUpdateStatus(number)

	msg := &signaling.Message{
		Type:            signaling.TypeUpdateTrigger,
		TargetPiVersion: targetPi,
		TargetFWVersion: targetFW,
	}

	var sendErr string
	if err := h.hub.SendTo(number, msg); err != nil {
		slog.Warn("update trigger failed", "number", number, "err", err)
		sendErr = err.Error()
	} else {
		slog.Info("update trigger sent", "number", number, "target_pi", targetPi, "target_fw", targetFW)
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if sendErr != "" {
			w.WriteHeader(http.StatusBadGateway)
			if err := json.NewEncoder(w).Encode(map[string]string{"error": sendErr}); err != nil {
				slog.Error("phone update: json encode failed", "err", err)
			}
		} else {
			if err := json.NewEncoder(w).Encode(map[string]string{"status": "triggered"}); err != nil {
				slog.Error("phone update: json encode failed", "err", err)
			}
		}
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneUpdateStatus(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.requireNumberOwnership(w, r, number) {
		return
	}
	status := h.hub.GetUpdateStatus(number)
	w.Header().Set("Content-Type", "application/json")
	if status == nil {
		if err := json.NewEncoder(w).Encode(map[string]string{"status": ""}); err != nil {
			slog.Error("update status: json encode failed", "err", err)
		}
		return
	}
	if err := json.NewEncoder(w).Encode(status); err != nil {
		slog.Error("update status: json encode failed", "err", err)
	}
}

func (h *Handler) handlePhoneFactoryReset(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.requireNumberOwnership(w, r, number) {
		return
	}

	h.hub.ClearUpdateStatus(number)

	msg := &signaling.Message{
		Type: signaling.TypeFactoryReset,
	}

	var sendErr string
	if err := h.hub.SendTo(number, msg); err != nil {
		slog.Warn("factory reset trigger failed", "number", number, "err", err)
		sendErr = err.Error()
	} else {
		slog.Info("factory reset triggered", "number", number)
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if sendErr != "" {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": sendErr})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "triggered"})
		}
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneRestart(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !h.requireNumberOwnership(w, r, number) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mode := strings.TrimSpace(r.FormValue("mode"))

	if mode != "service" && mode != "reboot" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "mode must be 'service' or 'reboot'"})
		return
	}

	msg := &signaling.Message{
		Type:        signaling.TypeRestart,
		RestartMode: mode,
	}

	var sendErr string
	if err := h.hub.SendTo(number, msg); err != nil {
		slog.Warn("restart command failed", "number", number, "mode", mode, "err", err)
		sendErr = err.Error()
	} else {
		slog.Info("restart command sent", "number", number, "mode", mode)
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if sendErr != "" {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": sendErr})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "triggered"})
		}
		return
	}
	http.Redirect(w, r, "/phones/"+number, http.StatusSeeOther)
}

func (h *Handler) handlePhoneDelete(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	ln := h.requireLineOwnership(w, r, number)
	if ln == nil {
		return
	}
	if err := h.lineStore.Delete(ln.ID); err != nil {
		slog.Error("delete line failed", "line_id", ln.ID, "err", err)
		http.Error(w, "failed to delete line", http.StatusInternalServerError)
		return
	}
	data := h.buildLinesData(r, "")
	if isHTMX(r) {
		renderWith(w, h.tmplPhones, "phones-table", data)
		return
	}
	http.Redirect(w, r, "/phones", http.StatusSeeOther)
}

// ---- Calls ----

type callsData struct {
	Page               string
	Version            string
	CallHistoryEnabled bool
	HouseholdName      string
	Entries            []calls.HistoryEntry
	User               *auth.User
}

func (h *Handler) handleCalls(w http.ResponseWriter, r *http.Request) {
	hhName, callHistory, loc := h.householdContext(r)
	if !callHistory {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	var entries []calls.HistoryEntry

	// Scope call log to the user's household lines
	user := auth.UserFromContext(r.Context())
	if user != nil && h.lineStore != nil && h.householdStore != nil {
		households, err := h.householdStore.GetForUser(user.ID)
		if err != nil {
			slog.Error("get households for user failed", "user_id", user.ID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if len(households) > 0 {
			lines, err := h.lineStore.ListByHousehold(households[0].ID)
			if err != nil {
				slog.Error("list lines for household failed", "household_id", households[0].ID, "err", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if len(lines) > 0 {
				numbers := make([]string, len(lines))
				for i, l := range lines {
					numbers[i] = l.Number
				}
				hist, err := h.tracker.RecentHistoryForPhones(numbers, 100)
				if err != nil {
					slog.Error("query recent history failed", "err", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				entries = hist
			}
		}
	}
	if entries == nil {
		entries = []calls.HistoryEntry{}
	}

	// Localize timestamps for display.
	for i := range entries {
		if entries[i].Call != nil {
			entries[i].Call.StartedAt = entries[i].Call.StartedAt.In(loc)
		}
		if entries[i].Conference != nil {
			entries[i].Conference.CreatedAt = entries[i].Conference.CreatedAt.In(loc)
		}
		entries[i].SortTime = entries[i].SortTime.In(loc)
	}

	renderWith(w, h.tmplCalls, layoutFor(r), callsData{Page: "calls", Version: version.Version, CallHistoryEnabled: callHistory, HouseholdName: hhName, Entries: entries, User: user})
}

// ---- Settings ----

type settingsData struct {
	Page               string
	Version            string
	CallHistoryEnabled bool
	HouseholdName      string
	User               *auth.User
	Household          *household.Household
	Saved              bool
}

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	var hh *household.Household
	if user != nil && h.householdStore != nil {
		households, _ := h.householdStore.GetForUser(user.ID)
		if len(households) > 0 {
			hh = households[0]
		}
	}
	hhName := ""
	if hh != nil {
		hhName = hh.Name
	}
	renderWith(w, h.tmplSettings, layoutFor(r), settingsData{
		Page:               "settings",
		Version:            version.Version,
		CallHistoryEnabled: h.callHistoryEnabled(r),
		HouseholdName:      hhName,
		User:               user,
		Household:          hh,
		Saved:              r.URL.Query().Get("saved") == "1",
	})
}

func (h *Handler) handleSettingsHouseholdPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	households, _ := h.householdStore.GetForUser(user.ID)
	if len(households) == 0 {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name != "" {
		if err := h.householdStore.UpdateName(households[0].ID, name); err != nil {
			slog.Error("update household name failed", "household_id", households[0].ID, "err", err)
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// ---- Links (Connected Families) ----

type linksData struct {
	Page            string
	Version         string
	CallHistoryEnabled bool
	HouseholdName   string
	LinkedFamilies  []linkedFamilyRow
	PendingInvites  []linkRow
	CreatedCode     string
	Accepted        bool
	Revoked         bool
	Canceled        bool
	Conflicts       string
	Error           string
	User            *auth.User
}

type linkedFamilyRow struct {
	ID          string
	Name        string
	Lines       []line.Line
	Status      string
	AcceptedAt  *time.Time
}

type linkRow struct {
	ID             string
	OtherHousehold string
	InviteCode     string
	Status         string
	CreatedAt      time.Time
	AcceptedAt     *time.Time
}

func (h *Handler) handleLinksGet(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}
	myHousehold := households[0]

	data := linksData{
		Page:               "links",
		Version:            version.Version,
		CallHistoryEnabled: h.callHistoryEnabled(r),
		HouseholdName:      myHousehold.Name,
		CreatedCode:        r.URL.Query().Get("created"),
		Accepted:           r.URL.Query().Get("accepted") == "1",
		Revoked:            r.URL.Query().Get("revoked") == "1",
		Canceled:           r.URL.Query().Get("canceled") == "1",
		Conflicts:          r.URL.Query().Get("conflicts"),
		Error:              r.URL.Query().Get("error"),
		User:               user,
	}

	// Active links — build connected family directory
	data.LinkedFamilies = h.buildLinkedFamilies(myHousehold.ID)

	// Pending invites sent by this household
	pending, err := h.linkStore.GetPendingForHousehold(myHousehold.ID)
	if err != nil {
		slog.Error("get pending links failed", "err", err)
	}
	for _, l := range pending {
		data.PendingInvites = append(data.PendingInvites, linkRow{
			ID:         l.ID,
			InviteCode: l.InviteCode,
			Status:     l.Status,
			CreatedAt:  l.CreatedAt,
		})
	}

	renderWith(w, h.tmplLinks, layoutFor(r), data)
}

func (h *Handler) handleLinksInvitePost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}
	myHousehold := households[0]

	link, err := h.linkStore.CreateInvite(myHousehold.ID, user.ID)
	if err != nil {
		slog.Error("create invite failed", "err", err)
		http.Redirect(w, r, "/links?error="+err.Error(), http.StatusSeeOther)
		return
	}

	// Send email notification to the creating user with the invite code
	if h.emailSender != nil && user.Email != "" {
		subj, body := email.HouseholdInviteEmail(myHousehold.Name, link.InviteCode, h.baseURL)
		if err := h.emailSender.Send(user.Email, subj, body); err != nil {
			slog.Error("send invite email failed", "err", err)
		}
	}

	http.Redirect(w, r, "/links?created="+link.InviteCode, http.StatusSeeOther)
}

func (h *Handler) handleLinksAcceptPost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}
	myHousehold := households[0]

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(strings.ToUpper(r.FormValue("code")))
	if code == "" {
		http.Redirect(w, r, "/links?error=invite+code+required", http.StatusSeeOther)
		return
	}

	link, err := h.linkStore.AcceptInvite(code, user.ID, myHousehold.ID)
	if err != nil {
		slog.Error("accept invite failed", "err", err)
		http.Redirect(w, r, "/links?error="+err.Error(), http.StatusSeeOther)
		return
	}

	// Check for number conflicts between the two households
	bID := ""
	if link.HouseholdBID != nil {
		bID = *link.HouseholdBID
	}
	conflicts, _ := h.linkStore.FindNumberConflicts(link.HouseholdAID, bID)
	if len(conflicts) > 0 {
		var names []string
		for _, c := range conflicts {
			names = append(names, c.Number)
		}
		slog.Warn("number conflicts on link accept", "conflicts", names)
		http.Redirect(w, r, "/links?accepted=1&conflicts="+strings.Join(names, ","), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/links?accepted=1", http.StatusSeeOther)
}

func (h *Handler) handleLinksRevokePost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	id := r.PathValue("id")

	// Look up the link and verify the user belongs to one of the linked
	// households before allowing revocation.
	link, err := h.linkStore.GetByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if h.householdStore == nil {
		http.NotFound(w, r)
		return
	}
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		http.NotFound(w, r)
		return
	}
	owned := false
	for _, hh := range households {
		if hh.ID == link.HouseholdAID || (link.HouseholdBID != nil && hh.ID == *link.HouseholdBID) {
			owned = true
			break
		}
	}
	if !owned {
		http.NotFound(w, r)
		return
	}

	wasPending := link.Status == "pending"

	if err := h.linkStore.RevokeLink(id, user.ID); err != nil {
		slog.Error("revoke link failed", "link_id", id, "err", err)
		http.Redirect(w, r, "/links?error="+err.Error(), http.StatusSeeOther)
		return
	}

	target := "/links?revoked=1"
	if wasPending {
		target = "/links?canceled=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// ---- Internal Stats ----

func (h *Handler) handleInternalStats(w http.ResponseWriter, r *http.Request) {
	if h.adminSecret == "" || r.Header.Get("X-Admin-Secret") != h.adminSecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	lineCount := 0
	if h.lineStore != nil {
		lines, err := h.lineStore.List()
		if err != nil {
			slog.Error("stats: list lines failed", "err", err)
			jsonError(w, "internal server error", http.StatusInternalServerError)
			return
		}
		lineCount = len(lines)
	}

	onlineCount := 0
	if h.hub != nil {
		onlineCount = len(h.hub.OnlineNumbers())
	}

	activeCallCount := 0
	if h.tracker != nil {
		activeCallCount = len(h.tracker.Active())
	}

	totalUsers := 0
	if h.authStore != nil {
		var err error
		totalUsers, err = h.authStore.CountUsers()
		if err != nil {
			slog.Error("stats: count users failed", "err", err)
			jsonError(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	totalHouseholds := 0
	if h.householdStore != nil {
		var err error
		totalHouseholds, err = h.householdStore.CountHouseholds()
		if err != nil {
			slog.Error("stats: count households failed", "err", err)
			jsonError(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	totalLinks := 0
	if h.linkStore != nil {
		var err error
		totalLinks, err = h.linkStore.CountActiveLinks()
		if err != nil {
			slog.Error("stats: count active links failed", "err", err)
			jsonError(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"total_users":      totalUsers,
		"total_households": totalHouseholds,
		"total_lines":      lineCount,
		"online_lines":     onlineCount,
		"active_calls":     activeCallCount,
		"total_links":      totalLinks,
	}); err != nil {
		slog.Error("stats: json encode failed", "err", err)
	}
}

// ---- API ----

func (h *Handler) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	ld := h.buildLinesData(r, "")
	nums := h.householdNumbers(r)

	var onlineCount int
	for _, row := range ld.Lines {
		if row.Online {
			onlineCount++
		}
	}

	allActive := h.tracker.Active()
	var activeCount int
	for _, a := range allActive {
		if nums[a.Caller] || nums[a.Callee] {
			activeCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"total_lines":  len(ld.Lines),
		"online_lines": onlineCount,
		"active_calls": activeCount,
	}); err != nil {
		slog.Error("api status: json encode failed", "err", err)
	}
}

func (h *Handler) handleAPIActiveCalls(w http.ResponseWriter, r *http.Request) {
	nums := h.householdNumbers(r)
	allActive := h.tracker.Active()
	var pairs []activePair
	for _, a := range allActive {
		if nums[a.Caller] || nums[a.Callee] {
			pairs = append(pairs, activePair{Caller: a.Caller, Callee: a.Callee})
		}
	}
	if pairs == nil {
		pairs = []activePair{}
	}

	// Return HTML for htmx, JSON for API clients
	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if len(pairs) == 0 {
			_, _ = fmt.Fprint(w, `<div class="px-4 py-8 text-center text-[#8b949e] text-sm">No active calls</div>`)
			return
		}
		for _, p := range pairs {
			_, _ = fmt.Fprintf(w, `<div class="px-4 py-3 border-b border-[#21262d] last:border-0"><div class="flex items-center gap-2"><span class="inline-block w-2 h-2 rounded-full bg-[#3fb950] animate-pulse shrink-0"></span><span class="font-mono text-sm text-[#e6edf3]">%s</span><span class="text-[#8b949e] text-xs">→</span><span class="font-mono text-sm text-[#e6edf3]">%s</span></div></div>`, template.HTMLEscapeString(p.Caller), template.HTMLEscapeString(p.Callee))
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(pairs); err != nil {
		slog.Error("active calls: json encode failed", "err", err)
	}
}

func (h *Handler) handleAPINumberAvailable(w http.ResponseWriter, r *http.Request) {
	number := line.StripNumber(r.URL.Query().Get("number"))
	if err := line.ValidateNumber(number); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	exists, err := h.lineStore.NumberExists(number)
	if err != nil {
		slog.Error("number available check failed", "err", err)
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"available": !exists}) //nolint:errcheck
}

// ---- WebSocket ----

// wsReject sends an error message to the WebSocket client and closes the connection.
func wsReject(ws *websocket.Conn, errMsg string) {
	_ = ws.WriteMessage(websocket.TextMessage, mustMarshal(&signaling.Message{
		Type:  signaling.TypeError,
		Error: errMsg,
	}))
	_ = ws.Close()
}

func (h *Handler) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "err", err)
		return
	}

	// Wait for register message
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil {
		slog.Error("websocket no register message", "err", err)
		_ = ws.Close()
		return
	}
	_ = ws.SetReadDeadline(time.Time{})

	msg, err := signaling.ParseMessage(data)
	if err != nil || msg.Type != signaling.TypeRegister || msg.Number == "" {
		slog.Warn("invalid register message")
		wsReject(ws, "must send register message first")
		return
	}

	// Require hardware ID for all connections
	if msg.HardwareID == "" {
		slog.Warn("ws register without hardware_id", "number", msg.Number)
		wsReject(ws, "hardware_id required")
		return
	}

	// Check pairing and token status
	if h.pairingStore != nil {
		paired, tokenValid, err := h.deviceStore.AuthStatus(msg.HardwareID, msg.DeviceToken)
		if err != nil {
			slog.Error("device auth check failed", "hardware_id", msg.HardwareID, "err", err)
			wsReject(ws, "internal error")
			return
		}
		if !paired {
			code, err := h.pairingStore.GenerateCode(msg.HardwareID)
			if err != nil {
				slog.Error("generate pairing code failed", "hardware_id", msg.HardwareID, "err", err)
			} else {
				_ = ws.WriteMessage(websocket.TextMessage, mustMarshal(&signaling.Message{
					Type:        signaling.TypePairingCode,
					PairingCode: code,
				}))
			}
			// Continue to register so the device can receive the TypePaired message
		} else if msg.DeviceToken == "" {
			slog.Warn("ws register without device_token", "hardware_id", msg.HardwareID)
			wsReject(ws, "device_token required")
			return
		} else if !tokenValid {
			slog.Warn("ws invalid device_token", "hardware_id", msg.HardwareID)
			wsReject(ws, "invalid device_token")
			return
		}
	}

	const (
		wsPingInterval = 30 * time.Second
		wsPongTimeout  = 45 * time.Second
		wsWriteTimeout = 10 * time.Second
	)

	conn := &signaling.Conn{
		WS:         ws,
		HardwareID: msg.HardwareID,
		Send:       make(chan []byte, 32),
		LastSeen:   time.Now(),
	}
	h.hub.Register(msg.Number, conn)
	h.relay.OnRegistered(msg.Number)
	number := msg.Number

	// Configure pong handler to extend read deadline on each pong
	_ = ws.SetReadDeadline(time.Now().Add(wsPongTimeout))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(wsPongTimeout))
		h.hub.TouchLastSeen(number)
		return nil
	})

	// Write pump with periodic pings
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		defer func() { _ = ws.Close() }()
		for {
			select {
			case data, ok := <-conn.Send:
				if !ok {
					return
				}
				if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
					return
				}
				if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
					slog.Error("websocket write failed", "number", number, "err", err)
					return
				}
			case <-ticker.C:
				if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
					return
				}
				if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// Read pump (blocks until disconnect)
	defer h.hub.Unregister(number, conn)
	defer h.relay.OnDisconnect(number)
	defer func() {
		if msg.HardwareID != "" && h.deviceStore != nil {
			if err := h.deviceStore.TouchLastSeen(msg.HardwareID); err != nil {
				slog.Warn("touch last seen on disconnect failed", "hardware_id", msg.HardwareID, "err", err)
			}
		}
	}()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Error("websocket read failed", "number", number, "err", err)
			}
			break
		}
		msg, err := signaling.ParseMessage(data)
		if err != nil {
			slog.Warn("bad websocket message", "number", number, "err", err)
			continue
		}
		h.relay.HandleMessage(number, msg)
	}
}

// ---- Helpers ----

func (h *Handler) handleSettingsCallHistory(w http.ResponseWriter, r *http.Request) {
	if h.householdStore == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	enabled := r.FormValue("enabled") == "true"
	if err := h.householdStore.SetCallHistoryEnabled(households[0].ID, enabled); err != nil {
		slog.Error("set call history failed", "err", err)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsTimezone(w http.ResponseWriter, r *http.Request) {
	if h.householdStore == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		http.Redirect(w, r, "/onboard", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tz := strings.TrimSpace(r.FormValue("timezone"))
	if tz != "" {
		if err := h.householdStore.SetTimezone(households[0].ID, tz); err != nil {
			slog.Warn("set timezone failed", "err", err)
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsTheme(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	theme := auth.Theme(r.FormValue("theme"))
	if err := h.authStore.SetTheme(user.ID, theme); err != nil {
		slog.Error("set theme failed", "err", err, "theme", theme)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (h *Handler) handleSettingsCRTMode(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mode := auth.CRTMode(r.FormValue("crt_mode"))
	if err := h.authStore.SetCRTMode(user.ID, mode); err != nil {
		slog.Error("set crt_mode failed", "err", err, "crt_mode", mode)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

type connectingData struct {
	Page          string
	Version       string
	HouseholdName string
	User          *auth.User
}

func (h *Handler) handleConnecting(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Theme != auth.ThemeDialup {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	renderWith(w, h.tmplConnecting, "connecting.html", connectingData{
		Page:          "connecting",
		Version:       version.Version,
		HouseholdName: h.householdNameFromContext(r),
		User:          user,
	})
}

// requireLineOwnership looks up a line by number and verifies the authenticated
// user's household owns it. Returns the line on success, or nil after writing
// an HTTP error response (404 to avoid leaking line existence).
func (h *Handler) requireLineOwnership(w http.ResponseWriter, r *http.Request, number string) *line.Line {
	ln, err := h.lineStore.GetByNumber(number)
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return nil
	}
	if h.householdStore == nil {
		http.NotFound(w, r)
		return nil
	}
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		http.NotFound(w, r)
		return nil
	}
	for _, hh := range households {
		if hh.ID == ln.HouseholdID {
			return ln
		}
	}
	http.NotFound(w, r)
	return nil
}

// requireNumberOwnership verifies the authenticated user's household owns the
// given phone number without needing the full line record. Returns true if
// ownership is confirmed, or false after writing an HTTP 404 response.
func (h *Handler) requireNumberOwnership(w http.ResponseWriter, r *http.Request, number string) bool {
	return h.requireLineOwnership(w, r, number) != nil
}

// ownedLinesForUser returns the lines owned by any household the user belongs
// to, keyed by number, plus the primary household ID. Returns (nil, "", false)
// if the user has no households or any lookup fails — caller writes 404, same
// response shape as nonexistent.
func (h *Handler) ownedLinesForUser(user *auth.User) (map[string]*line.Line, string, bool) {
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil {
		slog.Error("link_health: list households failed", "user_id", user.ID, "err", err)
		return nil, "", false
	}
	if len(households) == 0 {
		return nil, "", false
	}
	lines := make(map[string]*line.Line)
	for _, hh := range households {
		hhLines, err := h.lineStore.ListByHousehold(hh.ID)
		if err != nil {
			slog.Error("link_health: list lines failed", "household_id", hh.ID, "err", err)
			return nil, "", false
		}
		for i := range hhLines {
			ln := hhLines[i]
			lines[ln.Number] = &ln
		}
	}
	return lines, households[0].ID, true
}

// requireCallEndpointOwnership verifies the authenticated user owns either
// endpoint of the call (across ANY household the user belongs to). Returns
// the call, the owned-lines map (keyed by number), the primary household ID,
// and true on success. On any failure, writes 404 (unauthorized and
// nonexistent are indistinguishable).
//
// Implementation detail: always performs the same sequence of DB queries
// regardless of auth outcome, to avoid a timing side channel on call-id
// enumeration. Linked households do NOT grant access to telemetry; only
// direct household ownership of a call endpoint does.
func (h *Handler) requireCallEndpointOwnership(w http.ResponseWriter, r *http.Request, callID int64) (calls.Call, map[string]*line.Line, string, bool) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return calls.Call{}, nil, "", false
	}

	// Always do both queries in the same order, regardless of miss reason.
	ownedLines, primaryHH, ok := h.ownedLinesForUser(user)
	call, callErr := h.tracker.GetCall(callID)

	if callErr != nil {
		slog.Error("link_health: get call failed", "call_id", callID, "err", callErr)
		http.NotFound(w, r)
		return calls.Call{}, nil, "", false
	}
	if !ok || call.ID == 0 {
		http.NotFound(w, r)
		return calls.Call{}, nil, "", false
	}

	_, ownsCaller := ownedLines[call.Caller]
	_, ownsCallee := ownedLines[call.Callee]
	if !ownsCaller && !ownsCallee {
		http.NotFound(w, r)
		return calls.Call{}, nil, "", false
	}
	return call, ownedLines, primaryHH, true
}

// ---- Link Health API ----

// LinkHealthSample is the JSON representation of a single link-health
// measurement returned by GET /api/call/{id}/link-health.
type LinkHealthSample struct {
	TS       int64    `json:"ts"`
	LossPct  *float32 `json:"loss_pct,omitempty"`
	JitterMs *float32 `json:"jitter_ms,omitempty"`
	RttMs    *float32 `json:"rtt_ms,omitempty"`
	ConnType string   `json:"conn_type,omitempty"`
	BytesIn  *int64   `json:"bytes_in,omitempty"`
	BytesOut *int64   `json:"bytes_out,omitempty"`
}

// LinkHealthEndpointResp is the per-endpoint section of a LinkHealthResp.
type LinkHealthEndpointResp struct {
	Number      string            `json:"number"`
	DisplayName string            `json:"display_name"`
	Latest      *LinkHealthSample `json:"latest,omitempty"`
	Window      []LinkHealthSample `json:"window"`
}

// LinkHealthResp is the top-level response body for GET /api/call/{id}/link-health.
type LinkHealthResp struct {
	CallID    int64                  `json:"call_id"`
	StartedAt time.Time              `json:"started_at"`
	Caller    LinkHealthEndpointResp `json:"caller"`
	Callee    LinkHealthEndpointResp `json:"callee"`
}

func toAPISample(s calls.Sample) LinkHealthSample {
	return LinkHealthSample{
		TS:       s.TS.UnixMilli(),
		LossPct:  s.LossPct,
		JitterMs: s.JitterMs,
		RttMs:    s.RttMs,
		ConnType: s.ConnType,
		BytesIn:  s.BytesIn,
		BytesOut: s.BytesOut,
	}
}

func (h *Handler) handleCallLinkHealth(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	callID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || callID <= 0 {
		http.NotFound(w, r)
		return
	}
	call, ownedLines, primaryHH, ok := h.requireCallEndpointOwnership(w, r, callID)
	if !ok {
		return
	}

	// Display-name resolution — same helpers as /calls page. No new data exposure.
	// Linked-household names are shown for peers that the user already sees in
	// their call log; the underlying auth check does not grant read access to
	// calls the user was not part of.
	var linkedIndex map[string]string
	if primaryHH != "" {
		linkedFamilies := h.buildLinkedFamilies(primaryHH)
		linkedIndex = buildLinkedLineIndex(linkedFamilies)
	}

	resp := LinkHealthResp{CallID: call.ID, StartedAt: call.StartedAt}
	callerEndpoint, err := h.buildLinkHealthEndpoint(r.Context(), call.ID, call.Caller, linkedIndex, ownedLines)
	if err != nil {
		slog.Error("link_health: build caller endpoint failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	calleeEndpoint, err := h.buildLinkHealthEndpoint(r.Context(), call.ID, call.Callee, linkedIndex, ownedLines)
	if err != nil {
		slog.Error("link_health: build callee endpoint failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp.Caller = callerEndpoint
	resp.Callee = calleeEndpoint

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("link_health encode failed", "call_id", callID, "err", err)
	}
}

func (h *Handler) buildLinkHealthEndpoint(ctx context.Context, callID int64, number string, linkedIndex map[string]string, ownedLines map[string]*line.Line) (LinkHealthEndpointResp, error) {
	out := LinkHealthEndpointResp{Number: number, Window: []LinkHealthSample{}}

	// Display name resolution: owned line first (no extra DB query), then
	// linked-index for peer names, then bare number as fallback.
	if ln, ok := ownedLines[number]; ok && ln != nil {
		out.DisplayName = ln.Name
	} else {
		out.DisplayName = resolvePeerName(number, linkedIndex)
	}
	if out.DisplayName == "" {
		out.DisplayName = number
	}

	// Memory first.
	windowMem := h.healthStore.Window(callID, number)
	if len(windowMem) > 0 {
		out.Window = make([]LinkHealthSample, len(windowMem))
		for i, s := range windowMem {
			out.Window[i] = toAPISample(s)
		}
		la := toAPISample(windowMem[len(windowMem)-1])
		out.Latest = &la
		return out, nil
	}

	// DB fallback.
	dbSamples, err := h.healthStore.Readback(ctx, callID, number, 60)
	if err != nil {
		return out, fmt.Errorf("readback %d/%s: %w", callID, number, err)
	}
	out.Window = make([]LinkHealthSample, len(dbSamples))
	for i, s := range dbSamples {
		out.Window[i] = toAPISample(s)
	}
	if len(dbSamples) > 0 {
		la := toAPISample(dbSamples[len(dbSamples)-1])
		out.Latest = &la
	}
	return out, nil
}

// sseHeartbeatInterval is how often we emit a synthetic heartbeat event
// on the link-health stream. Clients use this for liveness detection:
// absence of any event for >2x this interval triggers a "connection lost"
// banner.
const sseHeartbeatInterval = 15 * time.Second

// handleCallLinkHealthStream opens an SSE stream for a call's telemetry.
// Delivers:
//   - one initial "sample" event per endpoint with the current snapshot
//   - one "sample" event per future Record
//   - one "disconnect" event if a user force-disconnects
//   - one "ended" event when the call ends (any cause), then closes
//   - periodic "heartbeat" events for client-side liveness
//
// Auth: same as the JSON endpoint (direct-endpoint-ownership). Ended calls
// return 404 before any stream bytes are written.
func (h *Handler) handleCallLinkHealthStream(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	callID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || callID <= 0 {
		http.NotFound(w, r)
		return
	}
	call, ownedLines, _, ok := h.requireCallEndpointOwnership(w, r, callID)
	if !ok {
		return
	}
	if call.Status == "ended" {
		http.NotFound(w, r)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("SSE stream: ResponseWriter does not implement Flusher")
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Compute the linked-families index once at subscribe time. It can't
	// change mid-call (household membership changes don't retroactively
	// apply to a live call), and buildLinkedFamilies + buildLinkedLineIndex
	// together issue DB queries we don't want on the per-sample hot path.
	linkedIndex := h.linkedIndexForCall(r.Context(), ownedLines)

	if err := h.writeInitialSnapshot(r.Context(), w, flusher, call, ownedLines, linkedIndex); err != nil {
		slog.Debug("SSE stream: initial snapshot write failed", "call_id", callID, "err", err)
		return
	}

	sub := h.healthStore.Subscribe(callID)
	defer sub.Close()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-sub.C:
			if !ok {
				// Channel closed by Evict. Send one final ended event and return.
				_ = writeSSE(w, "ended", renderEndedFragment(""))
				flusher.Flush()
				return
			}
			if err := h.writeEvent(w, flusher, call, ownedLines, linkedIndex, ev); err != nil {
				slog.Debug("SSE stream: write failed; client gone", "call_id", callID, "err", err)
				return
			}
		case <-heartbeat.C:
			if err := writeSSE(w, "heartbeat", "{}"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSE emits one SSE event frame: "event: <name>\ndata: <data>\n\n".
func writeSSE(w io.Writer, event, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	// Each line of data must be prefixed per the SSE spec.
	for _, line := range strings.Split(data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\n")
	return err
}

func (h *Handler) writeInitialSnapshot(ctx context.Context, w io.Writer, flusher http.Flusher, call calls.Call, ownedLines map[string]*line.Line, linkedIndex map[string]string) error {
	callerEp, err := h.buildLinkHealthEndpoint(ctx, call.ID, call.Caller, linkedIndex, ownedLines)
	if err != nil {
		return err
	}
	calleeEp, err := h.buildLinkHealthEndpoint(ctx, call.ID, call.Callee, linkedIndex, ownedLines)
	if err != nil {
		return err
	}
	fragment, err := h.renderLinkHealthPanel(call, callerEp, calleeEp)
	if err != nil {
		return err
	}
	if err := writeSSE(w, "sample", fragment); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (h *Handler) writeEvent(w io.Writer, flusher http.Flusher, call calls.Call, ownedLines map[string]*line.Line, linkedIndex map[string]string, ev calls.Event) error {
	switch ev.Kind {
	case calls.SampleKind:
		callerEp, err := h.buildLinkHealthEndpoint(context.Background(), call.ID, call.Caller, linkedIndex, ownedLines)
		if err != nil {
			return err
		}
		calleeEp, err := h.buildLinkHealthEndpoint(context.Background(), call.ID, call.Callee, linkedIndex, ownedLines)
		if err != nil {
			return err
		}
		fragment, err := h.renderLinkHealthPanel(call, callerEp, calleeEp)
		if err != nil {
			return err
		}
		if err := writeSSE(w, "sample", fragment); err != nil {
			return err
		}
	case calls.DisconnectKind:
		if err := writeSSE(w, "disconnect", renderEndedFragment(ev.EndedBy)); err != nil {
			return err
		}
	case calls.EndedKind:
		if err := writeSSE(w, "ended", renderEndedFragment("")); err != nil {
			return err
		}
	}
	flusher.Flush()
	return nil
}

// linkedIndexForCall builds the linked-families index for display-name
// resolution. Returns nil when the user belongs to no households.
func (h *Handler) linkedIndexForCall(ctx context.Context, ownedLines map[string]*line.Line) map[string]string {
	for _, ln := range ownedLines {
		if ln != nil {
			return buildLinkedLineIndex(h.buildLinkedFamilies(ln.HouseholdID))
		}
	}
	return nil
}

// renderLinkHealthPanel executes the _call-live-panel.html template against
// a LinkHealthResp and returns the rendered HTML.
func (h *Handler) renderLinkHealthPanel(call calls.Call, caller, callee LinkHealthEndpointResp) (string, error) {
	var buf bytes.Buffer
	data := LinkHealthResp{CallID: call.ID, StartedAt: call.StartedAt, Caller: caller, Callee: callee}
	if err := h.tmplCallLivePanel.ExecuteTemplate(&buf, "call-live-panel", data); err != nil {
		return "", fmt.Errorf("render call-live-panel: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// renderEndedFragment returns the small HTML shown when a call ends.
func renderEndedFragment(endedBy string) string {
	if endedBy != "" {
		return fmt.Sprintf(`<div class="deck-ended">Ended by %s.</div>`, html.EscapeString(endedBy))
	}
	return `<div class="deck-ended">Call ended.</div>`
}

// handleCallDisconnect force-ends an active call. Any user whose household
// owns either endpoint (direct ownership only; linked households do NOT
// qualify) can trigger this. The server records the actor in calls.force_ended_by,
// notifies any open SSE subscribers, sends hangup to both peers via the
// relay, and calls Tracker.OnCallEnded for deterministic DB close.
//
// Idempotent: calling against an already-ended call returns 200 without
// overwriting the audit column.
func (h *Handler) handleCallDisconnect(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	callID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || callID <= 0 {
		http.NotFound(w, r)
		return
	}
	call, _, _, ok := h.requireCallEndpointOwnership(w, r, callID)
	if !ok {
		return
	}

	// Idempotency: if the call already ended, just return 200 without
	// touching the audit column.
	if call.Status == "ended" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		return
	}

	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return
	}

	// Record who force-ended the call BEFORE the teardown fires, so the
	// audit row is in place even if we crash mid-teardown.
	if err := h.tracker.MarkForceEnded(callID, user.ID); err != nil {
		slog.Error("force-disconnect audit write failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Notify SSE subscribers before teardown so open pages flip to terminal
	// state immediately rather than flickering through a dropped stream.
	label := userDisplayLabel(user)
	h.healthStore.NotifyDisconnected(callID, label)

	// Send hangup to both peers. Errors per-peer are logged in ForceHangup.
	h.relay.ForceHangup(call.Caller, call.Callee)

	// Close the DB row deterministically. OnCallEnded is idempotent; a
	// peer-initiated hangup arriving later is a safe no-op.
	if err := h.tracker.OnCallEnded(call.Caller, call.Callee); err != nil {
		slog.Error("force-disconnect OnCallEnded failed", "call_id", callID, "err", err)
		// Phones are hung up regardless; the status transition will happen
		// when the next peer hangup arrives or during the daily cleanup.
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

// userDisplayLabel returns the preferred name for an audit/display context:
// User.Name if set, else the email local-part, else the bare email.
func userDisplayLabel(u *auth.User) string {
	if u == nil {
		return ""
	}
	if u.Name != "" {
		return u.Name
	}
	if at := strings.IndexByte(u.Email, '@'); at > 0 {
		return u.Email[:at]
	}
	return u.Email
}

// householdNumbers returns the set of phone numbers belonging to the
// authenticated user's household. Returns nil if the user has no household.
func (h *Handler) householdNumbers(r *http.Request) map[string]bool {
	user := auth.UserFromContext(r.Context())
	if user == nil || h.householdStore == nil {
		return nil
	}
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		return nil
	}
	lines, err := h.lineStore.ListByHousehold(households[0].ID)
	if err != nil {
		return nil
	}
	nums := make(map[string]bool, len(lines))
	for _, l := range lines {
		nums[l.Number] = true
	}
	return nums
}

// householdContext returns the household name, call-history flag, and timezone location for the current user.
func (h *Handler) householdContext(r *http.Request) (name string, callHistory bool, loc *time.Location) {
	if h.householdStore == nil {
		return "", false, time.UTC
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		return "", false, time.UTC
	}
	households, err := h.householdStore.GetForUser(user.ID)
	if err != nil || len(households) == 0 {
		return "", false, time.UTC
	}
	return households[0].Name, households[0].CallHistoryEnabled, households[0].Location()
}

func (h *Handler) callHistoryEnabled(r *http.Request) bool {
	_, ch, _ := h.householdContext(r)
	return ch
}

func (h *Handler) householdNameFromContext(r *http.Request) string {
	name, _, _ := h.householdContext(r)
	return name
}

func (h *Handler) handleAPIVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
	}); err != nil {
		slog.Error("api version: json encode failed", "err", err)
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

func renderWith(w http.ResponseWriter, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template render failed", "template", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// layoutFor returns the layout template name for the current user's theme.
// Falls back to direction C when no theme is set (unauthenticated or new user).
func layoutFor(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil && u.Theme == auth.ThemeDialup {
		return "layout-dialup.html"
	}
	return "layout-v2.html"
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// ---- Call live detail ----

type callLiveDetailData struct {
	Page               string
	Version            string
	User               *auth.User
	HouseholdName      string
	CallHistoryEnabled bool
	Call               calls.Call
	Caller             LinkHealthEndpointResp
	Callee             LinkHealthEndpointResp
	Ended              bool
	ForceEndedBy       string
}

// handleCallLiveDetail renders the observation-deck page for a specific call.
// Auth uses the same ownership check as the JSON endpoint. Ended calls are
// NOT 404'd here — they render in terminal state with the last-known samples
// from DB fallback, making the URL useful as a postmortem surface.
func (h *Handler) handleCallLiveDetail(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	callID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || callID <= 0 {
		http.NotFound(w, r)
		return
	}
	call, ownedLines, _, ok := h.requireCallEndpointOwnership(w, r, callID)
	if !ok {
		return
	}

	user := auth.UserFromContext(r.Context())
	linkedIndex := h.linkedIndexForCall(r.Context(), ownedLines)
	callerEp, err := h.buildLinkHealthEndpoint(r.Context(), call.ID, call.Caller, linkedIndex, ownedLines)
	if err != nil {
		slog.Error("call-live: build caller endpoint failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	calleeEp, err := h.buildLinkHealthEndpoint(r.Context(), call.ID, call.Callee, linkedIndex, ownedLines)
	if err != nil {
		slog.Error("call-live: build callee endpoint failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := callLiveDetailData{
		Page:               "call-live",
		Version:            version.Version,
		User:               user,
		HouseholdName:      h.householdNameFromContext(r),
		CallHistoryEnabled: h.callHistoryEnabled(r),
		Call:               call,
		Caller:             callerEp,
		Callee:             calleeEp,
		Ended:              call.Status == "ended",
		ForceEndedBy:       h.forceEndedLabel(call),
	}

	renderWith(w, h.tmplCallLiveDetail, layoutFor(r), data)
}

// forceEndedLabel returns the display label for who force-ended a call.
// Returns "" if peer-initiated or user lookup fails.
func (h *Handler) forceEndedLabel(call calls.Call) string {
	if call.ForceEndedBy == nil {
		return ""
	}
	u, err := h.authStore.GetUserByID(*call.ForceEndedBy)
	if err != nil || u == nil {
		return ""
	}
	return userDisplayLabel(u)
}

func mustMarshal(msg *signaling.Message) []byte {
	data, _ := msg.Marshal()
	return data
}

// handleTestStartCall is a DEV_MODE test-harness endpoint used by the
// Playwright suite to seed an active call without driving the full
// signaling flow. Never registered in production builds.
func (h *Handler) handleTestStartCall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Caller string `json:"caller"`
		Callee string `json:"callee"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if body.Caller == "" || body.Callee == "" {
		http.Error(w, "caller and callee required", http.StatusBadRequest)
		return
	}
	id, err := h.tracker.OnCallInitiated(body.Caller, body.Callee)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
}

// handleDevSeedFirmware registers a fake hub entry for a line number with the
// given firmware version. It is only reachable when DevMode is true and lets
// the Playwright e2e suite exercise the firmware update chip without a real
// device connection.
//
// POST /dev/seed-firmware?number=<line-number>&fw=<semver>
func (h *Handler) handleDevSeedFirmware(w http.ResponseWriter, r *http.Request) {
	number := r.URL.Query().Get("number")
	fw := r.URL.Query().Get("fw")
	if number == "" || fw == "" {
		http.Error(w, "number and fw query params are required", http.StatusBadRequest)
		return
	}
	conn := &signaling.Conn{Send: make(chan []byte, 8)}
	// Drain Send so any hub fan-out to this fake device is silently discarded
	// instead of blocking at the channel cap during interactive dev testing.
	go func() {
		for range conn.Send {
		}
	}()
	h.hub.Register(number, conn)
	h.hub.UpdateDeviceInfo(number, "", "", fw, "", false)
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"ok":true,"number":%q,"fw":%q}`, number, fw)
}

