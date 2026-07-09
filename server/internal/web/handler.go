// Package web is the HTTP layer for signald. Handler wires together all
// stores, the signaling hub, and the auth layer into a single http.Handler
// via Router. Templates are embedded at build time; static assets are either
// embedded (production) or served from disk (DevMode). Rate limiters protect
// sensitive endpoints (auth, pairing, WebSocket upgrade) at the handler level.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/events"
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
	return template.FuncMap{
		"fmtPhone": line.FormatNumber,
		"fmtDuration": func(seconds int) string {
			if seconds < 60 {
				return fmt.Sprintf("%ds", seconds)
			}
			return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
		},
		"fmtDurationClock": func(seconds int) string {
			h, m, s := clockParts(seconds)
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
		// weekdays returns the seven days for the quiet-hours day picker,
		// indexed to match time.Weekday (Sunday = 0) so the template can pair
		// each label with the corresponding entry in QuietHours.Days.
		"weekdays": func() []struct {
			Index int
			Short string
		} {
			return []struct {
				Index int
				Short string
			}{
				{0, "Sun"}, {1, "Mon"}, {2, "Tue"}, {3, "Wed"},
				{4, "Thu"}, {5, "Fri"}, {6, "Sat"},
			}
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
			return toSegments(pct, pctPerSegment, pctBadThreshold, pctWarnThreshold)
		},
		"msToSegments": func(ms float32) segDesc {
			return toSegments(ms, msPerSegment, msBadThreshold, msWarnThreshold)
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

const cacheControlImmutable = "public, max-age=31536000, immutable"

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
	tmplActiveCalls          *template.Template
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
	// Rate limiters. All are Handler fields so Router() has a single
	// construction pattern and limiters can be shared across request types.
	authLimiter        *ratelimit.Limiter // POST /auth/magic (magic link request)
	magicVerifyLimiter *ratelimit.Limiter // GET  /auth/magic/{token}
	googleLoginLimiter *ratelimit.Limiter // GET  /auth/google/login
	pairingLimiter     *ratelimit.Limiter // POST /phones/pair
	inviteLimiter      *ratelimit.Limiter // POST /settings/household/invite
	wsLimiter          *ratelimit.Limiter // GET  /ws (WebSocket upgrade)
	// Updates
	releases *updates.GitHubReleases
	// Metrics is the optional Prometheus registry. When set, a request
	// timing/count middleware is wrapped around the public mux. nil disables
	// HTTP instrumentation entirely (useful for tests that don't care).
	metrics *metrics.Registry
}

// HandlerConfig carries Handler behavior knobs that are not collaborator
// dependencies (those live in Deps).
type HandlerConfig struct {
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
	// TrustedProxies is the reverse-proxy hop count used to resolve the client
	// IP from X-Forwarded-For for rate limiting (see httputil.ClientIP). It is
	// passed verbatim to the rate limiters; the default of 1 is supplied by the
	// config loader.
	TrustedProxies int
}

// Deps bundles the stores, hub, and other collaborators the web Handler
// depends on. Named fields prevent a silent swap between same-typed
// parameters.
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
	// RedisClient, when non-nil, backs the rate limiters with a shared counter
	// so limits hold across replicas. Nil selects the per-process in-memory
	// limiter, which is all dev and single-replica deployments need.
	RedisClient redis.UniversalClient
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
	funcMap := TemplateFuncs()
	// parsePage closes over the layout + shared-partials file list so each
	// page only names itself. Adding a new layout or partial touches one line.
	parsePage := func(page string) (*template.Template, error) {
		t, err := template.New("").Funcs(funcMap).ParseFS(templateFS,
			"templates/_partials.html",
			"templates/_changelog.html",
			"templates/layout-v2.html",
			"templates/layout-dialup.html",
			"templates/layout-answering-machine.html",
			"templates/"+page,
		)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		return t, nil
	}
	// parsePageWith parses a page and then folds in extra partials, so their
	// {{template "..."}} references resolve inside the page's template tree.
	// Partials are named bare (e.g. "_call-live-panel.html"); the "templates/"
	// prefix is applied here so callers use the same convention as parsePage
	// and fragment.
	parsePageWith := func(page string, partials ...string) (*template.Template, error) {
		t, err := parsePage(page)
		if err != nil {
			return nil, err
		}
		paths := make([]string, len(partials))
		for i, p := range partials {
			paths[i] = "templates/" + p
		}
		if _, err := t.ParseFS(templateFS, paths...); err != nil {
			return nil, fmt.Errorf("parse partials %v into %s: %w", partials, page, err)
		}
		return t, nil
	}

	// page and fragment record the first parse failure in perr so the long
	// run of template assignments below stays flat instead of repeating an
	// error check after every parse. perr is checked once at the end.
	var perr error
	page := func(p string, extra ...string) *template.Template {
		if perr != nil {
			return nil
		}
		var t *template.Template
		var err error
		if len(extra) == 0 {
			t, err = parsePage(p)
		} else {
			t, err = parsePageWith(p, extra...)
		}
		if err != nil {
			perr = err
		}
		return t
	}
	// fragment parses standalone partials into their own named template tree
	// (used for htmx responses that render a partial without a page layout).
	fragment := func(name string, files ...string) *template.Template {
		if perr != nil {
			return nil
		}
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = "templates/" + f
		}
		t, err := template.New(name).Funcs(funcMap).ParseFS(templateFS, paths...)
		if err != nil {
			perr = fmt.Errorf("parse %s: %w", name, err)
		}
		return t
	}

	tmplDashboard := page("dashboard.html", "dnd-toggle.html", "_dashboard-am-status.html")
	tmplDashboardAMStatus := fragment("dashboard-am-status", "_dashboard-am-status.html")
	tmplPhones := page("phones.html")
	tmplCalls := page("calls.html")
	tmplSettings := page("settings.html")
	tmplOnboard := page("onboard.html")
	tmplPhoneDetail := page("phone-detail.html")
	tmplLinks := page("links.html")
	tmplConnecting := page("connecting.html")
	tmplWelcome := page("welcome.html")
	tmplInvite := page("invite.html")
	tmplCallLivePanel := fragment("call-live-panel", "_call-live-panel.html")
	tmplConferenceLivePanel := fragment("conference-live-panel", "_conference-live-panel.html")
	tmplCallLiveDetail := page("call-live-detail.html", "_call-live-panel.html")
	tmplConferenceLiveDetail := page("conference-live-detail.html", "_conference-live-panel.html")
	tmplChangelog := fragment("changelog", "_partials.html", "_changelog.html")
	tmplActiveCalls := fragment("active-calls", "_active-calls.html")
	if perr != nil {
		return nil, perr
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

	// Rate limiters. RedisClient (when set) backs them with a shared counter so
	// the configured limits hold across all replicas; nil falls back to the
	// per-process in-memory limiter. onReject records a rejection metric when a
	// registry is wired.
	var onReject func(string)
	if deps.Metrics != nil {
		onReject = deps.Metrics.ObserveRateLimitRejection
	}
	newLimiter := func(name string, limit int) *ratelimit.Limiter {
		return ratelimit.New(ratelimit.Config{
			Name:           name,
			Limit:          limit,
			Window:         time.Minute,
			TrustedProxies: cfg.TrustedProxies,
			Redis:          deps.RedisClient,
			OnReject:       onReject,
		})
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
		tmplActiveCalls:          tmplActiveCalls,
		cfg:                      cfg,
		authStore:                deps.AuthStore,
		authHandlers:             deps.AuthHandlers,
		googleAuth:               deps.GoogleAuth,
		householdStore:           deps.HouseholdStore,
		pairingStore:             deps.PairingStore,
		linkStore:                deps.LinkStore,
		inviteStore:              deps.InviteStore,
		emailer:                  deps.Emailer,
		authLimiter:              newLimiter("auth_magic", 5),
		magicVerifyLimiter:       newLimiter("magic_verify", 10),
		googleLoginLimiter:       newLimiter("google_login", 10),
		pairingLimiter:           newLimiter("pairing", 5),
		inviteLimiter:            newLimiter("invite", 5),
		wsLimiter:                newLimiter("ws", wsRateLimit(cfg)),
		metrics:                  deps.Metrics,
	}, nil
}

// Hub returns the signaling Hub. Used by callers (e.g. main) that wire
// external callbacks and need to broadcast messages to connected devices.
func (h *Handler) Hub() *signaling.Hub {
	return h.hub
}

// SetReleases configures the release index server. Called after construction
// because the callback passed to NewGitHubReleases references the handler's Hub,
// which creates a circular initialization dependency if wired at NewHandler time.
func (h *Handler) SetReleases(r *updates.GitHubReleases) {
	h.releases = r
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	// Static assets: no auth required. In DevMode, serve from disk so
	// CSS/JS edits don't require a rebuild; otherwise serve the embedded
	// FS so the production binary is self-contained.
	mux.Handle("GET /static/", staticFileServer(h.cfg.DevMode, h.cfg.DevStaticDir))

	// Health check: no auth required
	mux.HandleFunc("GET /healthz", httputil.Healthz(version.Version))

	// Public routes: no auth required
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

	// Update release index endpoint (unauthenticated; phones fetch this)
	if h.releases != nil {
		mux.HandleFunc("GET /api/updates/releases", h.releases.ServeReleases())
		mux.HandleFunc("GET /api/release-audio/{component}/{version}", h.releases.ServeAudio())
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

	// Protected routes: require valid session
	protected := http.NewServeMux()
	protected.HandleFunc("GET /", h.handleDashboard)
	protected.HandleFunc("GET /connecting", h.handleConnecting)
	protected.HandleFunc("GET /welcome", h.handleWelcomeGet)
	protected.HandleFunc("POST /welcome", h.handleWelcomePost)
	protected.HandleFunc("GET /onboard", h.handleOnboardGet)
	protected.HandleFunc("POST /onboard", h.handleOnboardPost)
	protected.HandleFunc("GET /phones", h.handlePhonesGet)
	protected.Handle("POST /phones/pair", h.pairingLimiter.Middleware(http.HandlerFunc(h.handlePhonesPairPost)))
	protected.HandleFunc("GET /phones/{number}", h.handlePhoneDetail)
	protected.HandleFunc("GET /phones/{number}/name", h.handlePhoneNameGet)
	protected.HandleFunc("GET /phones/{number}/name/edit", h.handlePhoneNameEditGet)
	protected.HandleFunc("POST /phones/{number}/name", h.handlePhoneNamePost)
	protected.HandleFunc("POST /phones/{number}/number", h.handlePhoneNumberPost)
	protected.HandleFunc("POST /phones/{number}/voice-style", h.handlePhoneVoiceStylePost)
	protected.HandleFunc("POST /phones/{number}/silent-mode", h.handlePhoneSilentModePost)
	protected.HandleFunc("POST /phones/{number}/auto-update", h.handlePhoneAutoUpdatePost)
	protected.HandleFunc("POST /phones/{number}/voicemail", h.handlePhoneVoicemailPost)
	protected.HandleFunc("POST /phones/{number}/voicemail-toggle", h.handlePhoneVoicemailTogglePost)
	protected.HandleFunc("POST /phones/{number}/quiet-hours", h.handlePhoneQuietHoursPost)
	protected.HandleFunc("POST /phones/{number}/convert", h.handlePhoneConvert)
	protected.HandleFunc("POST /phones/{number}/delete", h.handlePhoneDelete)
	protected.HandleFunc("POST /phones/{number}/update", h.handlePhoneUpdate)
	protected.HandleFunc("GET /phones/{number}/online", h.handlePhoneOnline)
	protected.HandleFunc("GET /phones/{number}/update-status", h.handlePhoneUpdateStatus)
	protected.HandleFunc("POST /phones/{number}/factory-reset", h.handlePhoneFactoryReset)
	protected.HandleFunc("POST /phones/{number}/restart", h.handlePhoneRestart)
	protected.HandleFunc("POST /phones/{number}/ring-test", h.handlePhoneRingTest)
	protected.HandleFunc("POST /phones/{number}/dev-mode", h.handlePhoneDevMode)
	protected.HandleFunc("GET /phones/{number}/dev-mode-status", h.handlePhoneDevModeStatus)
	protected.HandleFunc("GET /phones/{number}/operator", h.handlePhoneOperator)
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
