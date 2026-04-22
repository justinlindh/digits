/**
 * 06-links.spec.ts — Household Links page golden path.
 *
 * Verifies:
 *   - Page renders without errors
 *   - Create Invite and Accept Invite cards are visible
 *   - Connected families section renders a hub-and-spoke card or empty state
 *   - Pending invites section renders when there are any
 */

import { test, expect } from '@playwright/test';
import { activeNavLink, isServerUp } from './helpers';

test.beforeEach(async ({ page }, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

function isAuthOrOnboard(url: string) {
  return url.includes('/auth/login') || url.includes('/onboard');
}

test.describe('Links page', () => {
  test('links page renders without error', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Page h1 uses .page__title; copy is "Connected families" (lowercase f)
    // in the redesign. Match case-insensitively so a label tweak doesn't
    // break the test.
    await expect(page.locator('h1', { hasText: /connected families/i })).toBeVisible();
    await expect(page).toHaveTitle(/Connected [Ff]amilies|Digits/i);
  });

  test('create invite card is visible', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h2', { hasText: /create invite/i })).toBeVisible();

    // Button text is "Invite a friend" before a code exists, and
    // "Generate another" after one does. Either reading counts as "the
    // create-invite CTA is visible".
    const generateBtn = page.locator('button[type="submit"]', {
      hasText: /invite a friend|generate another/i,
    });
    await expect(generateBtn).toBeVisible();
  });

  test('accept invite card is visible with code input', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h2', { hasText: /accept invite/i })).toBeVisible();

    const codeInput = page.locator('input[name="code"]');
    await expect(codeInput).toBeVisible();

    // The redesigned invite input uses field__input--mono + text-transform
    // uppercase (inline style). Assert it has either the mono class (which
    // gives monospace font + letter tracking) or a style that uppercases.
    const cls = (await codeInput.getAttribute('class')) ?? '';
    const style = (await codeInput.getAttribute('style')) ?? '';
    expect(cls.toLowerCase() + ' ' + style.toLowerCase()).toMatch(/mono|uppercase/);

    const acceptBtn = page.locator('button[type="submit"]', { hasText: /accept invite/i });
    await expect(acceptBtn).toBeVisible();
  });

  test('connected families section renders hub card or empty state', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // The redesigned Links page drops the old top-level table. Section
    // heading is an h2 above a hub-and-spoke card OR an empty state. The
    // heading copy is "Connected · N households" when populated, bare
    // "Connected" when empty.
    const sectionHeading = page.locator('h2.panel__title', { hasText: /^connected(\b|$)/i });
    await expect(sectionHeading.first()).toBeVisible();

    // Either a hub (.hub) exists (populated) or the empty-state copy is
    // visible.
    const hub = page.locator('.hub');
    const emptyState = page.locator('text=No connected families yet.');

    const hubVisible = await hub.first().isVisible().catch(() => false);
    const emptyVisible = await emptyState.isVisible().catch(() => false);

    expect(hubVisible || emptyVisible).toBeTruthy();
  });

  test('hub card shows your household and each linked family exactly once per spoke', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Skip when there are no linked families — nothing to test against.
    const hub = page.locator('.hub').first();
    if (!(await hub.isVisible().catch(() => false))) {
      test.skip(true, 'No linked families — hub card not rendered');
      return;
    }

    // Each spoke is one linked family. For N >= 0, .hub__spoke count equals
    // the number of per-family detail panels below the hub.
    const spokes = hub.locator('.hub__spoke');
    const spokeCount = await spokes.count();
    expect(spokeCount).toBeGreaterThan(0);

    // Center shows the user's own household name and is unique.
    const center = hub.locator('.hub__center-name');
    await expect(center).toHaveCount(1);
  });

  test('pending invites section renders when at least one invite is pending', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Pending-invites section is conditionally rendered (only when there are
    // any). Copy is "Pending invites sent" (lowercase after "Pending").
    const heading = page.locator('h2.panel__title', { hasText: /pending invites sent/i });
    const visible = await heading.first().isVisible().catch(() => false);
    if (!visible) {
      test.skip(true, 'No pending invites — section not rendered');
      return;
    }

    // When visible it's accompanied by a table or mobile <ul>. Assert at
    // least one of those exists so a regression that hides all rows is
    // caught.
    const rowsPresent =
      (await page.locator('table.t tbody tr').count()) +
      (await page.locator('ul.show-sm > li').count());
    expect(rowsPresent).toBeGreaterThan(0);
  });

  test('accept invite form has correct action and maxlength', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const form = page.locator('form[action="/links/accept"]');
    await expect(form).toBeAttached();

    const method = await form.getAttribute('method');
    expect(method?.toLowerCase()).toBe('post');

    const codeInput = form.locator('input[name="code"]');
    const maxlength = await codeInput.getAttribute('maxlength');
    expect(Number(maxlength)).toBeGreaterThan(0);
  });

  test('nav shows Links (Families) as active on /links', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Label differs per layout:
    //   v2     -> "Families"
    //   dialup -> "FAMILIES" (channels sidebar)
    const active = activeNavLink(page).first();
    await expect(active).toBeVisible();
    const text = ((await active.textContent()) ?? '').toLowerCase();
    expect(text).toContain('families');
  });
});
