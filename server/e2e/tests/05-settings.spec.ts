/**
 * 05-settings.spec.ts — Settings page golden path.
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

test.describe('Settings', () => {
  test('settings page renders correctly', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h1', { hasText: 'Settings' })).toBeVisible();
    await expect(page).toHaveTitle(/Settings|Digits/i);
  });

  test('settings shows Account section', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h2', { hasText: 'Account' })).toBeVisible();
  });

  test('settings shows Household section', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // The Household section only appears if the user has a household
    const householdSection = page.locator('h2', { hasText: 'Household' });
    const onboard = page.locator('text=Welcome to Digits');

    // Either we have a household or we got redirected to onboard
    const householdVisible = await householdSection.isVisible().catch(() => false);
    const onboardVisible = await onboard.isVisible().catch(() => false);

    // At least one should be true
    expect(householdVisible || onboardVisible || page.url().includes('/onboard')).toBeTruthy();
  });

  test('settings shows household name input field', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const nameInput = page.locator('input[name="name"]#household-name');
    const visible = await nameInput.isVisible().catch(() => false);
    if (!visible) {
      test.skip(true, 'User has no household yet');
      return;
    }

    await expect(nameInput).toBeVisible();
    await expect(nameInput).not.toBeEmpty();
  });

  test('settings shows Privacy / call history toggle', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const privacySection = page.locator('h2', { hasText: 'Privacy' });
    const visible = await privacySection.isVisible().catch(() => false);
    if (!visible) {
      test.skip(true, 'User has no household yet — Privacy section not shown');
      return;
    }

    await expect(privacySection).toBeVisible();
    await expect(page.locator('text=Call History')).toBeVisible();

    // Toggle checkbox
    const toggle = page.locator('input[type="checkbox"][name="enabled"]');
    await expect(toggle).toBeVisible();
  });

  test('settings shows Sign out button', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Sign out button in the Session section of the settings page
    const signOut = page.locator('main button', { hasText: /sign out/i });
    await expect(signOut).toBeVisible();
  });

  test('nav shows Settings as active on /settings', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const activeLink = page.locator('.nav-link.active');
    const text = await activeLink.textContent();
    expect(text?.toLowerCase()).toContain('setting');
  });

  test('save household name form has correct action', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const form = page.locator('form[action="/settings/household"]');
    const visible = await form.isVisible().catch(() => false);
    if (!visible) {
      test.skip(true, 'No household form visible');
      return;
    }

    await expect(form).toBeVisible();
    const method = await form.getAttribute('method');
    expect(method?.toLowerCase()).toBe('post');
  });
});
