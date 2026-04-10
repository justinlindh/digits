/**
 * auth.setup.ts -- Playwright setup step that creates an authenticated session.
 *
 * Strategy (tried in order, first success wins):
 *   0. Hit GET /auth/dev-session?email=<email>. When the server runs with
 *      DEV_MODE=true this creates a user+session, sets the cookie, and
 *      redirects to /. Returns 404 when dev mode is off.
 *   1. If E2E_SESSION_COOKIE is set, inject it directly (fastest, no UI needed).
 *   2. If E2E_MAGIC_TOKEN is set, redeem it via GET /auth/magic/<token>.
 *   3. Request a magic link via the login form (needs devMode).
 *   4. Fallback: write an empty storageState so dependent tests still run and
 *      individually detect the missing auth.
 *
 * The recommended approach for CI is to run the server with DEV_MODE=true so
 * Strategy 0 handles auth automatically with no extra env vars.
 *
 * Environment variables:
 *   E2E_SESSION_COOKIE  -- raw value of the `digits_session` cookie (skip UI login)
 *   E2E_MAGIC_TOKEN     -- raw magic link token to redeem (we GET /auth/magic/<token>)
 *   E2E_EMAIL           -- email address to use (default: e2e@example.com)
 *   BASE_URL            -- server base URL (default: http://localhost:8080)
 */

import { test as setup, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { BASE_URL, isServerUp } from './helpers';

const AUTH_FILE = path.join(__dirname, '../.auth/user.json');

setup('authenticate', async ({ page, context }) => {
  const email = process.env.E2E_EMAIL || 'e2e@example.com';

  // Check server availability
  const up = await isServerUp();
  if (!up) {
    console.warn('[setup] Dev server not reachable -- writing empty auth state. Tests will skip.');
    writeEmptyAuth();
    return;
  }

  // -- Strategy 0: dev-session endpoint (DEV_MODE=true on the server) --------
  console.log(`[setup] Trying dev-session endpoint for ${email}`);
  const devSessionUrl = `/auth/dev-session?email=${encodeURIComponent(email)}`;
  const devResp = await page.goto(devSessionUrl);
  const devStatus = devResp?.status() ?? 0;
  if (devStatus !== 404 && !page.url().includes('/auth/login')) {
    await context.storageState({ path: AUTH_FILE });
    console.log('[setup] Dev-session endpoint succeeded -- auth state saved.');
    return;
  }
  console.log('[setup] Dev-session endpoint not available -- falling through.');

  // -- Strategy 1: inject a known session cookie ------------------------------
  const sessionCookie = process.env.E2E_SESSION_COOKIE;
  if (sessionCookie) {
    console.log('[setup] Using E2E_SESSION_COOKIE');
    await context.addCookies([{
      name: 'digits_session',
      value: sessionCookie,
      domain: new URL(BASE_URL).hostname,
      path: '/',
      httpOnly: true,
      secure: false,
      sameSite: 'Lax',
    }]);
    await page.goto('/');
    if (!page.url().includes('/auth/login')) {
      await context.storageState({ path: AUTH_FILE });
      console.log('[setup] Session cookie accepted -- auth state saved.');
      return;
    }
    console.warn('[setup] Session cookie was rejected -- falling back.');
  }

  // -- Strategy 2: redeem a magic link token ---------------------------------
  const magicToken = process.env.E2E_MAGIC_TOKEN;
  if (magicToken) {
    console.log(`[setup] Redeeming magic link token: ${magicToken.substring(0, 8)}...`);
    await page.goto(`/auth/magic/${magicToken}`);
    await page.waitForURL(url => !url.toString().includes('/auth/magic'), { timeout: 8000 });
    if (!page.url().includes('/auth/login')) {
      await context.storageState({ path: AUTH_FILE });
      console.log(`[setup] Magic link redeemed -- auth state saved. Landing: ${page.url()}`);
      return;
    }
    console.warn('[setup] Magic link was invalid or expired -- falling back.');
  }

  // -- Strategy 3: request a magic link and redeem it if the server returns it
  //    (only works when devMode=true: the server logs the link in HTTP response
  //    as a JSON dev payload, or we can check the login redirect message)
  console.log(`[setup] Requesting magic link for ${email} (devMode needed)`);

  // Submit the magic link form
  await page.goto('/auth/login');
  await page.fill('input[type="email"]', email);

  // Intercept the redirect to capture any dev-mode token echoed in the URL
  const [response] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/auth/magic') && r.request().method() === 'POST'),
    page.click('button[type="submit"]'),
  ]);

  // In devMode the server logs "dev magic link" to stdout -- we can't intercept
  // that from here without a helper endpoint. Write an empty state and let
  // dependent tests skip via requireServer().
  console.warn(
    '[setup] No E2E_SESSION_COOKIE or E2E_MAGIC_TOKEN provided. ' +
    'Tests requiring auth will detect missing session and skip. ' +
    'To authenticate: start server with DEV_MODE=true, run `make e2e-token EMAIL=e2e@example.com` ' +
    'to get the token, then re-run with E2E_MAGIC_TOKEN=<token>.'
  );
  writeEmptyAuth();
});

function writeEmptyAuth() {
  const emptyState = { cookies: [], origins: [] };
  fs.mkdirSync(path.dirname(AUTH_FILE), { recursive: true });
  fs.writeFileSync(AUTH_FILE, JSON.stringify(emptyState, null, 2));
}
