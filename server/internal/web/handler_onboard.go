package web

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/justinlindh/digits/server/internal/auth"
)

type onboardData struct {
	chromeData
	SuggestedName string
	User          *auth.User
}

func (h *Handler) handleOnboardGet(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	suggested := "My Family"
	if user != nil && user.Name != "" {
		suggested = user.Name + "'s Family"
	}
	renderWith(w, h.tmplOnboard, layoutFor(r), onboardData{
		chromeData:    chromeFor("onboard", h.primaryHousehold(r)),
		SuggestedName: suggested,
		User:          user,
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
	_, err := h.householdStore.Create(r.Context(), name, user.ID)
	if err != nil {
		slog.Error("create household failed", "err", err)
		http.Error(w, "failed to create household", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

