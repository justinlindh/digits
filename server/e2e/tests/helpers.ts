/**
 * helpers.ts — shared utilities for Digits E2E tests.
 */

import { APIRequestContext, Locator, Page, expect } from '@playwright/test';

export const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

/**
 * Probe whether the dev server is reachable.
 * Returns true if /healthz responds with 2xx within a short timeout.
 */
export async function isServerUp(): Promise<boolean> {
  try {
    const ctrl = new AbortController();
    const id = setTimeout(() => ctrl.abort(), 3000);
    const res = await fetch(`${BASE_URL}/healthz`, { signal: ctrl.signal });
    clearTimeout(id);
    return res.ok;
  } catch {
    return false;
  }
}

/**
 * Skip the current test if the server is not reachable.
 * Call at the top of tests that require a live server.
 */
export async function requireServer(testInfo: { skip: (condition: boolean, description: string) => void }) {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server is not running — skipping (set BASE_URL or start server)');
}

/**
 * Navigate to a page and assert the response was not a redirect to login.
 * Returns true if the page loaded, false if we ended up on /auth/login.
 */
export async function navigateTo(page: Page, path: string): Promise<boolean> {
  await page.goto(path);
  const url = page.url();
  return !url.includes('/auth/login');
}

/**
 * Theme identifiers. Match auth.ThemeIntercom / auth.ThemeDialup in the server.
 */
export type Theme = 'intercom' | 'dialup';

/**
 * Layout identifiers, one per layout template.
 *   'v2'     -> layout-v2.html (top rail, brass accent) used by theme 'intercom'
 *   'dialup' -> layout-dialup.html (channels sidebar) used by theme 'dialup'
 */
export type Layout = 'v2' | 'dialup';

/**
 * Sniff which layout template rendered the current page by looking for the
 * layout-specific root element. Returns the active layout.
 */
export async function getLayout(page: Page): Promise<Layout> {
  const dialup = await page.locator('.dialup-window').count();
  if (dialup > 0) return 'dialup';
  return 'v2';
}

/**
 * Map a theme value to the layout it renders with.
 */
export function layoutForTheme(theme: Theme): Layout {
  return theme === 'dialup' ? 'dialup' : 'v2';
}

/**
 * Locate a nav link to the given href in the page chrome (header / sidebar),
 * regardless of which layout is active.
 *   v2:     <header class="rail"> .rail__nav a[href=...]
 *   dialup: .dialup-toolbar a[href=...] and .dialup-channels a[href=...]
 *
 * The returned locator matches any of those, so assertions work in either
 * layout. Use .first() if you need a single element (e.g. for clicks).
 */
export function navLink(page: Page, href: string): Locator {
  return page.locator(
    `.rail__nav a[href="${href}"], .dialup-channels a[href="${href}"], .dialup-toolbar a[href="${href}"]`,
  );
}

/**
 * Locate the active nav link. Both layouts mark it with `.is-active`.
 */
export function activeNavLink(page: Page): Locator {
  return page.locator('.rail__nav a.is-active, .dialup-channels a.is-active');
}

/**
 * Change the logged-in user's theme via the same form-post the settings UI
 * uses. Requires an authenticated request context (pass page.request).
 */
export async function setTheme(request: APIRequestContext, theme: Theme): Promise<void> {
  const resp = await request.post('/settings/theme', {
    form: { theme },
    maxRedirects: 0,
  });
  // Expect a 303 See Other redirect to /settings?saved=1
  if (resp.status() !== 303 && resp.status() !== 302) {
    throw new Error(`setTheme: expected redirect, got ${resp.status()}`);
  }
}

/**
 * Assert that a nav link with the given label is active.
 * Works in both layouts (looks for .is-active in rail or channels).
 */
export async function expectNavActive(page: Page, labelPattern: RegExp) {
  const link = activeNavLink(page);
  await expect(link).toBeVisible();
  const text = await link.textContent();
  expect(text ?? '').toMatch(labelPattern);
}
