/**
 * 02-dashboard.spec.ts — Dashboard golden path.
 *
 * Requires authenticated session (storageState from auth.setup.ts).
 * If no session was established the test detects the login redirect and skips.
 */

import { test, expect } from '@playwright/test';
import { isServerUp } from './helpers';

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
    await expect(page).toHaveTitle(/Dashboard|Digits/i);
  });

  test('dashboard shows stats grid', async ({ page }) => {
    await page.goto('/');
    if (page.url().includes('/auth/login')) {
      test.skip(true, 'No authenticated session');
      return;
    }
    if (page.url().includes('/onboard')) {
      test.skip(true, 'User needs onboarding first');
      return;
    }

    // Stats cards: Phones, Online, Active, Today
    const statsCards = page.locator('.grid > div');
    await expect(statsCards.first()).toBeVisible();

    // At least one stat label should be present
    const statsText = await page.locator('.grid').first().textContent();
    const hasStats = ['Lines', 'Online', 'Active Calls', 'Calls Today'].some(s =>
      statsText?.includes(s)
    );
    expect(hasStats).toBeTruthy();
  });

  test('dashboard shows active calls section', async ({ page }) => {
    await page.goto('/');
    if (page.url().includes('/auth/login') || page.url().includes('/onboard')) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const activeCalls = page.locator('text=Active Calls');
    await expect(activeCalls).toBeVisible();
  });

  test('dashboard shows lines section', async ({ page }) => {
    await page.goto('/');
    if (page.url().includes('/auth/login') || page.url().includes('/onboard')) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const linesSection = page.locator('h2', { hasText: 'Lines' });
    await expect(linesSection).toBeVisible();
  });

  test('sidebar navigation is visible', async ({ page }) => {
    await page.goto('/');
    if (page.url().includes('/auth/login') || page.url().includes('/onboard')) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Desktop sidebar should be rendered (even if hidden on mobile via CSS)
    const sidebar = page.locator('#sidebar');
    await expect(sidebar).toBeAttached();

    // Nav links for core sections (use sidebar-specific selectors to avoid
    // matching other links on the page that share the same href)
    await expect(page.locator('#sidebar a[href="/phones"]')).toBeAttached();
    await expect(page.locator('#sidebar a[href="/settings"]')).toBeAttached();
    await expect(page.locator('#sidebar a[href="/links"]')).toBeAttached();
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
