package admin

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/justinlindh/digits/server/internal/httputil"
	"github.com/justinlindh/digits/server/internal/version"
)

//go:embed templates/*.html
var adminTemplateFS embed.FS

//go:embed static
var adminStaticFS embed.FS

type Server struct {
	cfg       *Config
	db        *AdminDB
	auth      *AuthStore
	stats     *StatsClient
	tmplLogin *template.Template
	tmplDash  *template.Template
	tmplUsers *template.Template
	tmplHH    *template.Template
	tmplHP    *template.Template
	httpSrv   *http.Server
}

func NewServer(cfg *Config, db *AdminDB, auth *AuthStore) *Server {
	parse := func(pages ...string) *template.Template {
		t, err := template.ParseFS(adminTemplateFS, pages...)
		if err != nil {
			slog.Error("parse admin templates", "err", err)
			os.Exit(1)
		}
		return t
	}

	s := &Server{
		cfg:       cfg,
		db:        db,
		auth:      auth,
		stats:     NewStatsClient(cfg.StatsURL, cfg.StatsSecret),
		tmplLogin: parse("templates/layout.html", "templates/login.html"),
		tmplDash:  parse("templates/layout.html", "templates/dashboard.html"),
		tmplUsers: parse("templates/layout.html", "templates/users.html"),
		tmplHH:    parse("templates/layout.html", "templates/households.html"),
		tmplHP:    parse("templates/layout.html", "templates/health.html"),
	}
	s.httpSrv = &http.Server{
		Addr:    cfg.Addr,
		Handler: s.router(),
	}
	return s
}

func (s *Server) ListenAndServe() error {
	return s.httpSrv.ListenAndServe()
}

func (s *Server) router() http.Handler {
	mux := http.NewServeMux()

	// Static assets — no auth required
	mux.Handle("GET /admin/static/", http.StripPrefix("/admin", http.FileServer(http.FS(adminStaticFS))))

	// Health check — no auth required
	mux.HandleFunc("GET /healthz", httputil.Healthz(version.Version))

	// Public
	mux.HandleFunc("GET /admin/login", s.handleLoginGet)
	mux.HandleFunc("POST /admin/login", s.handleLoginPost)

	// Protected
	mux.HandleFunc("GET /admin/", s.requireAdmin(s.handleDashboard))
	mux.HandleFunc("GET /admin/users", s.requireAdmin(s.handleUsers))
	mux.HandleFunc("GET /admin/households", s.requireAdmin(s.handleHouseholds))
	mux.HandleFunc("GET /admin/health", s.requireAdmin(s.handleHealth))
	mux.HandleFunc("POST /admin/logout", s.handleLogout)

	return securityHeadersMiddleware(mux)
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("admin_session")
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if s.auth == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		_, err = s.auth.ValidateSession(cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

type loginData struct {
	Page  string
	Error string
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmplLogin.ExecuteTemplate(w, "layout.html", loginData{Page: "login"}); err != nil {
		slog.Error("admin: render template", "err", err)
	}
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	if s.auth == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.tmplLogin.ExecuteTemplate(w, "layout.html", loginData{Page: "login", Error: "auth not configured"}); err != nil {
			slog.Error("admin: render template", "err", err)
		}
		return
	}

	adminID, err := s.auth.VerifyLogin(username, password)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.tmplLogin.ExecuteTemplate(w, "layout.html", loginData{Page: "login", Error: "Invalid credentials"}); err != nil {
			slog.Error("admin: render template", "err", err)
		}
		return
	}

	token, err := s.auth.CreateSession(adminID)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.tmplLogin.ExecuteTemplate(w, "layout.html", loginData{Page: "login", Error: "Session error"}); err != nil {
			slog.Error("admin: render template", "err", err)
		}
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "admin_session",
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_session",
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

type dashData struct {
	Page       string
	Stats      Stats
	StatsError string
	DBHealthy  bool
}

func (s *Server) fetchDashData(page string) dashData {
	d := dashData{Page: page, DBHealthy: true}
	stats, err := s.stats.Fetch()
	if err != nil {
		d.StatsError = err.Error()
	} else {
		d.Stats = *stats
	}
	if s.db != nil {
		if err := s.db.DB.Ping(); err != nil {
			d.DBHealthy = false
		}
	}
	return d
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmplDash.ExecuteTemplate(w, "layout.html", s.fetchDashData("dashboard")); err != nil {
		slog.Error("admin: render template", "err", err)
	}
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmplUsers.ExecuteTemplate(w, "layout.html", s.fetchDashData("users")); err != nil {
		slog.Error("admin: render template", "err", err)
	}
}

func (s *Server) handleHouseholds(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmplHH.ExecuteTemplate(w, "layout.html", s.fetchDashData("households")); err != nil {
		slog.Error("admin: render template", "err", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := s.fetchDashData("health")
	if s.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.db.DB.PingContext(ctx); err != nil {
			data.DBHealthy = false
		}
	}
	if err := s.tmplHP.ExecuteTemplate(w, "layout.html", data); err != nil {
		slog.Error("admin: render template", "err", err)
	}
}
