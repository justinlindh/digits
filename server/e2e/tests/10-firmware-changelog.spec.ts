/**
 * 10-firmware-changelog.spec.ts -- Firmware update chip and pair banner (issue #TBD).
 *
 * Tests:
 *   - Pair confirmation banner appears when arriving on /phones with ?paired= param
 *   - Banner dismisses via the close button
 *   - Update chip is visible and expands to show release notes for a behind device
 *   - Chip collapses on second click
 *
 * The chip tests require:
 *   - TEST_FAKE_UPDATES=1 on the server (fake release index: latest fw 1.4.0)
 *   - DEV_MODE=true on the server (enables POST /dev/seed-firmware and POST /phones direct add)
 *
 * Both flags are set by the e2e-ci Makefile target. If the server is running
 * without them the chip test is skipped gracefully.
 */

import { test, expect, Page } from '@playwright/test';
import { isServerUp, BASE_URL } from './helpers';

// 7-digit local number as used by the Digits network.
const TEST_LINE_NUMBER = '5559001';
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
    await page.goto('/phones?paired=Kitchen&fw=1.4.0');

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
    await page.goto('/phones?paired=Kitchen&fw=1.4.0');

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
    await page.goto('/phones?paired=Kitchen');

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
 * Seed a line in the DB via the direct POST /phones endpoint, then register
 * a fake hub entry at the given firmware version via POST /dev/seed-firmware.
 * Returns the 7-digit number that was seeded, or null on failure.
 */
async function seedOutdatedHandset(page: Page): Promise<string | null> {
  // Create the line record (requires auth session). The form expects a
  // 7-digit local number (Digits network convention).
  const addResp = await page.request.post('/phones', {
    form: {
      number: TEST_LINE_NUMBER,
      name: 'E2E Test Handset',
    },
    maxRedirects: 0,
  });
  // Accept 200 (line added or already exists) or 303 (redirect after success).
  if (addResp.status() >= 500) {
    return null;
  }

  // Register a fake hub connection at the old firmware version. The hub key
  // matches the number stored in the DB (7-digit local format).
  const seedResp = await page.request.post(
    `/dev/seed-firmware?number=${encodeURIComponent(TEST_LINE_NUMBER)}&fw=${OLD_FW}`,
  );
  if (!seedResp.ok()) {
    return null;
  }

  return TEST_LINE_NUMBER;
}

test.describe('Firmware update chip', () => {
  // Clean up the seeded test line after each chip test so it does not
  // interfere with tests in later spec files that look for phones.
  test.afterEach(async ({ page }) => {
    await page.request.post(`/phones/${TEST_LINE_NUMBER}/delete`).catch(() => undefined);
  });

  test('update chip expands to show release notes', async ({ page }) => {
    if (!(await hasFakeReleaseIndex())) {
      test.skip(true, 'Server does not have fake release index (TEST_FAKE_UPDATES=1 not set)');
      return;
    }

    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session');
      return;
    }

    // Seed a handset at the old firmware version.
    const number = await seedOutdatedHandset(page);
    if (!number) {
      test.skip(true, 'Could not seed test handset -- DEV_MODE may not be enabled');
      return;
    }

    // Reload so the server picks up the freshly seeded hub entry.
    await page.reload();

    // The update chip summary should be visible for at least one row.
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

    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session');
      return;
    }

    const number = await seedOutdatedHandset(page);
    if (!number) {
      test.skip(true, 'Could not seed test handset -- DEV_MODE may not be enabled');
      return;
    }

    await page.reload();

    const summary = page.locator('.update-details > summary').first();
    await expect(summary).toBeVisible({ timeout: 5000 });

    // Open then close.
    await summary.click();
    await expect(page.locator('.update-details[open]').first()).toBeVisible();

    await summary.click();
    await expect(page.locator('.update-details[open]')).toHaveCount(0);
  });
});
