package auth

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GoogleAuth handles Google OAuth2 login flow.
type GoogleAuth struct {
	config       *oauth2.Config
	store        *Store
	cookieDomain string // optional, e.g. ".digits.family"
}

// NewGoogleAuth creates a GoogleAuth handler. Pass empty clientID/clientSecret to disable.
// cookieDomain sets the session cookie Domain attribute (e.g. ".digits.family"); pass "" to omit.
func NewGoogleAuth(clientID, clientSecret, redirectURL, cookieDomain string, store *Store) *GoogleAuth {
	return &GoogleAuth{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
		store:        store,
		cookieDomain: cookieDomain,
	}
}

// Enabled returns true if Google OAuth credentials are configured.
func (g *GoogleAuth) Enabled() bool {
	return g.config.ClientID != "" && g.config.ClientSecret != ""
}

// HandleLogin redirects to Google consent screen.
func (g *GoogleAuth) HandleLogin(w http.ResponseWriter, r *http.Request) {
	state, _ := randomToken(16)
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	url := g.config.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// HandleCallback processes the Google OAuth callback.
func (g *GoogleAuth) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// Verify state
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	token, err := g.config.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "oauth exchange failed", http.StatusInternalServerError)
		return
	}

	// Fetch user info
	client := g.config.Client(r.Context(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		http.Error(w, "failed to get user info", http.StatusInternalServerError)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read user info", http.StatusInternalServerError)
		return
	}

	var info struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		http.Error(w, "failed to parse user info", http.StatusInternalServerError)
		return
	}

	// Find or create user
	user, err := g.store.GetUserByGoogleID(info.ID)
	if err != nil {
		// Try by email (user may have used magic link before)
		user, err = g.store.GetUserByEmail(info.Email)
		if err != nil {
			// New user
			googleID := info.ID
			user, err = g.store.CreateUser(info.Email, info.Name, &googleID)
			if err != nil {
				http.Error(w, "failed to create user", http.StatusInternalServerError)
				return
			}
		} else {
			// Link Google ID to existing account
			if err := g.store.LinkGoogleID(user.ID, info.ID); err != nil {
				log.Printf("auth: failed to link google ID for user %s: %v", user.ID, err)
			}
		}
	}

	if err := g.store.UpdateLastLogin(user.ID); err != nil {
		log.Printf("auth: failed to update last login for user %s: %v", user.ID, err)
	}

	// Create session
	sessionToken, _, err := g.store.CreateSession(user.ID, SessionTTL)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sessionToken,
		Domain:   g.cookieDomain,
		Path:     "/",
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
