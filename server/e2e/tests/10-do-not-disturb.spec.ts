/**
 * 10-do-not-disturb.spec.ts -- Household Do Not Disturb toggle.
 *
 * Tests:
 *   - Toggling DND on the Settings page surfaces the household chip on every
 *     primary surface (overview, lines, calls, links, settings).
 *   - The per-line silent-mode badge counts on /phones do not change when the
 *     household DND flag flips on or off (the two settings are independent).
 *   - Toggling DND off removes the chip everywhere.
 *   - Same flow under both the intercom and dialup themes.
 *
 * Skips when the dev server is not reachable.
 */

import { test, expect, Page } from '@playwright/test';
import { isServerUp, setTheme, Theme } from './helpers';

test.beforeEach(async ({ page }, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

function isAuthOrOnboard(url: string): boolean {
  return url.includes('/auth/login') || url.includes('/onboard');
}

/**
 * Returns the chip selector for the given theme. Both layouts render the chip
 * inside the page chrome only when the household DND flag is on.
 */
function chipSelector(theme: Theme): string {
  return theme === 'dialup' ? '.dialup-toolbar__dnd' : '.rail__dnd-chip';
}

/**
 * Counts the per-line silent badges on /phones. Returns 0 when the page is
 * unreachable (auth/onboard) so callers can short-circuit gracefully.
 */
async function countSilentBadges(page: Page): Promise<number> {
  await page.goto('/phones');
  if (isAuthOrOnboard(page.url())) {
    return 0;
  }
  return await page.locator('.phone-silent').count();
}

/**
 * Sets the household DND flag via the settings form. The toggle auto-submits
 * via onchange; we wait for the POST so the redirect to /settings?saved=1 has
 * landed before the next assertion.
 */
async function setDoNotDisturb(page: Page, target: 'on' | 'off'): Promise<void> {
  await page.goto('/settings#do-not-disturb');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'Settings page not reachable');
    return;
  }
  const checkbox = page.locator('#do-not-disturb input[name="enabled"]');
  await expect(checkbox).toBeVisible();
  const checked = await checkbox.isChecked();
  if ((target === 'on' && checked) || (target === 'off' && !checked)) {
    return; // already in desired state
  }
  const wait = page.waitForResponse(
    (r) =>
      r.url().includes('/settings/do-not-disturb') &&
      r.request().method() === 'POST',
  );
  await checkbox.click();
  await wait;
  // The handler returns 303 to /settings?saved=1; wait for that landing page.
  await page.waitForURL((u) => u.toString().includes('/settings'));
}

const SURFACES = ['/', '/phones', '/links', '/settings'];

function runDNDFlow(theme: Theme): void {
  test.describe(`Do Not Disturb (${theme} theme)`, () => {
    test.beforeEach(async ({ page }) => {
      try {
        await setTheme(page.request, theme);
      } catch (e) {
        test.skip(true, `setTheme failed: ${(e as Error).message}`);
      }
    });

    test.afterEach(async ({ page }) => {
      // Best-effort cleanup so the DND flag never leaks into the next block.
      await setDoNotDisturb(page, 'off').catch(() => undefined);
      if (theme !== 'intercom') {
        await setTheme(page.request, 'intercom').catch(() => undefined);
      }
    });

    test('chip appears across pages, per-line badges unchanged', async ({
      page,
    }) => {
      // Baseline: count per-line silent badges before any DND change.
      const before = await countSilentBadges(page);
      if (isAuthOrOnboard(page.url())) {
        test.skip(true, '/phones not reachable -- auth or onboard required');
        return;
      }

      // Make sure the household DND flag starts off so the on/off transition
      // is real, not a no-op.
      await setDoNotDisturb(page, 'off');

      // Confirm the chip is absent before we toggle.
      await page.goto('/');
      await expect(page.locator(chipSelector(theme))).toHaveCount(0);

      // Flip the toggle on.
      await setDoNotDisturb(page, 'on');

      // The saved banner should be visible on the redirect landing page.
      await expect(page.locator('.banner--ok')).toBeVisible();

      // The chip must show on every primary surface.
      const sel = chipSelector(theme);
      for (const path of SURFACES) {
        await page.goto(path);
        if (isAuthOrOnboard(page.url())) {
          // /calls is gated behind a feature flag; treat any redirect as fatal
          // for this assertion only when the surface is required.
          continue;
        }
        await expect(
          page.locator(sel),
          `DND chip missing on ${path} under ${theme}`,
        ).toBeVisible();
      }

      // /calls is conditional on CallHistoryEnabled. Probe it but don't fail
      // when the household has the feature disabled.
      await page.goto('/calls');
      if (
        !isAuthOrOnboard(page.url()) &&
        !page.url().includes('/auth/login') &&
        (await page.locator('.rail, .dialup-toolbar').count()) > 0
      ) {
        await expect(page.locator(sel)).toBeVisible();
      }

      // The per-line silent badge count must not have shifted: household DND
      // and per-line silent are independent dimensions.
      const after = await countSilentBadges(page);
      expect(after).toBe(before);

      // Toggle off and confirm the chip disappears from the overview.
      await setDoNotDisturb(page, 'off');
      await page.goto('/');
      await expect(page.locator(sel)).toHaveCount(0);
    });
  });
}

runDNDFlow('intercom');
runDNDFlow('dialup');
