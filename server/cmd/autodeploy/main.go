// Command autodeploy rolls new Pi and firmware builds out to the production
// server. Each run checks GitHub Releases for a newer tag, deploys it, polls
// a health check, and reverts and emails on failure. It is a one-shot process
// (invoked on a timer): -dry-run reports what would happen without changing
// anything, and -retry clears failed-attempt spin protection before running.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/justinlindh/digits/server/internal/autodeploy"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/version"
)

func main() {
	configPath := flag.String("config", "/etc/digits-autodeploy/config.env", "config file path")
	dryRun := flag.Bool("dry-run", false, "read-only: log what would happen but do not change anything")
	retry := flag.Bool("retry", false, "clear failed-attempt spin-protection before running")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger = logger.With("component", "autodeploy", "autodeploy_version", version.Version)
	slog.SetDefault(logger)

	cfg, err := autodeploy.LoadConfig(*configPath)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(2)
	}

	store := autodeploy.NewFileStore(cfg.StateFile)

	if *retry {
		s, err := store.Read()
		if err != nil {
			logger.Error("read state for --retry", "err", err)
			os.Exit(2)
		}
		s.LastAttemptTag = ""
		s.LastAttemptStatus = ""
		s.LastAttemptError = ""
		if err := store.Write(s); err != nil {
			logger.Error("write state for --retry", "err", err)
			os.Exit(2)
		}
		logger.Info("cleared spin-protection")
	}

	gh := autodeploy.NewGitHubClient("", cfg.GitHubToken)
	mailer := email.NewSMTPSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)

	d := &autodeploy.Deployer{
		Cfg:    cfg,
		GH:     gh,
		Runner: autodeploy.NewExecRunner(),
		Health: autodeploy.PollHealth,
		Mailer: mailer,
		Store:  store,
		Logger: logger,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if *dryRun {
		s, err := store.Read()
		if err != nil {
			logger.Error("dry-run read state", "err", err)
			os.Exit(2)
		}
		rel, err := gh.LatestReleaseWithETag(ctx, cfg.Repo, cfg.TagPrefix, s.GitHubETag)
		if err != nil {
			logger.Error("dry-run github", "err", err)
			os.Exit(1)
		}
		fmt.Printf("state.LastDeployedTag=%s\nlatest.TagName=%s\nlatest.NotModified=%v\n",
			s.LastDeployedTag, rel.TagName, rel.NotModified)
		return
	}

	res, err := d.Run(ctx)
	if err != nil {
		logger.Error("run", "err", err, "tag", res.Tag, "action", res.Action)
		os.Exit(1)
	}
	if res.Action == autodeploy.ActionNoop {
		logger.Debug("run", "action", res.Action)
		return
	}
	logger.Info("run", "action", res.Action, "tag", res.Tag)
}
