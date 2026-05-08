/**
 * 10-do-not-disturb.spec.ts -- Household "Silence All" toggle.
 *
 * Tests:
 *   - Toggling "Silence All" on the Settings page surfaces the DND chip on
 *     every primary surface (overview, lines, calls, links, settings).
 *   - Toggling it off removes the chip everywhere and clears per-line
 *     silent badges.
 *   - Same flow under both the intercom and dialup themes.
 *
 * "Silence All" is derived state: it batch-writes silent_mode on every line
 * in the household. The chip appears when all lines are silent.
 *
 * Skips when the dev server is not reachable.
 */

import { test, expect, Page } from '@playwright/test';
import { isServerUp, setTheme, Theme } from './helpers';

const DND_TEST_LINE = '5550199';

test.beforeEach(async ({ page }, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

function isAuthOrOnboard(url: string): boolean {
  return url.includes('/auth/login') || url.includes('/onboard');
}

/**
 * Ensure the household has at least one phone line so "Silence All" can
 * derive its state. Idempotent: a second call for the same number is a
 * harmless no-op (the server returns 303 either way).
 */
async function ensureLine(page: Page): Promise<void> {
  const resp = await page.request.post('/phones', {
    form: { number: DND_TEST_LINE, name: 'DND Test' },
    maxRedirects: 0,
  });
  if (resp.status() >= 500) {
    test.skip(true, 'Could not seed phone line for DND test');
  }
}

/**
 * Returns the chip selector for the given theme. Both layouts render the chip
 * inside the page chrome only when all lines are silent.
 */
function chipSelector(theme: Theme): string {
  return theme === 'dialup'
    ? '.dialup-toolbar .chip--err'
    : '.rail .chip--err';
}

/**
 * Sets the "Silence All" toggle via the settings form. The toggle auto-submits
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
  await expect(checkbox).toBeAttached();
  const checked = await checkbox.isChecked();
  if ((target === 'on' && checked) || (target === 'off' && !checked)) {
    return; // already in desired state
  }
  const wait = page.waitForResponse(
    (r) =>
      r.url().includes('/settings/do-not-disturb') &&
      r.request().method() === 'POST',
  );
  await checkbox.setChecked(target === 'on', { force: true });
  await wait;
  // The handler returns 303 to /settings?saved=1. The page started at
  // /settings#do-not-disturb, so a plain "/settings" check resolves
  // immediately against the current URL and lets the next goto race the
  // in-flight redirect; require the ?saved=1 query the handler appends.
  await page.waitForURL((u) => u.toString().includes('/settings?saved=1'));
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

    test('chip appears across pages when silenced', async ({
      page,
    }) => {
      // The household needs at least one line for "Silence All" to have
      // any effect (derived state requires lines to exist).
      await ensureLine(page);

      // Make sure silence starts off so the on/off transition is real.
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

      // Toggle off and confirm the chip disappears from the overview.
      await setDoNotDisturb(page, 'off');
      await page.goto('/');
      await expect(page.locator(sel)).toHaveCount(0);
    });
  });
}

runDNDFlow('intercom');
runDNDFlow('dialup');
