package autodeploy

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/email"
)

type Action string

const (
	ActionNoop       Action = "noop"
	ActionSkipFailed Action = "skip_failed_tag"
	ActionDeployed   Action = "deployed"
	ActionReverted   Action = "reverted"
	ActionCritical   Action = "critical"
)

// Step identifies which phase of a deploy attempt produced an error.
// Stored alongside emails so debounce keys on the structural step, not on
// a fragile substring of an error message.
type Step string

const (
	StepLogin       Step = "login"
	StepPull        Step = "pull"
	StepUp          Step = "up"
	StepHealthcheck Step = "healthcheck"
)

// needsRevert reports whether a failure at this step left the new container
// running and therefore warrants reverting to the previous version.
// Login and pull happen before any container churn.
func (s Step) needsRevert() bool {
	return s == StepUp || s == StepHealthcheck
}

// stepError tags an error with the deploy phase it came from. Returned by
// deployVersion and unwrapped by Run to drive revert and email-class logic.
type stepError struct {
	Step Step
	Err  error
}

func (e *stepError) Error() string { return string(e.Step) + ": " + e.Err.Error() }
func (e *stepError) Unwrap() error { return e.Err }

func stepOf(err error) Step {
	var se *stepError
	if errors.As(err, &se) {
		return se.Step
	}
	return ""
}

type Result struct {
	Action Action
	Tag    string
}

type HealthPoller interface {
	Poll(ctx context.Context, url, wantVersion string, interval time.Duration) error
}

type healthFn func(ctx context.Context, url, wantVersion string, interval time.Duration) error

func (f healthFn) Poll(ctx context.Context, url, wantVersion string, interval time.Duration) error {
	return f(ctx, url, wantVersion, interval)
}

func DefaultHealthPoller() HealthPoller { return healthFn(PollHealth) }

type GitHubReleases interface {
	LatestReleaseWithETag(ctx context.Context, repo, prefix, etag string) (Release, error)
}

type Deployer struct {
	Cfg    Config
	GH     GitHubReleases
	Runner Runner
	Health HealthPoller
	Mailer email.Sender
	Store  Store
	Logger *slog.Logger
	Now    func() time.Time
}

func (d *Deployer) log() *slog.Logger {
	if d.Logger == nil {
		return slog.Default()
	}
	return d.Logger
}

func (d *Deployer) now() time.Time {
	if d.Now == nil {
		return time.Now().UTC()
	}
	return d.Now().UTC()
}

func (d *Deployer) Run(ctx context.Context) (Result, error) {
	state, err := d.Store.Read()
	if err != nil {
		return Result{}, fmt.Errorf("read state: %w", err)
	}

	rel, err := d.GH.LatestReleaseWithETag(ctx, d.Cfg.Repo, d.Cfg.TagPrefix, state.GitHubETag)
	if err != nil {
		return Result{}, fmt.Errorf("github: %w", err)
	}
	if rel.NotModified {
		return Result{Action: ActionNoop}, nil
	}
	// 200 OK paths: if the response brought a new ETag (no-match or same-tag),
	// persist it so the next tick can ask with If-None-Match and stay under
	// the GitHub rate-limit budget.
	if rel.TagName == "" || rel.TagName == state.LastDeployedTag {
		if rel.ETag != "" && rel.ETag != state.GitHubETag {
			state.GitHubETag = rel.ETag
			_ = d.Store.Write(state)
		}
		return Result{Action: ActionNoop}, nil
	}
	if rel.TagName == state.LastAttemptTag && isFailed(state.LastAttemptStatus) {
		d.log().Info("skipping known-failed tag", "tag", rel.TagName, "status", state.LastAttemptStatus)
		return Result{Action: ActionSkipFailed, Tag: rel.TagName}, nil
	}

	version := strings.TrimPrefix(rel.TagName, d.Cfg.TagPrefix)
	prevVersion := strings.TrimPrefix(state.LastDeployedTag, d.Cfg.TagPrefix)
	d.log().Info("new release, deploying", "tag", rel.TagName, "commit", rel.CommitSHA, "version", version)

	state.LastAttemptTag = rel.TagName
	state.LastAttemptStatus = StatusInProgress
	state.LastAttemptAt = d.now()
	state.LastAttemptError = ""
	if err := d.Store.Write(state); err != nil {
		return Result{}, fmt.Errorf("write in-progress state: %w", err)
	}

	if err := d.deployVersion(ctx, version, d.Cfg.HealthTimeout); err != nil {
		step := stepOf(err)
		d.log().Error("deploy failed", "err", err, "step", step)

		if prevVersion != "" && step.needsRevert() {
			// Revert must not inherit a cancelled parent context: a user-
			// interrupted forward deploy would otherwise escalate to CRITICAL
			// without actually trying to bring the old container back.
			revertCtx := context.WithoutCancel(ctx)
			if revertErr := d.deployVersion(revertCtx, prevVersion, d.Cfg.RevertHealthTimeout); revertErr != nil {
				d.log().Error("revert also failed", "err", revertErr)
				state.LastAttemptStatus = StatusCritical
				state.LastAttemptError = fmt.Sprintf("deploy: %v; revert: %v", err, revertErr)
				d.finalize(&state, rel, "critical")
				_ = d.Store.Write(state)
				return Result{Action: ActionCritical, Tag: rel.TagName}, fmt.Errorf("critical: %w", revertErr)
			}
			state.LastAttemptStatus = StatusFailedReverted
			state.LastAttemptError = err.Error()
			d.finalize(&state, rel, string(step))
			_ = d.Store.Write(state)
			return Result{Action: ActionReverted, Tag: rel.TagName}, err
		}

		state.LastAttemptStatus = StatusFailed
		state.LastAttemptError = err.Error()
		d.finalize(&state, rel, string(step))
		_ = d.Store.Write(state)
		return Result{Tag: rel.TagName}, err
	}

	state.LastDeployedTag = rel.TagName
	state.LastDeployedCommitSHA = rel.CommitSHA
	state.LastDeployedAt = d.now()
	state.LastAttemptStatus = StatusSuccess
	state.LastAttemptError = ""
	state.LastEmailAt = time.Time{}
	state.LastEmailErrorClass = ""
	if rel.ETag != "" {
		state.GitHubETag = rel.ETag
	}
	if err := d.Store.Write(state); err != nil {
		return Result{Action: ActionDeployed, Tag: rel.TagName}, fmt.Errorf("write success state: %w", err)
	}
	return Result{Action: ActionDeployed, Tag: rel.TagName}, nil
}

func (d *Deployer) deployVersion(ctx context.Context, version string, healthTimeout time.Duration) error {
	env := []string{"BUILD_VERSION=" + version}

	if _, err := d.Runner.Run(ctx, RunSpec{
		Name:  "docker",
		Args:  []string{"login", "ghcr.io", "-u", d.Cfg.GHCRUsername, "--password-stdin"},
		Stdin: []byte(d.Cfg.GHCRToken),
	}); err != nil {
		return &stepError{Step: StepLogin, Err: err}
	}

	composeArgs := []string{
		"compose",
		"-p", d.Cfg.ComposeProject,
		"-f", d.Cfg.ComposeFile,
		"--env-file", d.Cfg.ComposeEnvFile,
	}

	pullArgs := append(append([]string{}, composeArgs...), "pull")
	pullArgs = append(pullArgs, d.Cfg.Services...)
	if _, err := d.Runner.Run(ctx, RunSpec{
		Name: "docker", Args: pullArgs, Dir: d.Cfg.ComposeDir, Env: env,
	}); err != nil {
		return &stepError{Step: StepPull, Err: err}
	}

	upArgs := append(append([]string{}, composeArgs...), "up", "-d", "--wait")
	upArgs = append(upArgs, d.Cfg.Services...)
	if _, err := d.Runner.Run(ctx, RunSpec{
		Name: "docker", Args: upArgs, Dir: d.Cfg.ComposeDir, Env: env,
	}); err != nil {
		return &stepError{Step: StepUp, Err: err}
	}

	hctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	for _, u := range d.Cfg.HealthURLs {
		if err := d.Health.Poll(hctx, u, version, 2*time.Second); err != nil {
			return &stepError{Step: StepHealthcheck, Err: err}
		}
	}
	return nil
}

func (d *Deployer) finalize(state *State, rel Release, errorClass string) {
	subject := fmt.Sprintf("[digits-prod] FAILED deploying %s", rel.TagName)
	if state.LastAttemptStatus == StatusCritical {
		subject = fmt.Sprintf("[digits-prod] CRITICAL: deploy and revert failed for %s", rel.TagName)
	}
	if state.LastAttemptStatus == StatusFailedReverted {
		subject = fmt.Sprintf("[digits-prod] FAILED deploying %s (reverted to %s)", rel.TagName, state.LastDeployedTag)
	}

	if !state.LastEmailAt.IsZero() &&
		state.LastEmailErrorClass == errorClass &&
		d.now().Sub(state.LastEmailAt) < d.Cfg.EmailDebounce {
		d.log().Info("email debounced", "class", errorClass)
		return
	}

	body := fmt.Sprintf("tag: %s\ncommit: %s\nstatus: %s\nstep: %s\nerror: %s\n",
		rel.TagName, rel.CommitSHA, state.LastAttemptStatus, errorClass, state.LastAttemptError)
	// email.Sender expects HTML; wrap in <pre> so newlines render and escape
	// the payload so docker/git error text can't break the markup.
	htmlBody := "<pre>" + html.EscapeString(body) + "</pre>"
	if err := d.Mailer.Send(d.Cfg.AlertTo, subject, htmlBody); err != nil {
		d.log().Error("email send failed", "err", err)
		return
	}
	state.LastEmailAt = d.now()
	state.LastEmailErrorClass = errorClass
}

func isFailed(s AttemptStatus) bool {
	switch s {
	case StatusFailed, StatusFailedReverted, StatusCritical:
		return true
	}
	return false
}
