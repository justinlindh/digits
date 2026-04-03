/**
 * 01-login.spec.ts — Login page smoke tests (no auth required).
 *
 * These tests run against the public login page and do not need an authenticated
 * session, so they work even without E2E_SESSION_COOKIE / E2E_MAGIC_TOKEN.
 */

import { test, expect } from '@playwright/test';
import { isServerUp } from './helpers';

test.beforeEach(async ({}, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

test.describe('Login page', () => {
  test('renders the login page with email form', async ({ page }) => {
    await page.goto('/auth/login');

    // Page title
    await expect(page).toHaveTitle(/Sign In|Digits/i);

    // DIGITS branding
    await expect(page.locator('text=DIGITS')).toBeVisible();

    // Email input
    const emailInput = page.locator('input[type="email"][name="email"]');
    await expect(emailInput).toBeVisible();

    // Submit button
    const submitBtn = page.locator('button[type="submit"]', { hasText: /sign.in link|send/i });
    await expect(submitBtn).toBeVisible();
  });

  test('shows placeholder text on email input', async ({ page }) => {
    await page.goto('/auth/login');
    const emailInput = page.locator('input[type="email"]');
    const placeholder = await emailInput.getAttribute('placeholder');
    expect(placeholder).toMatch(/@/);
  });

  test('submitting empty email shows validation (HTML5)', async ({ page }) => {
    await page.goto('/auth/login');
    // Click submit without filling email — browser native validation prevents submit
    await page.click('button[type="submit"]');
    // Still on login page
    expect(page.url()).toContain('/auth/login');
  });

  test('submitting email redirects to login with success message', async ({ page }) => {
    await page.goto('/auth/login');
    await page.fill('input[type="email"]', 'nouser@example.com');

    // Don't follow the form POST — watch for the success redirect
    await page.click('button[type="submit"]');

    // Server redirects back to /auth/login?success=... or shows a success banner
    await page.waitForURL('**/auth/login**', { timeout: 8000 });
    const url = page.url();

    // Either "success" param or green banner visible
    const hasSuccessParam = url.includes('success');
    const successBanner = page.locator('.bg-green\\/10, [class*="green"]');

    if (!hasSuccessParam) {
      await expect(successBanner.first()).toBeVisible();
    } else {
      expect(hasSuccessParam).toBeTruthy();
    }
  });

  test('healthz endpoint returns 200', async ({ request }) => {
    const res = await request.get('/healthz');
    expect(res.ok()).toBeTruthy();
  });
});
