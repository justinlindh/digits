/**
 * 08-theme.spec.ts — Theme switcher and per-theme rendering.
 *
 * The redesign ships two visual themes:
 *   - "intercom" Home-intercom (default): layout-v2.html, warm paper + brass
 *   - "dialup"   Online-service 1997:     layout-dialup.html, blue title bar
 *
 * These tests exercise the theme switcher on /settings and verify each
 * layout renders the core pages without error. Each test restores the
 * default theme ("intercom") in afterEach so it doesn't leak into later files.
 *
 * If no auth session is available (dev-session endpoint missing), the tests
 * skip -- same pattern as every other spec here.
 */

import { test, expect } from '@playwright/test';
import { getLayout, isServerUp, layoutForTheme, setTheme, Theme } from './helpers';

test.beforeEach(async ({ page }, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

function isAuthOrOnboard(url: string) {
  return url.includes('/auth/login') || url.includes('/onboard');
}

test.describe('Theme switcher', () => {
  test.afterEach(async ({ page }) => {
    // Always restore the default theme so later test files start from a
    // known state. If the request 401s (no session) just ignore.
    await setTheme(page.request, 'intercom').catch(() => undefined);
  });

  test('default theme "intercom" renders layout-v2 (rail)', async ({ page }) => {
    await setTheme(page.request, 'intercom').catch(() => undefined);
    await page.goto('/');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    expect(await getLayout(page)).toBe('v2');
    await expect(page.locator('header.rail')).toBeVisible();
    // Direction-C rail nav labels are capitalised: Overview, Families, ...
    await expect(page.locator('.rail__nav a', { hasText: /overview/i })).toBeVisible();
  });

  test('theme "dialup" renders layout-dialup (channels sidebar)', async ({ page }) => {
    // Skip cleanly if we cannot even switch themes (no session).
    try {
      await setTheme(page.request, 'dialup');
    } catch (e) {
      test.skip(true, `setTheme failed: ${(e as Error).message}`);
      return;
    }

    await page.goto('/');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    expect(await getLayout(page)).toBe('dialup');
    await expect(page.locator('.dialup-window')).toBeVisible();
    await expect(page.locator('.dialup-channels')).toBeVisible();
    // Channel labels are uppercase in the markup: WELCOME, FAMILIES, ...
    // (the "MY PHONES" channel was removed when Lines merged into Overview).
    await expect(page.locator('.dialup-channels a', { hasText: /welcome/i })).toBeVisible();
  });

  test('theme "answering-machine" renders layout-am (shell + LED chrome)', async ({ page }) => {
    try {
      await setTheme(page.request, 'answering-machine');
    } catch (e) {
      test.skip(true, `setTheme failed: ${(e as Error).message}`);
      return;
    }

    await page.goto('/');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    expect(await getLayout(page)).toBe('am');
    await expect(page.locator('body.am')).toHaveCount(1);
    await expect(page.locator('.am-shell')).toBeVisible();
    // AM nav tabs mirror the rail nav but with am-tab styling.
    await expect(page.locator('.am-tabs a.am-tab', { hasText: /overview/i })).toBeVisible();
  });

  test('POST /settings/theme round-trips and settings page reflects the new theme', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Flip to dialup via form submit, then reload /settings and confirm the
    // dialup radio is checked.
    await setTheme(page.request, 'dialup');
    await page.goto('/settings');
    await expect(page.locator('input[type="radio"][name="theme"][value="dialup"]')).toBeChecked();

    // Flip back to c and confirm.
    await setTheme(page.request, 'intercom');
    await page.goto('/settings');
    await expect(page.locator('input[type="radio"][name="theme"][value="intercom"]')).toBeChecked();
  });
});

// -- Per-theme smoke tests ---------------------------------------------------
//
// For each core page and each theme, verify the page loads under that theme.
// This is the per-theme coverage the redesign spec asks for: a regression
// that only surfaces on one layout (e.g. a dialup template block that
// references a field only the C dashboard exposes) gets caught here.

const CORE_PAGES = [
  { path: '/',         h1: /./ },                    // any non-empty h1 (household name)
  { path: '/phones',   h1: /pair a handset/i },
  { path: '/links',    h1: /connected families/i },
  { path: '/settings', h1: /^Settings$/ },
];

for (const theme of ['intercom', 'dialup', 'answering-machine'] as Theme[]) {
  test.describe(`core pages render under theme "${theme}"`, () => {
    test.beforeEach(async ({ page }) => {
      // Skip the whole group if we can't establish a session.
      const up = await isServerUp();
      if (!up) test.skip(true, 'Dev server not running');
      try {
        await setTheme(page.request, theme);
      } catch (e) {
        test.skip(true, `setTheme(${theme}) failed: ${(e as Error).message}`);
      }
    });

    test.afterEach(async ({ page }) => {
      // Always leave the DB pointing at the default theme.
      await setTheme(page.request, 'intercom').catch(() => undefined);
    });

    for (const { path, h1 } of CORE_PAGES) {
      test(`${path} renders under ${theme}`, async ({ page }) => {
        await page.goto(path);
        if (isAuthOrOnboard(page.url())) {
          test.skip(true, 'No authenticated session or needs onboarding');
          return;
        }

        // Layout must match the theme we set.
        expect(await getLayout(page)).toBe(layoutForTheme(theme));

        // Body contains no visible 500 / crash indicator. Word-boundary on
        // the bare "500" keeps the AM chrome brand "Digits 2500" from
        // triggering a false positive.
        const body = (await page.textContent('body')) ?? '';
        expect(body).not.toMatch(/\b500\b|internal server error/i);

        // The AM theme replaces page-level h1s with LED plates and am-title
        // headers, so the heading-copy assertion only applies to the two
        // themes that still render a conventional h1 at the top of each page.
        if (theme !== 'answering-machine') {
          const heading = page.locator('h1').first();
          await expect(heading).toBeVisible();
          const text = ((await heading.textContent()) ?? '').trim();
          expect(text).toMatch(h1);
        }
      });
    }
  });
}
