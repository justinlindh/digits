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
	"github.com/justinlindh/digits/server/internal/dashboard/events"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/httputil"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/metrics"
	"github.com/justinlindh/digits/server/internal/pairing"
	"github.com/justinlindh/digits/server/internal/ratelimit"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/tracing"
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

// TemplateFuncs returns the html/template FuncMap that page templates expect.
// Exposed so test helpers that parse templates directly (without going through
// NewHandler) get the same {{static}}, {{fmtPhone}}, etc. helpers as prod.
func TemplateFuncs() template.FuncMap {
	return baseTemplateFuncs()
}

func baseTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"fmtPhone": line.FormatNumber,
		"fmtDuration": func(seconds int) string {
			if seconds < 60 {
				return fmt.Sprintf("%ds", seconds)
			}
			return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
		},
		"fmtDurationClock": func(seconds int) string {
			h := seconds / 3600
			m := (seconds % 3600) / 60
			s := seconds % 60
			if h > 0 {
				return fmt.Sprintf("%d:%02d:%02d", h, m, s)
			}
			return fmt.Sprintf("%02d:%02d", m, s)
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
		"inc": func(n int) int { return n + 1 },
		"mod": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a % b
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
		// staticURL returns a cache-bust-suffixed /static/ URL. Cloudflare and
		// other CDNs key on the full URL including query, so each release's
		// commit-suffixed URL bypasses the prior release's cached entry. In
		// dev (commit unset) we fall back to the bare path so the disk-served
		// edits still revalidate per request.
		"static": func(path string) string {
			if version.Commit == "" || version.Commit == "unknown" {
				return "/static/" + path
			}
			return "/static/" + path + "?v=" + version.Commit
		},
		"edgeFor": func(edges []ConferenceLinkHealthEdge, from, peer string) *ConferenceLinkHealthEdge {
			for i := range edges {
				if edges[i].From == from && edges[i].Peer == peer {
					return &edges[i]
				}
			}
			return nil
		},
	}
}

const (
	cacheControlImmutable = "public, max-age=31536000, immutable"
	hstsHeader            = "max-age=31536000; includeSubDomains"
)

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
//
// Requests carrying a query string (e.g. /static/foo.css?v=<commit>) are
// served with a 1-year immutable cache header; templates use the {{static}}
// helper to produce those versioned URLs so a new release writes a new URL
// that bypasses any CDN cache of the prior one.
func staticFileServer(devMode bool, diskDir string) http.Handler {
	var base http.Handler
	if devMode {
		if diskDir == "" {
			diskDir = devStaticDirDefault
		}
		base = http.StripPrefix("/static/", http.FileServer(http.Dir(diskDir)))
	} else {
		// fs.Sub strips the "static" prefix from the embedded FS so request
		// paths align with the disk-mode handler's StripPrefix treatment.
		sub, err := fs.Sub(staticFS, "static")
		if err != nil {
			// embed declares the "static" directory above; sub-rooting to it
			// cannot fail at runtime. A panic here would indicate a programmer
			// error in the embed declaration.
			panic(fmt.Errorf("fs.Sub(staticFS, \"static\"): %w", err))
		}
		base = http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			w.Header().Set("Cache-Control", cacheControlImmutable)
		}
		base.ServeHTTP(w, r)
	})
}

// Handler is the HTTP handler for the signald web UI and API. Construct one
// with NewHandler; the zero value is not valid.
type Handler struct {
	upgrader    websocket.Upgrader
	lineStore   *line.Store
	deviceStore *device.Store
	hub         *signaling.Hub
	tracker     *calls.Tracker
	relay       *signaling.Relay
	healthStore *calls.HealthStore
	dashEvents  *events.Broadcaster
	// Per-page template sets to avoid {{define}} name conflicts
	tmplDashboard            *template.Template
	tmplPhones               *template.Template
	tmplCalls                *template.Template
	tmplSettings             *template.Template
	tmplOnboard              *template.Template
	tmplPhoneDetail          *template.Template
	tmplLinks                *template.Template
	tmplConnecting           *template.Template
	tmplWelcome              *template.Template
	tmplInvite               *template.Template
	tmplCallLivePanel        *template.Template
	tmplCallLiveDetail       *template.Template
	tmplConferenceLivePanel  *template.Template
	tmplConferenceLiveDetail *template.Template
	tmplDashboardAMStatus    *template.Template
	tmplChangelog            *template.Template
	cfg                      HandlerConfig
	// Auth
	authStore    *auth.Store
	authHandlers *auth.Handlers
	googleAuth   *auth.GoogleAuth
	// Household
	householdStore *household.Store
	// Pairing
	pairingStore *pairing.Store
	// Household links
	linkStore   *household.LinkStore
	inviteStore *household.InviteStore
	emailer     email.Sender
	// Rate limiters. All four are Handler fields so Router() has a single
	// construction pattern; previously the magic-link verify and Google
	// login limiters were instantiated inline inside Router(), which made
	// them harder to spot and impossible to share across request types.
	authLimiter        *ratelimit.Limiter // POST /auth/magic (magic link request)
	magicVerifyLimiter *ratelimit.Limiter // GET  /auth/magic/{token}
	googleLoginLimiter *ratelimit.Limiter // GET  /auth/google/login
	pairingLimiter     *ratelimit.Limiter // POST /phones/pair
	inviteLimiter      *ratelimit.Limiter // POST /settings/household/invite
	wsLimiter          *ratelimit.Limiter // GET  /ws (WebSocket upgrade)
	// Updates
	Releases *updates.GitHubReleases
	// Metrics is the optional Prometheus registry. When set, a request
	// timing/count middleware is wrapped around the public mux. nil disables
	// HTTP instrumentation entirely (useful for tests that don't care).
	metrics *metrics.Registry
}

// segDesc drives bar segment rendering. Lit is the count (0..10) of
// segments that should be rendered as lit; Severity ("" | "warn" | "bad")
// controls their color via CSS classes.
type segDesc struct {
	Lit      int
	Severity string
}

// HandlerConfig carries Handler behavior knobs that are not collaborator
// dependencies (those live in Deps).
type HandlerConfig struct {
	Addr string
	// BaseURL is the public origin for the app (e.g. https://app.digits.family).
	// Used for WebSocket origin checks and outgoing magic-link URLs.
	BaseURL string
	// AdminSecret gates /internal/stats. Empty disables the endpoint.
	AdminSecret string
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
	// WSRateLimitPerMin overrides the default WebSocket upgrade rate limit
	// (per IP, per minute). Zero uses the default (30).
	WSRateLimitPerMin int
}

// Deps bundles the stores, hub, and other collaborators the web Handler
// depends on. Named fields prevent a silent swap between same-typed
// parameters (the previous 16-arg positional NewHandler made that easy).
type Deps struct {
	LineStore      *line.Store
	DeviceStore    *device.Store
	Hub            *signaling.Hub
	Tracker        *calls.Tracker
	Relay          *signaling.Relay
	HealthStore    *calls.HealthStore
	DashEvents     *events.Broadcaster
	AuthStore      *auth.Store
	AuthHandlers   *auth.Handlers
	GoogleAuth     *auth.GoogleAuth
	HouseholdStore *household.Store
	PairingStore   *pairing.Store
	LinkStore      *household.LinkStore
	InviteStore    *household.InviteStore
	Emailer        email.Sender
	Metrics        *metrics.Registry
}

func wsRateLimit(cfg HandlerConfig) int {
	if cfg.WSRateLimitPerMin > 0 {
		return cfg.WSRateLimitPerMin
	}
	return 30
}

// NewHandler constructs a Handler, parses all embedded HTML templates, and
// wires up rate limiters. Returns an error if any template fails to parse.
func NewHandler(deps Deps, cfg HandlerConfig) (*Handler, error) {
	funcMap := baseTemplateFuncs()
	// parsePage closes over the layout + shared-partials file list so each
	// page only names itself. Adding a new layout or partial touches one line.
	parsePage := func(page string) (*template.Template, error) {
		return template.New("").Funcs(funcMap).ParseFS(templateFS,
			"templates/_partials.html",
			"templates/_changelog.html",
			"templates/layout-v2.html",
			"templates/layout-dialup.html",
			"templates/layout-answering-machine.html",
			"templates/"+page,
		)
	}

	tmplDashboard, err := parsePage("dashboard.html")
	if err != nil {
		return nil, err
	}
	// Merge the AM status partial so {{template "dashboard-am-status"}} resolves
	// inside the dashboard page.
	if _, err := tmplDashboard.ParseFS(templateFS, "templates/_dashboard-am-status.html"); err != nil {
		return nil, fmt.Errorf("parse dashboard-am-status partial into dashboard: %w", err)
	}
	tmplDashboardAMStatus, err := template.New("dashboard-am-status").Funcs(funcMap).ParseFS(templateFS, "templates/_dashboard-am-status.html")
	if err != nil {
		return nil, fmt.Errorf("parse dashboard-am-status: %w", err)
	}
	tmplPhones, err := parsePage("phones.html")
	if err != nil {
		return nil, err
	}
	if _, err := tmplPhones.ParseFS(templateFS, "templates/dnd-toggle.html"); err != nil {
		return nil, fmt.Errorf("parse dnd-toggle partial into phones: %w", err)
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
	tmplWelcome, err := parsePage("welcome.html")
	if err != nil {
		return nil, err
	}
	tmplInvite, err := parsePage("invite.html")
	if err != nil {
		return nil, err
	}
	tmplCallLivePanel, err := template.New("call-live-panel").Funcs(funcMap).ParseFS(templateFS, "templates/_call-live-panel.html")
	if err != nil {
		return nil, fmt.Errorf("parse call-live-panel: %w", err)
	}
	tmplConferenceLivePanel, err := template.New("conference-live-panel").Funcs(funcMap).ParseFS(templateFS, "templates/_conference-live-panel.html")
	if err != nil {
		return nil, fmt.Errorf("parse conference-live-panel: %w", err)
	}
	tmplCallLiveDetail, err := parsePage("call-live-detail.html")
	if err != nil {
		return nil, fmt.Errorf("parse call-live-detail: %w", err)
	}
	// Merge the panel partial so {{template "call-live-panel"}} resolves inside the detail page.
	if _, err := tmplCallLiveDetail.ParseFS(templateFS, "templates/_call-live-panel.html"); err != nil {
		return nil, fmt.Errorf("parse call-live-panel partial into detail: %w", err)
	}
	tmplConferenceLiveDetail, err := parsePage("conference-live-detail.html")
	if err != nil {
		return nil, fmt.Errorf("parse conference-live-detail: %w", err)
	}
	// Merge the panel partial so {{template "conference-live-panel"}} resolves inside the detail page.
	if _, err := tmplConferenceLiveDetail.ParseFS(templateFS, "templates/_conference-live-panel.html"); err != nil {
		return nil, fmt.Errorf("parse conference-live-panel partial into detail: %w", err)
	}
	tmplChangelog, err := template.New("changelog").Funcs(funcMap).ParseFS(templateFS,
		"templates/_partials.html",
		"templates/_changelog.html",
	)
	if err != nil {
		return nil, fmt.Errorf("parse changelog: %w", err)
	}

	u := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Non-browser clients (e.g. Pi daemon) send no Origin; allow them.
				return true
			}
			return origin == cfg.BaseURL
		},
	}

	return &Handler{
		upgrader:                 u,
		lineStore:                deps.LineStore,
		deviceStore:              deps.DeviceStore,
		hub:                      deps.Hub,
		tracker:                  deps.Tracker,
		relay:                    deps.Relay,
		healthStore:              deps.HealthStore,
		dashEvents:               deps.DashEvents,
		tmplDashboard:            tmplDashboard,
		tmplPhones:               tmplPhones,
		tmplCalls:                tmplCalls,
		tmplSettings:             tmplSettings,
		tmplOnboard:              tmplOnboard,
		tmplPhoneDetail:          tmplPhoneDetail,
		tmplLinks:                tmplLinks,
		tmplConnecting:           tmplConnecting,
		tmplWelcome:              tmplWelcome,
		tmplInvite:               tmplInvite,
		tmplCallLivePanel:        tmplCallLivePanel,
		tmplCallLiveDetail:       tmplCallLiveDetail,
		tmplConferenceLivePanel:  tmplConferenceLivePanel,
		tmplConferenceLiveDetail: tmplConferenceLiveDetail,
		tmplDashboardAMStatus:    tmplDashboardAMStatus,
		tmplChangelog:            tmplChangelog,
		cfg:                      cfg,
		authStore:                deps.AuthStore,
		authHandlers:             deps.AuthHandlers,
		googleAuth:               deps.GoogleAuth,
		householdStore:           deps.HouseholdStore,
		pairingStore:             deps.PairingStore,
		linkStore:                deps.LinkStore,
		inviteStore:              deps.InviteStore,
		emailer:                  deps.Emailer,
		authLimiter:              ratelimit.New(5, time.Minute),
		magicVerifyLimiter:       ratelimit.New(10, time.Minute),
		googleLoginLimiter:       ratelimit.New(10, time.Minute),
		pairingLimiter:           ratelimit.New(5, time.Minute),
		inviteLimiter:            ratelimit.New(5, time.Minute),
		wsLimiter:                ratelimit.New(wsRateLimit(cfg), time.Minute),
		metrics:                  deps.Metrics,
	}, nil
}

// Hub returns the signaling Hub. Used by callers (e.g. main) that wire
// external callbacks and need to broadcast messages to connected devices.
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
	mux.HandleFunc("GET /healthz", httputil.Healthz(version.Version))

	// Public routes — no auth required
	mux.HandleFunc("GET /auth/login", h.authHandlers.HandleLoginPage)
	mux.Handle("POST /auth/magic", h.authLimiter.Middleware(http.HandlerFunc(h.authHandlers.HandleMagicLinkRequest)))
	mux.Handle("GET /auth/magic/{token}", h.magicVerifyLimiter.Middleware(http.HandlerFunc(h.authHandlers.HandleMagicLinkVerify)))
	mux.HandleFunc("POST /auth/logout", h.authHandlers.HandleLogout)
	mux.HandleFunc("GET /auth/dev-session", h.authHandlers.HandleDevSession)
	mux.Handle("GET /auth/google/login", h.googleLoginLimiter.Middleware(http.HandlerFunc(h.googleAuth.HandleLogin)))
	mux.HandleFunc("GET /auth/google/callback", h.googleAuth.HandleCallback)
	mux.HandleFunc("GET /invite/{token}", h.handleInviteGet)
	mux.HandleFunc("POST /invite/{token}/accept", h.handleInviteAcceptPost)
	mux.HandleFunc("GET /api/version", h.handleAPIVersion)
	mux.HandleFunc("GET /internal/stats", h.handleInternalStats)
	mux.Handle("GET /ws", h.wsLimiter.Middleware(http.HandlerFunc(h.handleWS)))

	// Update release index endpoint (unauthenticated — phones fetch this)
	if h.Releases != nil {
		mux.HandleFunc("GET /api/updates/releases", h.Releases.ServeReleases())
		mux.HandleFunc("GET /api/release-audio/{component}/{version}", h.Releases.ServeAudio())
		slog.Info("updates: serving release index from GitHub")
	}
	// /test is a legacy alias for the dev test client. The file now lives in
	// the embedded static FS so it works from any CWD, and /static/* serves
	// it directly; this alias keeps any bookmarked URLs working.
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/test-client.html", http.StatusFound)
	})

	// Dev-only routes: gated by DevMode, not registered in production builds.
	if h.cfg.DevMode {
		// Harness endpoint for /call/live e2e tests to seed an active call
		// without going through the full signaling dance.
		mux.HandleFunc("POST /test-harness/start-call", h.handleTestStartCall)
		// Harness endpoint for /conference/live e2e tests to seed a conference
		// without going through the full signaling dance.
		mux.HandleFunc("POST /test-harness/start-conference", h.handleTestStartConference)
		// Seed a fake hub entry so e2e tests can exercise the firmware update
		// chip without a real device connection.
		mux.HandleFunc("POST /dev/seed-firmware", h.handleDevSeedFirmware)
	}

	// Protected routes — require valid session
	protected := http.NewServeMux()
	protected.HandleFunc("GET /", h.handleDashboard)
	protected.HandleFunc("GET /connecting", h.handleConnecting)
	protected.HandleFunc("GET /welcome", h.handleWelcomeGet)
	protected.HandleFunc("POST /welcome", h.handleWelcomePost)
	protected.HandleFunc("GET /onboard", h.handleOnboardGet)
	protected.HandleFunc("POST /onboard", h.handleOnboardPost)
	protected.HandleFunc("GET /phones", h.handlePhonesGet)
	protected.HandleFunc("POST /phones", h.handlePhonesPost)
	protected.Handle("POST /phones/pair", h.pairingLimiter.Middleware(http.HandlerFunc(h.handlePhonesPairPost)))
	protected.HandleFunc("GET /phones/{number}", h.handlePhoneDetail)
	protected.HandleFunc("GET /phones/{number}/edit", h.handlePhoneEditGet)
	protected.HandleFunc("POST /phones/{number}/edit", h.handlePhoneEditPost)
	protected.HandleFunc("GET /phones/{number}/name", h.handlePhoneNameGet)
	protected.HandleFunc("GET /phones/{number}/name/edit", h.handlePhoneNameEditGet)
	protected.HandleFunc("POST /phones/{number}/name", h.handlePhoneNamePost)
	protected.HandleFunc("POST /phones/{number}/number", h.handlePhoneNumberPost)
	protected.HandleFunc("POST /phones/{number}/voice-style", h.handlePhoneVoiceStylePost)
	protected.HandleFunc("POST /phones/{number}/silent-mode", h.handlePhoneSilentModePost)
	protected.HandleFunc("POST /phones/{number}/auto-update", h.handlePhoneAutoUpdatePost)
	protected.HandleFunc("POST /phones/{number}/voicemail", h.handlePhoneVoicemailPost)
	protected.HandleFunc("POST /phones/{number}/convert", h.handlePhoneConvert)
	protected.HandleFunc("POST /phones/{number}/delete", h.handlePhoneDelete)
	protected.HandleFunc("POST /phones/{number}/update", h.handlePhoneUpdate)
	protected.HandleFunc("GET /phones/{number}/online", h.handlePhoneOnline)
	protected.HandleFunc("GET /phones/{number}/update-status", h.handlePhoneUpdateStatus)
	protected.HandleFunc("POST /phones/{number}/factory-reset", h.handlePhoneFactoryReset)
	protected.HandleFunc("POST /phones/{number}/restart", h.handlePhoneRestart)
	protected.HandleFunc("POST /phones/{number}/ring-test", h.handlePhoneRingTest)
	protected.HandleFunc("GET /calls", h.handleCalls)
	protected.HandleFunc("GET /settings", h.handleSettings)
	protected.HandleFunc("POST /settings/household", h.handleSettingsHouseholdPost)
	protected.HandleFunc("POST /settings/call-history", h.handleSettingsCallHistory)
	protected.HandleFunc("POST /settings/do-not-disturb", h.handleSettingsDoNotDisturb)
	protected.HandleFunc("POST /settings/timezone", h.handleSettingsTimezone)
	protected.HandleFunc("POST /settings/theme", h.handleSettingsTheme)
	protected.HandleFunc("POST /settings/crt-mode", h.handleSettingsCRTMode)
	protected.HandleFunc("POST /settings/appearance", h.handleSettingsAppearance)
	protected.Handle("POST /settings/household/invite", h.inviteLimiter.Middleware(http.HandlerFunc(h.handleHouseholdInvitePost)))
	protected.HandleFunc("POST /settings/household/invite/{id}/cancel", h.handleHouseholdInviteCancelPost)
	protected.HandleFunc("POST /settings/household/members/{id}/remove", h.handleHouseholdMemberRemovePost)
	protected.HandleFunc("POST /settings/household/switch", h.handleHouseholdSwitchPost)
	protected.HandleFunc("POST /settings/account/delete", h.handleAccountDeletePost)
	protected.HandleFunc("GET /changelog", h.handleChangelog)
	protected.HandleFunc("GET /links", h.handleLinksGet)
	protected.HandleFunc("POST /links/invite", h.handleLinksInvitePost)
	protected.HandleFunc("POST /links/accept", h.handleLinksAcceptPost)
	protected.HandleFunc("POST /links/{id}/revoke", h.handleLinksRevokePost)
	protected.HandleFunc("GET /api/status", h.handleAPIStatus)
	protected.HandleFunc("GET /api/active-calls", h.handleAPIActiveCalls)
	protected.HandleFunc("GET /call/live/{id}", h.handleCallLiveDetail)
	protected.HandleFunc("GET /conference/live/{uuid}", h.handleConferenceLiveDetail)
	protected.HandleFunc("GET /api/dashboard/stream", h.handleDashboardStream)
	protected.HandleFunc("GET /api/call/{id}/link-health", h.handleCallLinkHealth)
	protected.HandleFunc("GET /api/call/{id}/link-health/stream", h.handleCallLinkHealthStream)
	protected.HandleFunc("GET /api/conference/{uuid}/link-health", h.handleConferenceLinkHealth)
	protected.HandleFunc("GET /api/conference/{uuid}/link-health/stream", h.handleConferenceLinkHealthStream)
	protected.HandleFunc("POST /api/conference/{uuid}/kick", h.handleConferenceKick)
	protected.HandleFunc("POST /api/call/{id}/disconnect", h.handleCallDisconnect)
	protected.HandleFunc("GET /api/lines/number-available", h.handleAPINumberAvailable)

	// Two gates compose in front of protected routes, both behind RequireAuth:
	//
	//   1. Welcome gate: until theme_chosen=true, send the user to /welcome
	//      so they pick a theme. Always active (the column has a backfill so
	//      pre-existing users skip it).
	//   2. Onboarding gate: until the user has a household, send them to
	//      /onboard. Only active when householdStore is wired.
	//
	// Order matters: welcome runs first so the household onboarding screens
	// render in the user's chosen theme, not the intercom default.
	protectedHandler := h.authStore.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Welcome gate. Exempts /welcome itself plus the universal set
		// (/auth/*, /api/*, /ws) so JSON/SSE/WS clients get their natural
		// 4xx instead of an HTML redirect.
		if !isGateExempt(r.URL.Path, "/welcome") {
			user := auth.UserFromContext(r.Context())
			if user != nil && !user.ThemeChosen {
				http.Redirect(w, r, "/welcome", http.StatusSeeOther)
				return
			}
		}
		// Onboarding gate. Adds /onboard (its redirect target) and
		// /welcome (the picker runs before a household exists) to the
		// universal exempt set.
		if h.householdStore != nil && !isGateExempt(r.URL.Path, "/welcome", "/onboard") {
			user := auth.UserFromContext(r.Context())
			if user != nil && h.householdStore.NeedsOnboarding(r.Context(), user.ID) {
				http.Redirect(w, r, "/onboard", http.StatusSeeOther)
				return
			}
		}
		protected.ServeHTTP(w, r)
	}))
	mux.Handle("/", protectedHandler)

	// Wrap with root-domain redirect before security headers.
	wrapped := rootDomainRedirect(h.cfg.BaseURL, csrfOriginCheck(h.cfg.BaseURL, securityHeadersMiddleware(h.cfg.BaseURL, mux)))
	// Metrics middleware sits outside redirect/security headers so it
	// sees the actual response code and duration including any redirect
	// header work above. RouteOf bucket is computed from the request
	// path, never from a route name read off the matched handler, so the
	// labels can't pick up an internal name.
	if h.metrics != nil {
		wrapped = h.metrics.Middleware(wrapped)
	}
	// Tracing middleware sits outermost so the server span covers the
	// full request lifetime and so an inbound traceparent header is
	// honored before any other middleware runs. The middleware uses the
	// same metrics.RouteOf bucketer for span names, so a phone number in
	// the URL never reaches a span attribute or span name.
	wrapped = tracing.HTTPServerMiddleware("signald", wrapped)
	return wrapped
}

// isGateExempt reports whether a request path should bypass the welcome and
// onboarding redirect gates. /auth/*, /api/*, and /ws[/*] are always exempt
// because their consumers can't (or shouldn't) follow an HTML page redirect:
// SSE/fetch clients would parse the HTML as JSON, WS upgrades would fail,
// and the auth flow itself must reach /auth/login regardless of state. Each
// gate also passes its own redirect target (and any other gate's target it
// needs to defer to) as `extra` so a redirect-to-self can't loop.
func isGateExempt(path string, extra ...string) bool {
	for _, p := range extra {
		if path == p {
			return true
		}
	}
	if strings.HasPrefix(path, "/auth/") {
		return true
	}
	if strings.HasPrefix(path, "/api/") {
		return true
	}
	if path == "/ws" || strings.HasPrefix(path, "/ws/") {
		return true
	}
	return false
}

func securityHeadersMiddleware(baseURL string, next http.Handler) http.Handler {
	connectSrc := "'self' wss:"
	if baseURL != "" {
		wssOrigin := strings.Replace(baseURL, "https://", "wss://", 1)
		wssOrigin = strings.Replace(wssOrigin, "http://", "ws://", 1)
		connectSrc = "'self' " + wssOrigin
	}
	csp := fmt.Sprintf("default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src %s; frame-ancestors 'none'", connectSrc)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", hstsHeader)
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// csrfOriginCheck rejects state-changing requests (POST/PUT/DELETE/PATCH)
// whose Origin header does not match the configured base URL. GET/HEAD/OPTIONS
// are safe methods and pass through. Requests with no Origin header are allowed
// because non-browser clients (CLI tools, the Pi daemon) legitimately omit it.
func csrfOriginCheck(baseURL string, next http.Handler) http.Handler {
	if baseURL == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && origin != baseURL {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
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

		// Don't redirect WebSocket, API, or healthcheck paths. /healthz in
		// particular is hit over plain HTTP against localhost by the
		// autodeploy binary, which expects 200 + JSON and would mis-fire
		// on every tick if redirected to the canonical HTTPS origin.
		if strings.HasPrefix(r.URL.Path, "/ws") || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		target := appURL + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}
