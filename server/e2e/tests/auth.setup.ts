/**
 * auth.setup.ts — Playwright setup step that creates an authenticated session.
 *
 * Strategy:
 *   1. If E2E_SESSION_COOKIE is set, inject it directly (fastest, no UI needed).
 *   2. Otherwise, use the magic-link flow in devMode: POST /auth/magic with a
 *      test email, then poll the server logs for the token (devMode logs the URL
 *      to stdout) — not easily automatable externally.
 *   3. Fallback: write an empty storageState so dependant tests still run and
 *      individually detect the missing auth.
 *
 * The recommended approach for local development is:
 *   1. Start the server in devMode.
 *   2. POST /auth/magic → the server logs the magic link URL.
 *   3. Copy the token and set E2E_MAGIC_TOKEN=<token> before running tests.
 *
 * Environment variables:
 *   E2E_SESSION_COOKIE  — raw value of the `digits_session` cookie (skip UI login)
 *   E2E_MAGIC_TOKEN     — raw magic link token to redeem (we GET /auth/magic/<token>)
 *   E2E_EMAIL           — email address to use (default: e2e@example.com)
 *   BASE_URL            — server base URL (default: http://localhost:8080)
 */

import { test as setup, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { BASE_URL, isServerUp } from './helpers';

const AUTH_FILE = path.join(__dirname, '../.auth/user.json');

setup('authenticate', async ({ page, context }) => {
  // Check server availability
  const up = await isServerUp();
  if (!up) {
    console.warn('[setup] Dev server not reachable — writing empty auth state. Tests will skip.');
    writeEmptyAuth();
    return;
  }

  // ── Strategy 1: inject a known session cookie ─────────────────────────────
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
      console.log('[setup] Session cookie accepted — auth state saved.');
      return;
    }
    console.warn('[setup] Session cookie was rejected — falling back.');
  }

  // ── Strategy 2: redeem a magic link token ────────────────────────────────
  const magicToken = process.env.E2E_MAGIC_TOKEN;
  if (magicToken) {
    console.log(`[setup] Redeeming magic link token: ${magicToken.substring(0, 8)}...`);
    await page.goto(`/auth/magic/${magicToken}`);
    await page.waitForURL(url => !url.toString().includes('/auth/magic'), { timeout: 8000 });
    if (!page.url().includes('/auth/login')) {
      await context.storageState({ path: AUTH_FILE });
      console.log(`[setup] Magic link redeemed — auth state saved. Landing: ${page.url()}`);
      return;
    }
    console.warn('[setup] Magic link was invalid or expired — falling back.');
  }

  // ── Strategy 3: request a magic link and redeem it if the server returns it
  //    (only works when devMode=true: the server logs the link in HTTP response
  //    as a JSON dev payload, or we can check the login redirect message)
  const email = process.env.E2E_EMAIL || 'e2e@example.com';
  console.log(`[setup] Requesting magic link for ${email} (devMode needed)`);

  // Submit the magic link form
  await page.goto('/auth/login');
  await page.fill('input[type="email"]', email);

  // Intercept the redirect to capture any dev-mode token echoed in the URL
  const [response] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/auth/magic') && r.request().method() === 'POST'),
    page.click('button[type="submit"]'),
  ]);

  // In devMode the server logs "dev magic link" to stdout — we can't intercept
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
