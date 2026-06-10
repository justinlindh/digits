/**
 * 09-silent-mode.spec.ts: Per-phone silent-mode toggle (issue #217).
 *
 * Tests:
 *   - Ringer panel renders on phone detail page
 *   - Toggling silent on via the checkbox updates the partial inline (htmx swap)
 *     and persists across reload
 *   - The Overview line cards show the silent badge for silent phones, not for
 *     others (the line roster moved from /phones to Overview)
 *   - Toggling silent off removes the badge
 *   - Same flow under the dialup theme
 *
 * Skips if the test household has no paired phones.
 */

import { test, expect, Page } from '@playwright/test';
import { isServerUp, setTheme } from './helpers';

test.beforeEach(async ({ page }, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

function isAuthOrOnboard(url: string) {
  return url.includes('/auth/login') || url.includes('/onboard');
}

/**
 * Returns the href of the first line card on Overview, or null when the
 * household has none. The caller should skip in the null case. The line roster
 * lives on Overview now, not on /phones (which is pairing-only).
 */
async function firstPhoneHref(page: Page): Promise<string | null> {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    return null;
  }
  const link = page.locator('a[href^="/phones/"]:not([href="/phones"])').first();
  if ((await link.count()) === 0) {
    return null;
  }
  return await link.getAttribute('href');
}

/**
 * Sets the silent-mode flag on the line currently shown by the detail page.
 * The form auto-submits on change via htmx. Waits for the POST response so the
 * partial swap is settled before returning.
 */
async function setSilentOnDetailPage(page: Page, target: 'on' | 'off') {
  const checkbox = page.locator('#silent-mode-section input[name="silent_mode"]');
  await expect(checkbox).toBeVisible();
  const checked = await checkbox.isChecked();
  if ((target === 'on' && checked) || (target === 'off' && !checked)) {
    return; // already in desired state, do not bounce a no-op POST
  }
  const wait = page.waitForResponse(
    (r) => r.url().includes('/silent-mode') && r.request().method() === 'POST',
  );
  await checkbox.click();
  await wait;
}

test.describe('Silent mode (intercom theme)', () => {
  test.beforeEach(async ({ page }) => {
    await setTheme(page.request, 'intercom').catch(() => undefined);
  });

  test('ringer panel renders on phone detail page', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    await expect(page.locator('h2.panel__title', { hasText: /ringer/i })).toBeVisible();
    await expect(page.locator('#silent-mode-section')).toBeVisible();
    await expect(page.locator('#silent-mode-section input[name="silent_mode"]')).toBeVisible();
    await expect(
      page.locator('#silent-mode-section', { hasText: /the light still flashes/i }),
    ).toBeVisible();
  });

  test('toggling silent on persists and shows the Overview card badge', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }

    await page.goto(href);
    await setSilentOnDetailPage(page, 'off'); // baseline
    await setSilentOnDetailPage(page, 'on');

    // Reload: the swap state must be persisted server-side.
    await page.goto(href);
    await expect(
      page.locator('#silent-mode-section input[name="silent_mode"]'),
    ).toBeChecked();

    // Badge appears on the Overview line card.
    await page.goto('/');
    const card = page.locator(`a.rooms__card[href="${href}"]`);
    await expect(card.locator('.rooms__badges .phone-silent').first()).toBeVisible();

    // Toggle off and confirm badge disappears.
    await page.goto(href);
    await setSilentOnDetailPage(page, 'off');
    await page.goto('/');
    const offCard = page.locator(`a.rooms__card[href="${href}"]`);
    await expect(offCard.locator('.phone-silent')).toHaveCount(0);
  });

  test('card badge is absent when silent is off across all rows', async ({ page }) => {
    // After cleanup from the prior test the line is silent=off. This test
    // pins the negative case explicitly, so a regression that always emits
    // the badge is caught.
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);
    await setSilentOnDetailPage(page, 'off');
    await page.goto('/');
    await expect(page.locator('.rooms .phone-silent')).toHaveCount(0);
  });
});

test.describe('Silent mode (dialup theme)', () => {
  test.beforeEach(async ({ page }) => {
    try {
      await setTheme(page.request, 'dialup');
    } catch (e) {
      test.skip(true, `setTheme failed: ${(e as Error).message}`);
    }
  });

  test.afterEach(async ({ page }) => {
    await setTheme(page.request, 'intercom').catch(() => undefined);
  });

  test('ringer panel and toggle work the same under dialup', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }

    await page.goto(href);
    await expect(page.locator('#silent-mode-section')).toBeVisible();
    await setSilentOnDetailPage(page, 'on');

    await page.goto('/');
    const card = page.locator(`a.rooms__card[href="${href}"]`);
    const badge = card.locator('.phone-silent').first();
    await expect(badge).toBeVisible();
    // Dialup shows the SILENT label; intercom hides it via display:none.
    await expect(card.locator('.phone-silent__text')).toBeVisible();

    // Restore baseline so the next theme block starts clean.
    await page.goto(href);
    await setSilentOnDetailPage(page, 'off');
  });
});
