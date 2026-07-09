package web

import (
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
)

func TestUserDisplayLabel(t *testing.T) {
	cases := []struct {
		name string
		user *auth.User
		want string
	}{
		{"nil user", nil, ""},
		{"name wins", &auth.User{Name: "Ada Lovelace", Email: "ada@example.com"}, "Ada Lovelace"},
		{"email local part when no name", &auth.User{Email: "ada@example.com"}, "ada"},
		{"bare email when no @", &auth.User{Email: "operator"}, "operator"},
		{"leading @ falls back to whole email", &auth.User{Email: "@host"}, "@host"},
		{"empty user", &auth.User{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := userDisplayLabel(tc.user); got != tc.want {
				t.Errorf("userDisplayLabel(%+v) = %q, want %q", tc.user, got, tc.want)
			}
		})
	}
}

func TestBuildLinkedLineIndex(t *testing.T) {
	families := []linkedFamilyRow{
		{Name: "Smiths", Lines: []line.Line{
			{Number: "5550001", Name: "Kitchen"},
			{Number: "5550002", Name: "Study"},
		}},
		{Name: "Joneses", Lines: []line.Line{
			{Number: "5550003", Name: "Hall"},
		}},
	}
	index := buildLinkedLineIndex(families)

	want := map[string]string{
		"5550001": "Smiths · Kitchen",
		"5550002": "Smiths · Study",
		"5550003": "Joneses · Hall",
	}
	if len(index) != len(want) {
		t.Fatalf("index size = %d, want %d (%v)", len(index), len(want), index)
	}
	for num, label := range want {
		if index[num] != label {
			t.Errorf("index[%q] = %q, want %q", num, index[num], label)
		}
	}
}

func TestBuildLinkedLineIndex_Empty(t *testing.T) {
	index := buildLinkedLineIndex(nil)
	if len(index) != 0 {
		t.Errorf("expected empty index, got %v", index)
	}
}

func TestResolvePeerName(t *testing.T) {
	linked := map[string]string{"5550001": "Smiths · Kitchen"}

	if got := resolvePeerName("5550001", linked); got != "Smiths · Kitchen" {
		t.Errorf("resolvePeerName(known) = %q, want the linked label", got)
	}

	// Unknown number falls back to the phone-number formatter.
	const unknown = "5559999"
	if got := resolvePeerName(unknown, linked); got != line.FormatNumber(unknown) {
		t.Errorf("resolvePeerName(unknown) = %q, want %q", got, line.FormatNumber(unknown))
	}

	// Nil map behaves like an empty map (all fallbacks).
	if got := resolvePeerName(unknown, nil); got != line.FormatNumber(unknown) {
		t.Errorf("resolvePeerName(nil map) = %q, want %q", got, line.FormatNumber(unknown))
	}
}

func TestWSRateLimit(t *testing.T) {
	if got := wsRateLimit(HandlerConfig{}); got != 30 {
		t.Errorf("wsRateLimit(zero) = %d, want default 30", got)
	}
	if got := wsRateLimit(HandlerConfig{WSRateLimitPerMin: 0}); got != 30 {
		t.Errorf("wsRateLimit(0) = %d, want default 30", got)
	}
	if got := wsRateLimit(HandlerConfig{WSRateLimitPerMin: 120}); got != 120 {
		t.Errorf("wsRateLimit(120) = %d, want 120", got)
	}
}

func TestMatchMembership(t *testing.T) {
	memberships := []household.Membership{
		{Household: &household.Household{ID: "hh-a"}, Role: "member"},
		{Household: &household.Household{ID: "hh-b"}, Role: "admin"},
		{Household: nil, Role: "admin"}, // defensive: a nil household never matches
	}

	t.Run("admin household resolves household and role in one pass", func(t *testing.T) {
		hh, role, ok := matchMembership(memberships, "hh-b")
		if !ok {
			t.Fatal("expected hh-b to match")
		}
		if hh == nil || hh.ID != "hh-b" {
			t.Fatalf("wrong household: %+v", hh)
		}
		if role != "admin" {
			t.Fatalf("role = %q, want admin", role)
		}
	})

	t.Run("member household returns member role", func(t *testing.T) {
		_, role, ok := matchMembership(memberships, "hh-a")
		if !ok || role != "member" {
			t.Fatalf("hh-a: ok=%v role=%q, want ok=true role=member", ok, role)
		}
	})

	t.Run("household the user does not belong to does not match", func(t *testing.T) {
		hh, role, ok := matchMembership(memberships, "hh-unknown")
		if ok || hh != nil || role != "" {
			t.Fatalf("unexpected match: hh=%+v role=%q ok=%v", hh, role, ok)
		}
	})

	t.Run("empty membership set does not match", func(t *testing.T) {
		if _, _, ok := matchMembership(nil, "hh-a"); ok {
			t.Fatal("nil memberships should not match")
		}
	})
}
