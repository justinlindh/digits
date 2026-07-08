# Digits E2E Tests

Playwright-based end-to-end tests for the Digits web application.

## Quick start

```bash
# From repo root
make e2e

# Or from this directory
npm ci
npx playwright install --with-deps chromium
npx playwright test
```

## Authentication

The tests use Playwright's [storageState](https://playwright.dev/docs/auth) to share
an authenticated session between test files. The `auth.setup.ts` project runs first
and writes `.auth/user.json` (gitignored).

### Option A: Session cookie (fastest)

If you have a valid `digits_session` cookie from a running dev server:

```bash
E2E_SESSION_COOKIE=<cookie-value> make e2e
```

### Option B: Magic link token

1. Start the server with `DEV_MODE=true` (it logs magic link URLs to stdout).
2. POST a magic link request:
   ```bash
   curl -s -X POST http://localhost:8080/auth/magic \
     -d 'email=e2e@example.com'
   ```
3. Grab the token from the server log output.
4. Run tests:
   ```bash
   E2E_MAGIC_TOKEN=<token> make e2e
   ```

### Option C: No auth (public pages only)

Run without any auth env vars. Tests that require an authenticated session
will skip themselves gracefully. Public-page tests (login page, healthz) still run.

## Environment variables

| Variable              | Default                  | Description                         |
|-----------------------|--------------------------|-------------------------------------|
| `BASE_URL`            | `http://localhost:8080`  | Target server URL                   |
| `E2E_SESSION_COOKIE`  | *(unset)*                | Inject a known `digits_session` cookie |
| `E2E_MAGIC_TOKEN`     | *(unset)*                | Redeem a magic-link token for auth  |
| `E2E_EMAIL`           | `e2e@example.com`        | Email used in magic-link requests   |

## Test structure

```
tests/
  helpers.ts              # Shared utilities (isServerUp, requireServer, etc.)
  auth.setup.ts           # Playwright setup: establishes authenticated session
  NN-<feature>.spec.ts    # One spec per feature area, numbered by rough order
```

Specs are named `NN-<feature>.spec.ts`, one per feature area (login, dashboard,
onboarding, phones, settings, links, navigation, theme, silent mode, live
calls, do-not-disturb, firmware changelog, voicemail, conference). Run `ls
tests/` for the current set rather than maintaining a list here.

## Skipping behaviour

All tests call `isServerUp()` in `beforeEach` and skip when the dev server is
unreachable. Tests that need auth skip when the session is missing. This means
the suite always exits cleanly regardless of environment.
