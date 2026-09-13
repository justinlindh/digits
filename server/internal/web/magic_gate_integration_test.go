//go:build integration

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/email/emailtest"
)

func postLoginForm(h *Handler, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/magic", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

// Through the real router with the real invite store: a stranger gets no
// mail, an invited address does, and an invite that was cancelled no longer
// unlocks the form.
func TestMagicLinkRequest_InviteGate(t *testing.T) {
	h, database, authStore := setupHandler(t)
	_, hh := setupAuthedHousehold(t, h, database, authStore)
	sender := h.emailer.(*emailtest.Sender)
	ctx := context.Background()
	inviter, err := authStore.GetUserByEmail(ctx, "test@example.com")
	if err != nil {
		t.Fatalf("get inviter: %v", err)
	}

	if w := postLoginForm(h, url.Values{"email": {"nobody@example.com"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("stranger: got %d, want 303", w.Code)
	}
	if len(sender.Sent) != 0 {
		t.Fatalf("stranger received %d emails, want 0", len(sender.Sent))
	}

	inv, err := h.inviteStore.CreateInvite(ctx, hh.ID, "newcomer@example.com", inviter.ID)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	t.Cleanup(func() { _, _ = database.DB.Exec("DELETE FROM household_invites WHERE id = $1", inv.ID) })

	if w := postLoginForm(h, url.Values{"email": {"newcomer@example.com"}, "return_to": {"/invite/" + inv.Token}}); w.Code != http.StatusSeeOther {
		t.Fatalf("invited: got %d, want 303", w.Code)
	}
	if len(sender.Sent) != 1 || sender.Sent[0].To != "newcomer@example.com" {
		t.Fatalf("invited address: sent = %+v, want one magic link to it", sender.Sent)
	}

	if err := h.inviteStore.CancelInvite(ctx, inv.ID); err != nil {
		t.Fatalf("CancelInvite: %v", err)
	}
	sender.Sent = nil
	if w := postLoginForm(h, url.Values{"email": {"newcomer@example.com"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("cancelled: got %d, want 303", w.Code)
	}
	if len(sender.Sent) != 0 {
		t.Errorf("cancelled invite still unlocked the form: %d emails", len(sender.Sent))
	}
}
