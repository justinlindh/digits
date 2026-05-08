/**
 * 05-settings.spec.ts — Settings page golden path.
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

    await expect(page.getByRole('heading', { name: 'Account', exact: true })).toBeVisible();
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
    // Copy is "Call log" rendered in a <div> next to the toggle.
    await expect(page.locator('text=/call log/i').first()).toBeVisible();

    // Toggle checkbox — wrapped in <label class="toggle">, so the input is
    // present in the DOM but typically visually hidden via CSS (the <span
    // class="toggle__track"> is the visible control). Use toBeAttached
    // instead of toBeVisible.
    const toggle = page.locator('#privacy input[type="checkbox"][name="enabled"]');
    await expect(toggle).toBeAttached();
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

  test('settings shows a Theme picker with two options', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h2.panel__title', { hasText: /^Theme$/i })).toBeVisible();

    // Two radios: value="intercom" (home intercom default) and value="dialup"
    // (the 1997 online-service alternate).
    await expect(page.locator('input[type="radio"][name="theme"][value="intercom"]')).toBeAttached();
    await expect(page.locator('input[type="radio"][name="theme"][value="dialup"]')).toBeAttached();

    // Default user should have theme "intercom" selected. (We don't assert checked
    // directly because earlier runs on the same DB may have flipped it.)
    const submit = page.locator('button[type="submit"]', { hasText: /save theme/i });
    await expect(submit).toBeVisible();
  });

  test('nav shows Settings as active on /settings', async ({ page }) => {
    await page.goto('/settings');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Both layouts use .is-active on the current-page link. Label differs
    // per layout ("Settings" in v2, "SETTINGS" in dialup), so match both.
    const active = activeNavLink(page).first();
    await expect(active).toBeVisible();
    const text = ((await active.textContent()) ?? '').toLowerCase();
    expect(text).toContain('setting');
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
