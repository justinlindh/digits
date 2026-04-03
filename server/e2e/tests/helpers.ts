/**
 * helpers.ts — shared utilities for Digits E2E tests.
 */

import { Page, expect } from '@playwright/test';

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
 * Assert that a nav link with the given label is active (has the `active` CSS class).
 */
export async function expectNavActive(page: Page, label: string) {
  const link = page.locator('.nav-link.active', { hasText: label });
  await expect(link).toBeVisible();
}
