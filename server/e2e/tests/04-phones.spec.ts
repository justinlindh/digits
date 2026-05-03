/**
 * 04-phones.spec.ts — Phone management golden path.
 *
 * Tests:
 *   - Phones list page renders
 *   - Pair a Phone form is present with correct fields
 *   - Navigation from phones list to phone detail (if a phone exists)
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



test.describe('Phones list', () => {
  test('phones page renders heading and pair form', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Page heading: <h1 class="page__title">Lines</h1>
    await expect(page.locator('h1', { hasText: 'Lines' })).toBeVisible();

    // When lines exist, the pair section collapses into a <details> titled
    // "Add a phone". When no lines exist, it renders as an open panel titled
    // "Pair a new handset". Check for either.
    const pairHeading = page.locator('h2.panel__title', { hasText: /pair a new handset|add a phone/i });
    await expect(pairHeading).toBeVisible();
  });

  test('pair form has visible manual code, line number, and line name inputs', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }


    // The redesigned pair form splits the pairing code into two inputs: a
    // hidden input[name="code"] submitted to the server, and a visible
    // input[name="code_manual"] that the keypad and typed digits drive. The
    // keypad + pin display are the primary UI; the manual input is a
    // secondary typing affordance. Assert the visible manual input here.
    const codeInput = page.locator('input[name="code_manual"]');
    await expect(codeInput).toBeVisible();
    expect(await codeInput.getAttribute('placeholder')).toMatch(/\d+/);

    // Line number input — visible.
    const numberInput = page.locator('input[name="number"]');
    await expect(numberInput).toBeVisible();

    // Line name input — visible.
    const nameInput = page.locator('input[name="name"]');
    await expect(nameInput).toBeVisible();

    // The Pair button (starts disabled until a 6-digit code, a 7-digit
    // number, and a name are all present — covered by the dedicated test
    // below).
    const pairBtn = page.locator('button[type="submit"]', { hasText: /pair/i });
    await expect(pairBtn).toBeVisible();
  });

  test('pair form validates code length (HTML5 pattern)', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }


    // The visible manual input carries the pattern + maxlength. The hidden
    // input[name="code"] is populated by JS from this one (or the keypad).
    const codeInput = page.locator('input[name="code_manual"]');
    const pattern = await codeInput.getAttribute('pattern');
    expect(pattern).toBeTruthy(); // e.g. \d{6}

    const maxlength = await codeInput.getAttribute('maxlength');
    expect(Number(maxlength)).toBe(6);
  });

  test('pair button starts disabled until the 6-digit code is filled', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }


    // Fresh load: no code, no number, no name — submit is disabled.
    const pairBtn = page.locator('button#pair-submit');
    await expect(pairBtn).toBeDisabled();

    // Tap keypad for 6 digits to fill the code only. Number + name still
    // empty, so button remains disabled (the JS requires all three).
    for (const d of ['1', '2', '3', '4', '5', '6']) {
      await page.locator(`.keypad__btn[data-digit="${d}"]`).click();
    }
    await expect(pairBtn).toBeDisabled();
  });

  // TODO: fix HTML5 pattern validation test -- Playwright click() appears to
  // bypass browser form validation in headless Chromium, causing the form to
  // submit and lose the session. Tracked separately from CI setup.
  test.skip('pair form rejects invalid code without submitting to server', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Type a partial code and attempt submit
    await page.fill('input[name="code_manual"]', '123');   // too short
    await page.fill('input[name="number"]', '3140001');
    await page.fill('input[name="name"]', 'Test Phone');
    await page.click('button[type="submit"]');

    // Should still be on /phones (HTML5 validation blocked submit)
    expect(page.url()).toContain('/phones');
  });

  test('registered phones panel renders with header', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // The phones-table partial always renders its panel header even when
    // there are no lines yet (empty state). Copy is "Your lines".
    await expect(page.locator('h2.panel__title', { hasText: /your lines/i })).toBeVisible();
  });

  test('nav shows Phones (Lines) as active on /phones', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Both layouts mark the current page with .is-active. Label differs:
    //   v2     -> "Lines"
    //   dialup -> "MY PHONES" (channels sidebar) — uppercase via markup
    // So we match either.
    const active = activeNavLink(page).first();
    await expect(active).toBeVisible();
    const text = (await active.textContent()) ?? '';
    expect(text).toMatch(/lines|my phones/i);
  });
});

test.describe('Phone detail', () => {
  test('phone detail page renders for existing phone', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Check if there are any phones listed
    const phoneLinks = page.locator('a[href^="/phones/"]');
    const count = await phoneLinks.count();

    if (count === 0) {
      test.skip(true, 'No phones registered — skipping phone detail test');
      return;
    }

    // Click the first phone
    const href = await phoneLinks.first().getAttribute('href');
    await phoneLinks.first().click();

    await page.waitForURL(`**${href}**`);

    // Phone detail heading
    await expect(page.locator('h1')).toBeVisible();

    // "Details" section
    await expect(page.locator('text=Details')).toBeVisible();

    // Status badge (online / offline)
    const statusBadge = page.locator('text=online, text=offline').first();
    await expect(statusBadge).toBeVisible();

    // Back link to /phones
    await expect(page.locator('a[href="/phones"]')).toBeVisible();
  });

  test('phone detail shows hardware & software card', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const phoneLinks = page.locator('a[href^="/phones/"]');
    if (await phoneLinks.count() === 0) {
      test.skip(true, 'No phones registered');
      return;
    }

    await phoneLinks.first().click();
    await page.waitForURL('**/phones/**');

    await expect(page.locator('text=Hardware')).toBeVisible();
  });
});
