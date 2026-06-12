/**
 * 02-dashboard.spec.ts — Dashboard golden path.
 *
 * Requires authenticated session (storageState from auth.setup.ts).
 * If no session was established the test detects the login redirect and skips.
 */

import { test, expect } from '@playwright/test';
import { isServerUp, navLink } from './helpers';

test.beforeEach(async ({ page }, testInfo) => {
  // Skip if server is down
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

test.describe('Dashboard', () => {
  test('authenticated user sees dashboard, not login', async ({ page }) => {
    await page.goto('/');
    const url = page.url();

    if (url.includes('/auth/login') || url.includes('/onboard')) {
      test.skip(true, 'No authenticated session — skipping dashboard tests');
      return;
    }

    // Should be on dashboard
    await expect(page).toHaveURL('/');
    await expect(page).toHaveTitle(/Dashboard|Overview|Digits/i);
  });

  test('dashboard shows the rooms grid', async ({ page }) => {
    await page.goto('/');
    if (page.url().includes('/auth/login')) {
      test.skip(true, 'No authenticated session');
      return;
    }
    if (page.url().includes('/onboard')) {
      test.skip(true, 'User needs onboarding first');
      return;
    }

    // The intercom redesign replaced the KPI strip with a `.rooms` grid of
    // per-line cards (or a `.rooms__empty` pair-a-handset prompt when the
    // household has no lines yet). Assert the grid container renders.
    const rooms = page.locator('section.rooms').first();
    await expect(rooms).toBeVisible();
  });

  test('dashboard heading shows the household name (page__title)', async ({ page }) => {
    await page.goto('/');
    if (page.url().includes('/auth/login') || page.url().includes('/onboard')) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // The redesigned dashboard uses .page__title for the h1. The content is
    // the household name (falling back to "Overview" when unset), and the
    // e2e setup creates "E2E Test Family", so we just assert the h1 is
    // visible with some non-empty text.
    const title = page.locator('.page__title').first();
    await expect(title).toBeVisible();
    const text = await title.textContent();
    expect((text ?? '').trim().length).toBeGreaterThan(0);
  });

  test('page chrome has nav links for core sections', async ({ page }) => {
    await page.goto('/');
    if (page.url().includes('/auth/login') || page.url().includes('/onboard')) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Both layouts (v2 rail, dialup channels/toolbar) expose anchors to the
    // core pages in the page chrome. Use the layout-agnostic navLink helper
    // so this test survives a theme switch. /phones is no longer a nav item
    // (it merged into Overview), so it is not in this list.
    for (const href of ['/', '/links', '/settings']) {
      await expect(navLink(page, href).first()).toBeAttached();
    }
  });

  test('unauthenticated visit to / redirects to login', async ({ browser }) => {
    // Use a fresh context with no cookies
    const freshCtx = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    const freshPage = await freshCtx.newPage();

    await freshPage.goto('/');
    expect(freshPage.url()).toContain('/auth/login');
    await freshCtx.close();
  });
});
