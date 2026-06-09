/**
 * 10-firmware-changelog.spec.ts: Firmware update chip and pair banner.
 *
 * Tests:
 *   - Pair confirmation banner appears when arriving on Overview ("/") with the
 *     ?paired= param (POST /phones/pair now redirects to /?paired=...)
 *   - Banner dismisses via the close button
 *   - Update chip is visible and expands to show release notes for a behind
 *     device, on the Overview line cards
 *   - Chip collapses on second click
 *
 * The chip tests require:
 *   - TEST_FAKE_UPDATES=1 on the server (fake release index: latest fw 1.4.0)
 *   - DEV_MODE=true on the server (enables POST /dev/seed-firmware)
 *
 * Both flags are set by the e2e-ci Makefile target. If the server is running
 * without them the chip test is skipped gracefully.
 */

import { test, expect, Page } from '@playwright/test';
import { isServerUp, BASE_URL } from './helpers';

const OLD_FW = '1.2.0';

test.beforeEach(async ({}, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

function isAuthOrOnboard(url: string) {
  return url.includes('/auth/login') || url.includes('/onboard');
}

// ---------------------------------------------------------------------------
// Pair confirmation banner
// ---------------------------------------------------------------------------

test.describe('Pair confirmation banner', () => {
  test('banner appears with line name and fw version', async ({ page }) => {
    await page.goto('/?paired=Kitchen&fw=1.4.0');

    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session');
      return;
    }

    const banner = page.locator('.pair-banner');
    await expect(banner).toBeVisible();
    await expect(banner).toContainText('Kitchen');
    await expect(banner).toContainText('fw 1.4.0');
  });

  test('banner dismisses when close button is clicked', async ({ page }) => {
    await page.goto('/?paired=Kitchen&fw=1.4.0');

    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session');
      return;
    }

    const banner = page.locator('.pair-banner');
    await expect(banner).toBeVisible();

    await banner.locator('.pair-banner__close').click();
    await expect(banner).not.toBeVisible();
  });

  test('banner renders without fw version when fw param is absent', async ({ page }) => {
    await page.goto('/?paired=Kitchen');

    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session');
      return;
    }

    const banner = page.locator('.pair-banner');
    await expect(banner).toBeVisible();
    await expect(banner).toContainText('Kitchen');
    // No "fw" text should appear when the fw param is missing.
    const text = await banner.textContent();
    expect(text).not.toMatch(/fw\s+\d/);
  });
});

// ---------------------------------------------------------------------------
// Firmware update chip
// ---------------------------------------------------------------------------

/**
 * Check whether the server has the fake release index loaded.
 * Hits GET /api/updates/releases and looks for fw 1.4.0 as latest.
 */
async function hasFakeReleaseIndex(): Promise<boolean> {
  try {
    const ctrl = new AbortController();
    const id = setTimeout(() => ctrl.abort(), 3000);
    const res = await fetch(`${BASE_URL}/api/updates/releases`, { signal: ctrl.signal });
    clearTimeout(id);
    if (!res.ok) return false;
    const body = await res.json() as { firmware?: { latest?: string } };
    return body?.firmware?.latest === '1.4.0';
  } catch {
    return false;
  }
}

/**
 * Read an existing line number off the Overview line cards. The legacy POST
 * /phones add-without-pairing endpoint is gone, so we no longer create a line;
 * we reuse one the dev household already has and mark its firmware old via
 * POST /dev/seed-firmware. Returns the 7-digit number, or null when no line
 * exists or seeding fails.
 */
async function seedOutdatedHandset(page: Page): Promise<string | null> {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    return null;
  }
  const href = await page
    .locator('a.rooms__card[href^="/phones/"]')
    .first()
    .getAttribute('href')
    .catch(() => null);
  if (!href) {
    return null;
  }
  // href is /phones/<number>; pull the trailing path segment.
  const number = href.split('/').pop() ?? '';
  if (!number) {
    return null;
  }

  // Register a fake hub connection at the old firmware version. The hub key
  // matches the number stored in the DB (7-digit local format).
  const seedResp = await page.request.post(
    `/dev/seed-firmware?number=${encodeURIComponent(number)}&fw=${OLD_FW}`,
  );
  if (!seedResp.ok()) {
    return null;
  }

  return number;
}

test.describe('Firmware update chip', () => {
  test('update chip expands to show release notes', async ({ page }) => {
    if (!(await hasFakeReleaseIndex())) {
      test.skip(true, 'Server does not have fake release index (TEST_FAKE_UPDATES=1 not set)');
      return;
    }

    // Seed an existing line at the old firmware version.
    const number = await seedOutdatedHandset(page);
    if (!number) {
      test.skip(true, 'No line to seed (DEV_MODE off or empty household)');
      return;
    }

    // Reload Overview so the server picks up the freshly seeded hub entry.
    await page.goto('/');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session');
      return;
    }

    // The update chip summary should be visible for at least one card.
    const summary = page.locator('.update-details > summary').first();
    await expect(summary).toBeVisible({ timeout: 5000 });

    // Notes are hidden before the chip is opened.
    const notes = page.locator('.update-details .update-notes').first();
    await expect(notes).toBeHidden();

    // Click to expand.
    await summary.click();

    // At least one update-note article is visible.
    const firstNote = page.locator('.update-details[open] .update-notes .update-note').first();
    await expect(firstNote).toBeVisible();
  });

  test('update chip collapses on second click', async ({ page }) => {
    if (!(await hasFakeReleaseIndex())) {
      test.skip(true, 'Server does not have fake release index (TEST_FAKE_UPDATES=1 not set)');
      return;
    }

    const number = await seedOutdatedHandset(page);
    if (!number) {
      test.skip(true, 'No line to seed (DEV_MODE off or empty household)');
      return;
    }

    await page.goto('/');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session');
      return;
    }

    const summary = page.locator('.update-details > summary').first();
    await expect(summary).toBeVisible({ timeout: 5000 });

    // Open then close.
    await summary.click();
    await expect(page.locator('.update-details[open]').first()).toBeVisible();

    await summary.click();
    await expect(page.locator('.update-details[open]')).toHaveCount(0);
  });
});
