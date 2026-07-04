// Package profiling wires the Pyroscope continuous-profiling SDK for
// signald. Profiles are pushed to a Pyroscope server via HTTP; the server
// is expected to be in-cluster and not internet-reachable.
//
// Privacy: continuous profiles capture stack traces, goroutine names,
// memory allocation sites, and CPU samples. None of those carry user
// data on their own. The sensitive surface in Pyroscope is the *label
// set*: like Prometheus, every profile is keyed by a fixed set of
// labels, and a careless label can echo a user identifier into the
// profile UI exactly as a metric label can.
//
// What is collected:
//
//   - CPU profile (pprof "profile") at the SDK default rate (100 Hz).
//   - Allocation profile ("alloc_objects", "alloc_space",
//     "inuse_objects", "inuse_space").
//   - Goroutine profile ("goroutines").
//
// Label set (closed):
//
//   - service: "signald", picked by the calling main.
//   - version: build version from internal/version.
//   - hostname: os.Hostname() (the k8s pod name in the deployment;
//     attacker-rebinding away from k8s would only expose the OS hostname,
//     never a user identifier).
//   - environment: "k8s" or "docker" or "dev", supplied by the operator
//     via DEPLOYMENT_ENV. Defaults to "" (unset) so a stale label
//     never appears in the UI.
//
// What is NEVER pushed as a label:
//
//   - User IDs, household IDs, line numbers, call IDs.
//   - Any per-request data: profiles are aggregated over a 10-second
//     window, so a label that varies per request would balloon the
//     label set and create a privacy surface.
//   - Free-form strings from any external input.
//
// The Init function is safe to call from cmd/signald regardless of whether
// PYROSCOPE_SERVER_ADDRESS is configured: with no address, Init returns a
// no-op stop closure and profiling stays off.
package profiling

import (
	"fmt"
	"os"

	"github.com/grafana/pyroscope-go"
)

// Config holds the runtime configuration for profiler initialization.
// ServerAddress empty disables the profiler. ApplicationName becomes the
// pyroscope "service" label; the suffix ".cpu" / ".inuse_space" / etc.
// is appended by the SDK per profile type.
type Config struct {
	// ApplicationName is the pyroscope app identifier, conventionally
	// "<service>" with no suffix. The SDK suffixes profile-type names.
	ApplicationName string
	// ServerAddress is the URL of the pyroscope ingest endpoint. Empty
	// disables profiling. Form: "http://host:port" (no trailing slash).
	ServerAddress string
	// AuthToken is the bearer token for Grafana Cloud Pyroscope. The
	// in-cluster homelab Pyroscope is unauthenticated, so this stays empty
	// in the homelab deployment.
	AuthToken string
	// TenantID is sent as the X-Scope-OrgID header for multi-tenant
	// Pyroscope deployments. Empty in single-tenant homelab.
	TenantID string
	// Tags is a closed-set label map appended to every profile. Init
	// merges in service / version / hostname automatically; Tags is for
	// operator-supplied extras (e.g. environment, region).
	Tags map[string]string
}

// NewConfig builds a Config from env vars. Reads:
//
//   - PYROSCOPE_SERVER_ADDRESS (required to enable; empty -> profiling off)
//   - PYROSCOPE_AUTH_TOKEN (optional)
//   - PYROSCOPE_TENANT_ID  (optional)
//   - DEPLOYMENT_ENV       (optional; becomes the "environment" tag)
//
// Application name is supplied by the caller so a binary cannot have its
// identity changed via env.
func NewConfig(applicationName string) Config {
	c := Config{
		ApplicationName: applicationName,
		ServerAddress:   os.Getenv("PYROSCOPE_SERVER_ADDRESS"),
		AuthToken:       os.Getenv("PYROSCOPE_AUTH_TOKEN"),
		TenantID:        os.Getenv("PYROSCOPE_TENANT_ID"),
		Tags:            map[string]string{},
	}
	if v := os.Getenv("DEPLOYMENT_ENV"); v != "" {
		c.Tags["environment"] = v
	}
	return c
}

// Stop is a closure returned by Init that flushes any buffered profiles
// and shuts the profiler down. Always call this on graceful exit. A nil
// return value indicates Init was a no-op.
type Stop func() error

// noop is the Stop returned when profiling is disabled. Calling it is a
// successful no-op so callers can defer it unconditionally.
func noop() error { return nil }

// Init starts the Pyroscope profiler with cfg. When ServerAddress is
// empty, Init is a no-op and returns a no-op stop closure. Otherwise it
// configures CPU + alloc + goroutine profiling and pushes to the
// configured server.
//
// CAUTION: Init must be called exactly once per process. Re-calling
// installs a second profiler; the first one's HTTP push goroutine is
// leaked.
func Init(cfg Config, version string) (Stop, error) {
	if cfg.ServerAddress == "" {
		return noop, nil
	}

	host, _ := os.Hostname() //nolint:errcheck // empty hostname acceptable
	tags := map[string]string{
		"service":  cfg.ApplicationName,
		"version":  version,
		"hostname": host,
	}
	for k, v := range cfg.Tags {
		// Allow operator-supplied tags to extend the closed set, but never
		// to overwrite the service / version / hostname trio that the
		// pyroscope dashboards expect.
		if _, reserved := tags[k]; reserved {
			continue
		}
		tags[k] = v
	}

	// Profile types: CPU, alloc objects, alloc space, inuse objects,
	// inuse space, goroutines.
	types := []pyroscope.ProfileType{
		pyroscope.ProfileCPU,
		pyroscope.ProfileAllocObjects,
		pyroscope.ProfileAllocSpace,
		pyroscope.ProfileInuseObjects,
		pyroscope.ProfileInuseSpace,
		pyroscope.ProfileGoroutines,
	}

	prof, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: cfg.ApplicationName,
		ServerAddress:   cfg.ServerAddress,
		AuthToken:       cfg.AuthToken,
		TenantID:        cfg.TenantID,
		Tags:            tags,
		ProfileTypes:    types,
		// Logger left nil: the SDK falls back to a stderr logger that's
		// noisier than slog. Pushing the logger through slog would mean
		// importing slog into this package; not worth the dep just for
		// pyroscope's three lines per minute of "remote not reachable"
		// when the cluster ingester is rolling.
	})
	if err != nil {
		return nil, fmt.Errorf("start pyroscope: %w", err)
	}

	return func() error {
		if err := prof.Stop(); err != nil {
			return fmt.Errorf("stop pyroscope: %w", err)
		}
		return nil
	}, nil
}
