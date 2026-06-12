package auth

import "testing"

func TestIsSafeRedirect(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"", false},
		{"/", true},
		{"/phones", true},
		{"/phones?tab=devices", true},
		{"//evil.com", false},
		{"//evil.com/phones", false},
		{`/\evil.com`, false},
		{"http://evil.com", false},
		{"https://evil.com/", false},
		{"phones", false},
		{`\/evil.com`, false},
	}
	for _, c := range cases {
		if got := isSafeRedirect(c.path); got != c.want {
			t.Errorf("isSafeRedirect(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestLoginRedirectFor(t *testing.T) {
	if got := LoginRedirectFor(nil); got != "/" {
		t.Errorf("LoginRedirectFor(nil) = %q, want /", got)
	}
	if got := LoginRedirectFor(&User{Theme: ThemeDialup}); got != "/connecting" {
		t.Errorf("LoginRedirectFor(dialup) = %q, want /connecting", got)
	}
	if got := LoginRedirectFor(&User{Theme: ThemeIntercom}); got != "/" {
		t.Errorf("LoginRedirectFor(intercom) = %q, want /", got)
	}
}

func TestSafeReturnTo(t *testing.T) {
	if got := safeReturnTo("/calls", nil); got != "/calls" {
		t.Errorf("safeReturnTo(/calls) = %q, want /calls", got)
	}
	// Invalid paths fall back to the theme-aware landing page.
	if got := safeReturnTo("//evil.com", nil); got != "/" {
		t.Errorf("safeReturnTo(//evil.com, nil) = %q, want /", got)
	}
	if got := safeReturnTo("", &User{Theme: ThemeDialup}); got != "/connecting" {
		t.Errorf("safeReturnTo(empty, dialup) = %q, want /connecting", got)
	}
}
