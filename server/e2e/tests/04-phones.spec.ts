/**
 * 04-phones.spec.ts — Phone management golden path.
 *
 * Tests:
 *   - Phones list page renders
 *   - Pair a Phone form is present with correct fields
 *   - Navigation from phones list to phone detail (if a phone exists)
 */

import { test, expect } from '@playwright/test';
import { isServerUp } from './helpers';

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

    // Page heading
    await expect(page.locator('h1', { hasText: 'Lines' })).toBeVisible();

    // "Pair a Device" section
    await expect(page.locator('h2', { hasText: 'Pair a Device' })).toBeVisible();
  });

  test('pair form has pairing code, number, and name inputs', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Pairing code input (6-digit, font-mono)
    const codeInput = page.locator('input[name="code"]');
    await expect(codeInput).toBeVisible();
    expect(await codeInput.getAttribute('placeholder')).toMatch(/\d+/);

    // Phone number input
    const numberInput = page.locator('input[name="number"]');
    await expect(numberInput).toBeVisible();

    // Display name input
    const nameInput = page.locator('input[name="name"]');
    await expect(nameInput).toBeVisible();

    // Pair button
    const pairBtn = page.locator('button[type="submit"]', { hasText: /pair/i });
    await expect(pairBtn).toBeVisible();
  });

  test('pair form validates code length (HTML5 pattern)', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const codeInput = page.locator('input[name="code"]');
    const pattern = await codeInput.getAttribute('pattern');
    expect(pattern).toBeTruthy(); // e.g. \d{6}

    const maxlength = await codeInput.getAttribute('maxlength');
    expect(Number(maxlength)).toBe(6);
  });

  test('pair form rejects invalid code without submitting to server', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Type a partial code and attempt submit
    await page.fill('input[name="code"]', '123');        // too short
    await page.fill('input[name="number"]', '3140001');
    await page.fill('input[name="name"]', 'Test Phone');
    await page.click('button[type="submit"]');

    // Should still be on /phones (HTML5 validation blocked submit)
    expect(page.url()).toContain('/phones');
  });

  test('registered phones table renders when phones exist', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // The "Registered Phones" section always renders
    await expect(page.locator('text=Registered Phones')).toBeVisible();
  });

  test('nav shows Phones as active on /phones', async ({ page }) => {
    await page.goto('/phones');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const activeLink = page.locator('.nav-link.active');
    const text = await activeLink.textContent();
    expect(text?.toLowerCase()).toContain('phone');
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
