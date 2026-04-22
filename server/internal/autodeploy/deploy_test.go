package autodeploy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type mockGH struct {
	rel Release
	err error
}

func (m *mockGH) LatestReleaseWithETag(ctx context.Context, repo, prefix, etag string) (Release, error) {
	return m.rel, m.err
}

type mockRunner struct {
	calls []RunSpec
	errs  map[string]error // keyed by "Name" or "Name arg0"
}

func (m *mockRunner) Run(ctx context.Context, spec RunSpec) (RunOutput, error) {
	m.calls = append(m.calls, spec)
	if m.errs != nil {
		if e, ok := m.errs[spec.Name]; ok {
			return RunOutput{}, e
		}
		if len(spec.Args) > 0 {
			if e, ok := m.errs[spec.Name+" "+spec.Args[0]]; ok {
				return RunOutput{}, e
			}
		}
	}
	return RunOutput{}, nil
}

type sentEmail struct {
	To      string
	Subject string
	Body    string
}

type mockMailer struct {
	sent []sentEmail
	err  error
}

func (m *mockMailer) Send(to, subject, htmlBody string) error {
	m.sent = append(m.sent, sentEmail{To: to, Subject: subject, Body: htmlBody})
	return m.err
}

type mockHealth struct {
	errs map[string]error // keyed by wantVersion
}

func (m *mockHealth) Poll(ctx context.Context, url, wantVersion string, interval time.Duration) error {
	if m.errs == nil {
		return nil
	}
	return m.errs[wantVersion]
}

type memStore struct{ s State }

func (m *memStore) Read() (State, error) { return m.s, nil }
func (m *memStore) Write(s State) error  { m.s = s; return nil }

func baseCfg() Config {
	return Config{
		Repo: "justinlindh/digits", TagPrefix: "server/v",
		ComposeDir: "/srv", ComposeFile: "docker-compose.prod.yml",
		ComposeProject: "digits-prod", ComposeEnvFile: "/srv/.env.prod",
		Services:     []string{"signald", "admind"},
		HealthURLs:   []string{"http://x/healthz", "http://y/healthz"},
		GHCRUsername: "u", GHCRToken: "t", StateFile: "/tmp/state.json",
		SMTPHost: "s", SMTPPort: "587", SMTPFrom: "f@x", AlertTo: "a@x",
		HealthTimeout: 5 * time.Second, RevertHealthTimeout: 5 * time.Second,
		EmailDebounce: 30 * time.Minute, PollInterval: 10 * time.Millisecond,
	}
}

func TestDeployNoop_SameTag(t *testing.T) {
	d := &Deployer{
		Cfg:    baseCfg(),
		GH:     &mockGH{rel: Release{TagName: "server/v1.9.0", CommitSHA: "abc"}},
		Runner: &mockRunner{}, Health: &mockHealth{}, Mailer: &mockMailer{},
		Store: &memStore{s: State{LastDeployedTag: "server/v1.9.0"}},
		Now:   func() time.Time { return time.Unix(0, 0) },
	}
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionNoop {
		t.Errorf("action=%q", res.Action)
	}
}

func TestDeployNoop_NotModified(t *testing.T) {
	d := &Deployer{
		Cfg:    baseCfg(),
		GH:     &mockGH{rel: Release{NotModified: true}},
		Runner: &mockRunner{}, Health: &mockHealth{}, Mailer: &mockMailer{},
		Store:  &memStore{s: State{LastDeployedTag: "server/v1.9.0"}},
		Now:    func() time.Time { return time.Unix(0, 0) },
	}
	res, _ := d.Run(context.Background())
	if res.Action != ActionNoop {
		t.Errorf("action=%q", res.Action)
	}
}

func TestDeployHappyPath_NoGHCRToken_SkipsLogin(t *testing.T) {
	// With an empty GHCRToken (public images), docker login is unnecessary
	// and would fail with empty stdin. autodeploy must skip it and proceed
	// straight to pull + up.
	runner := &mockRunner{}
	cfg := baseCfg()
	cfg.GHCRToken = ""
	d := &Deployer{
		Cfg:    cfg,
		GH:     &mockGH{rel: Release{TagName: "server/v1.9.1", CommitSHA: "def"}},
		Runner: runner, Health: &mockHealth{}, Mailer: &mockMailer{},
		Store: &memStore{s: State{LastDeployedTag: "server/v1.9.0"}},
		Now:   func() time.Time { return time.Unix(1000, 0).UTC() },
	}
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Action != ActionDeployed {
		t.Errorf("action=%q", res.Action)
	}
	for _, c := range runner.calls {
		if c.Name == "docker" && len(c.Args) > 0 && c.Args[0] == "login" {
			t.Errorf("docker login was invoked despite empty GHCRToken: %v", c.Args)
		}
	}
	if len(runner.calls) == 0 {
		t.Fatal("no runner calls")
	}
	if !strings.Contains(strings.Join(runner.calls[0].Args, " "), "pull") {
		t.Errorf("first call should be pull when login is skipped, got: %v", runner.calls[0].Args)
	}
}

func TestDeployHappyPath(t *testing.T) {
	runner := &mockRunner{}
	mailer := &mockMailer{}
	store := &memStore{s: State{LastDeployedTag: "server/v1.9.0"}}
	d := &Deployer{
		Cfg:    baseCfg(),
		GH:     &mockGH{rel: Release{TagName: "server/v1.9.1", CommitSHA: "def", ETag: `W/"e"`}},
		Runner: runner, Health: &mockHealth{}, Mailer: mailer,
		Store: store,
		Now:   func() time.Time { return time.Unix(1000, 0).UTC() },
	}
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Action != ActionDeployed {
		t.Errorf("action=%q", res.Action)
	}
	if store.s.LastDeployedTag != "server/v1.9.1" {
		t.Errorf("LastDeployedTag=%q", store.s.LastDeployedTag)
	}
	if store.s.LastAttemptStatus != StatusSuccess {
		t.Errorf("LastAttemptStatus=%q", store.s.LastAttemptStatus)
	}
	if store.s.GitHubETag != `W/"e"` {
		t.Errorf("ETag=%q", store.s.GitHubETag)
	}
	names := []string{}
	for _, c := range runner.calls {
		names = append(names, c.Name+" "+strings.Join(c.Args, " "))
	}
	if len(names) < 3 {
		t.Fatalf("not enough runner calls: %v", names)
	}
	if !strings.Contains(names[0], "login") {
		t.Errorf("first call not login: %s", names[0])
	}
	if !strings.Contains(names[1], "pull") {
		t.Errorf("second call not pull: %s", names[1])
	}
	if !strings.Contains(names[2], "up") || !strings.Contains(names[2], "--wait") {
		t.Errorf("third call not up --wait: %s", names[2])
	}
	if len(mailer.sent) != 0 {
		t.Errorf("email sent on success: %+v", mailer.sent)
	}
}

func TestDeploy_LoginFails(t *testing.T) {
	runner := &mockRunner{errs: map[string]error{"docker login": errors.New("denied")}}
	mailer := &mockMailer{}
	store := &memStore{s: State{LastDeployedTag: "server/v1.9.0"}}
	d := &Deployer{
		Cfg: baseCfg(),
		GH:  &mockGH{rel: Release{TagName: "server/v1.9.1", CommitSHA: "def"}},
		Runner: runner, Health: &mockHealth{}, Mailer: mailer, Store: store,
		Now: func() time.Time { return time.Unix(1000, 0).UTC() },
	}
	res, err := d.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if res.Action != ActionFailed {
		t.Errorf("action=%q, want ActionFailed", res.Action)
	}
	if store.s.LastDeployedTag != "server/v1.9.0" {
		t.Errorf("LastDeployedTag changed to %q", store.s.LastDeployedTag)
	}
	if store.s.LastAttemptStatus != StatusFailed {
		t.Errorf("LastAttemptStatus=%q", store.s.LastAttemptStatus)
	}
	// Login failure must NOT trigger a revert: nothing was pulled or restarted,
	// so the only docker call should be the failed login itself.
	if len(runner.calls) != 1 {
		t.Errorf("expected exactly 1 runner call (the failed login), got %d", len(runner.calls))
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("expected 1 email, got %d", len(mailer.sent))
	}
	if !strings.Contains(mailer.sent[0].Subject, "FAILED") {
		t.Errorf("subject=%q", mailer.sent[0].Subject)
	}
}

func TestDeploy_HealthFails_RevertSucceeds(t *testing.T) {
	runner := &mockRunner{}
	mailer := &mockMailer{}
	store := &memStore{s: State{LastDeployedTag: "server/v1.9.0"}}
	health := &mockHealth{errs: map[string]error{"1.9.1": errors.New("timeout")}}
	d := &Deployer{
		Cfg: baseCfg(),
		GH:  &mockGH{rel: Release{TagName: "server/v1.9.1", CommitSHA: "def"}},
		Runner: runner, Health: health, Mailer: mailer, Store: store,
		Now: func() time.Time { return time.Unix(1000, 0).UTC() },
	}
	_, err := d.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if store.s.LastAttemptStatus != StatusFailedReverted {
		t.Errorf("LastAttemptStatus=%q", store.s.LastAttemptStatus)
	}
	if store.s.LastDeployedTag != "server/v1.9.0" {
		t.Errorf("LastDeployedTag changed to %q", store.s.LastDeployedTag)
	}
	sawRevert := false
	for _, c := range runner.calls {
		for _, e := range c.Env {
			if e == "BUILD_VERSION=1.9.0" {
				sawRevert = true
			}
		}
	}
	if !sawRevert {
		t.Error("expected revert up with BUILD_VERSION=1.9.0")
	}
	if len(mailer.sent) != 1 {
		t.Errorf("sent=%d", len(mailer.sent))
	}
}

func TestDeploy_HealthFails_RevertAlsoFails(t *testing.T) {
	runner := &mockRunner{}
	mailer := &mockMailer{}
	store := &memStore{s: State{LastDeployedTag: "server/v1.9.0"}}
	health := &mockHealth{errs: map[string]error{
		"1.9.1": errors.New("timeout"),
		"1.9.0": errors.New("still broken"),
	}}
	d := &Deployer{
		Cfg: baseCfg(),
		GH:  &mockGH{rel: Release{TagName: "server/v1.9.1"}},
		Runner: runner, Health: health, Mailer: mailer, Store: store,
		Now: func() time.Time { return time.Unix(1000, 0).UTC() },
	}
	_, err := d.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if store.s.LastAttemptStatus != StatusCritical {
		t.Errorf("LastAttemptStatus=%q", store.s.LastAttemptStatus)
	}
	if len(mailer.sent) != 1 {
		t.Errorf("sent=%d", len(mailer.sent))
	}
	if !strings.Contains(mailer.sent[0].Subject, "CRITICAL") {
		t.Errorf("subject=%q", mailer.sent[0].Subject)
	}
}

func TestDeploy_SpinProtection(t *testing.T) {
	store := &memStore{s: State{
		LastDeployedTag:   "server/v1.9.0",
		LastAttemptTag:    "server/v1.9.1",
		LastAttemptStatus: StatusFailed,
	}}
	runner := &mockRunner{}
	d := &Deployer{
		Cfg:    baseCfg(),
		GH:     &mockGH{rel: Release{TagName: "server/v1.9.1"}},
		Runner: runner, Health: &mockHealth{}, Mailer: &mockMailer{},
		Store: store,
		Now:   func() time.Time { return time.Unix(1000, 0).UTC() },
	}
	res, err := d.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionSkipFailed {
		t.Errorf("action=%q", res.Action)
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner invoked during spin-protection: %d calls", len(runner.calls))
	}
}

func TestDeploy_EmailDebounce(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	store := &memStore{s: State{
		LastDeployedTag:     "server/v1.9.0",
		LastAttemptTag:      "server/v1.9.1",
		LastAttemptStatus:   StatusFailed,
		LastEmailAt:         now.Add(-5 * time.Minute),
		LastEmailErrorClass: "login",
	}}
	runner := &mockRunner{errs: map[string]error{"docker login": errors.New("denied")}}
	mailer := &mockMailer{}
	d := &Deployer{
		Cfg: baseCfg(),
		GH:  &mockGH{rel: Release{TagName: "server/v1.9.2"}},
		Runner: runner, Health: &mockHealth{}, Mailer: mailer, Store: store,
		Now: func() time.Time { return now },
	}
	_, _ = d.Run(context.Background())
	if len(mailer.sent) != 0 {
		t.Errorf("email sent during debounce window: %+v", mailer.sent)
	}
}

func TestDeploy_EmailAfterDebounceWindow(t *testing.T) {
	// LastEmailAt is older than EmailDebounce (30m), so a new failure of the
	// same class must email again.
	now := time.Unix(10_000, 0).UTC()
	store := &memStore{s: State{
		LastDeployedTag:     "server/v1.9.0",
		LastAttemptTag:      "server/v1.9.1",
		LastAttemptStatus:   StatusFailed,
		LastEmailAt:         now.Add(-45 * time.Minute),
		LastEmailErrorClass: "login",
	}}
	runner := &mockRunner{errs: map[string]error{"docker login": errors.New("denied")}}
	mailer := &mockMailer{}
	d := &Deployer{
		Cfg: baseCfg(),
		GH:  &mockGH{rel: Release{TagName: "server/v1.9.2"}},
		Runner: runner, Health: &mockHealth{}, Mailer: mailer, Store: store,
		Now: func() time.Time { return now },
	}
	_, _ = d.Run(context.Background())
	if len(mailer.sent) != 1 {
		t.Fatalf("expected 1 email after debounce window expired, got %d", len(mailer.sent))
	}
	if !store.s.LastEmailAt.Equal(now) {
		t.Errorf("LastEmailAt=%v, want %v", store.s.LastEmailAt, now)
	}
}
