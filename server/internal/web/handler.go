package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
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

type Handler struct {
	upgrader websocket.Upgrader
	lineStore   *line.Store
	deviceStore *device.Store
	hub      *signaling.Hub
	tracker  *calls.Tracker
	relay    *signaling.Relay
	// Per-page template sets to avoid {{define}} name conflicts
	tmplDashboard   *template.Template
	tmplPhones      *template.Template
	tmplCalls       *template.Template
	tmplSettings    *template.Template
	tmplOnboard     *template.Template
	tmplPhoneDetail *template.Template
	tmplLinks       *template.Template
	tmplConnecting  *template.Template
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

type HandlerConfig struct {
	Addr string
}

func NewHandler(lineStore *line.Store, deviceStore *device.Store, hub *signaling.Hub, tracker *calls.Tracker, relay *signaling.Relay, cfg HandlerConfig, authStore *auth.Store, authHandlers *auth.Handlers, googleAuth *auth.GoogleAuth, householdStore *household.Store, pairingStore *pairing.Store, linkStore *household.LinkStore, emailSender email.Sender, baseURL string, adminSecret string) (*Handler, error) {
	funcMap := template.FuncMap{
		"fmtPhone": line.FormatNumber,
		"fmtDuration": func(seconds int) string {
			if seconds < 60 {
				return fmt.Sprintf("%ds", seconds)
			}
			return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
		},
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
		relay:           relay,
		tmplDashboard:   tmplDashboard,
		tmplPhones:      tmplPhones,
		tmplCalls:       tmplCalls,
		tmplSettings:    tmplSettings,
		tmplOnboard:     tmplOnboard,
		tmplPhoneDetail: tmplPhoneDetail,
		tmplLinks:       tmplLinks,
		tmplConnecting:  tmplConnecting,
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

	// Static assets — no auth required
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

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

	// Annotate lines with active-call state. When both sides of the call are
	// own lines (intra-household), each card uses the other local line's name
	// as its peer instead of a phone-number fallback.
	for _, pair := range active {
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
		}
		if calleeRow != nil {
			calleeRow.OnCall = true
			calleeRow.OnCallElapsed = elapsed
			if callerRow != nil {
				calleeRow.OnCallPeerName = callerRow.Line.Name
			} else {
				calleeRow.OnCallPeerName = resolvePeerName(pair.Caller, linkedLineIndex)
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
			ActiveCalls: len(active),
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

type linesData struct {
	Page                  string
	Version               string
	CallHistoryEnabled    bool
	HouseholdName         string
	Lines                 []lineRow
	Error                 string
	PairError             string
	User                  *auth.User
	LatestPiVersion       string
	LatestFirmwareVersion string
}

type lineRow struct {
	Line            line.Line
	Online          bool
	OnCall          bool
	OnCallPeerName  string
	OnCallElapsed   string // "mm:ss" for the Dashboard room-card callout
	DeviceInfo      *signaling.DeviceInfoSnapshot
	UpdateAvailable bool // either Pi or firmware is behind the latest release
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

	// Fall back to global list if household lookup failed or feature disabled
	if lines == nil {
		lines, _ = h.lineStore.List()
	}

	online := h.hub.OnlineNumbers()
	onlineSet := make(map[string]bool, len(online))
	for _, n := range online {
		onlineSet[n] = true
	}

	var latestPi, latestFw string
	if h.Releases != nil {
		if idx := h.Releases.ReleaseIndex(); idx != nil {
			latestPi = idx.Pi.Latest
			latestFw = idx.Firmware.Latest
		}
	}

	rows := make([]lineRow, len(lines))
	for i, l := range lines {
		info := h.hub.DeviceInfo(l.Number)
		row := lineRow{Line: l, Online: onlineSet[l.Number], DeviceInfo: info}
		if info != nil {
			if latestPi != "" && info.PiVersion != "" && info.PiVersion != latestPi {
				row.UpdateAvailable = true
			}
			if latestFw != "" && info.FirmwareVersion != "" && info.FirmwareVersion != latestFw {
				row.UpdateAvailable = true
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
	renderWith(w, h.tmplPhones, layoutFor(r), h.buildLinesData(r, ""))
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

	http.Redirect(w, r, "/phones", http.StatusSeeOther)
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
	ln, err := h.lineStore.GetByNumber(number)
	if err != nil {
		http.NotFound(w, r)
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
			piReleases = idx.SortedReleases("pi")
			fwReleases = idx.SortedReleases("firmware")
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
	ln, err := h.lineStore.GetByNumber(number)
	if err != nil {
		http.NotFound(w, r)
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

	ln, err := h.lineStore.GetByNumber(number)
	if err != nil {
		http.Error(w, "line not found", http.StatusNotFound)
		return
	}

	if err := h.lineStore.Update(ln.ID, number, name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	ln, err := h.lineStore.GetByNumber(number)
	if err != nil {
		http.NotFound(w, r)
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

// pushLineSettings sends the updated settings to the device currently
// registered as the given number, if any. A missing device is not an error;
// the next time that device reconnects it will receive the latest settings
// via the registration push in relay.OnRegistered.
func (h *Handler) pushLineSettings(number string, settings line.Settings) error {
	if h.hub.Get(number) == nil {
		return nil
	}
	return h.hub.SendTo(number, &signaling.Message{
		Type: signaling.TypeLineSettings,
		To:   number,
		LineSettings: &signaling.LineSettings{
			VoiceStyle: settings.VoiceStyle,
		},
	})
}

func (h *Handler) handlePhoneUpdate(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
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
	ln, err := h.lineStore.GetByNumber(number)
	if errors.Is(err, line.ErrNotFound) {
		http.Error(w, "line not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("delete line: lookup failed", "number", number, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
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
	Calls              []calls.Call
	User               *auth.User
}

func (h *Handler) handleCalls(w http.ResponseWriter, r *http.Request) {
	hhName, callHistory, loc := h.householdContext(r)
	if !callHistory {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	var recent []calls.Call

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
				recentCalls, err := h.tracker.RecentForPhones(numbers, 100)
				if err != nil {
					slog.Error("query recent calls failed", "err", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				recent = recentCalls
			}
		}
	}
	if recent == nil {
		recent = []calls.Call{}
	}

	for i := range recent {
		recent[i].StartedAt = recent[i].StartedAt.In(loc)
	}

	renderWith(w, h.tmplCalls, layoutFor(r), callsData{Page: "calls", Version: version.Version, CallHistoryEnabled: callHistory, HouseholdName: hhName, Calls: recent, User: user})
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

	// Look up the link's status before revoking so we can pick accurate
	// post-action copy (pending invite -> "canceled", active link ->
	// "disconnected"). If the lookup fails, fall through with disconnected
	// copy as the safer default.
	wasPending := false
	if link, err := h.linkStore.GetByID(id); err == nil && link != nil {
		wasPending = link.Status == "pending"
	}

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
	lines, err := h.lineStore.List()
	if err != nil {
		slog.Error("api status: list lines failed", "err", err)
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}
	online := h.hub.OnlineNumbers()
	active := h.tracker.Active()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"total_lines":  len(lines),
		"online_lines": len(online),
		"active_calls": len(active),
	}); err != nil {
		slog.Error("api status: json encode failed", "err", err)
	}
}

func (h *Handler) handleAPIActiveCalls(w http.ResponseWriter, r *http.Request) {
	active := h.tracker.Active()
	pairs := make([]activePair, len(active))
	for i, a := range active {
		pairs[i] = activePair{Caller: a.Caller, Callee: a.Callee}
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

func mustMarshal(msg *signaling.Message) []byte {
	data, _ := msg.Marshal()
	return data
}

