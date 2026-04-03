/**
 * 03-onboarding.spec.ts — Onboarding flow.
 *
 * Tests the new-user household creation page (/onboard).
 * This page is served when a user has no household yet.
 * We test the UI structure; we don't submit the form to avoid
 * permanently creating a household in a live environment.
 */

import { test, expect } from '@playwright/test';
import { isServerUp } from './helpers';

test.beforeEach(async ({}, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

test.describe('Onboarding', () => {
  test('onboard page renders household form', async ({ page }) => {
    await page.goto('/onboard');

    // If redirected to login, skip — we don't have auth
    if (page.url().includes('/auth/login')) {
      test.skip(true, 'No authenticated session');
      return;
    }

    // Page heading
    const heading = page.locator('text=Welcome to Digits');
    await expect(heading).toBeVisible();

    // Family name input
    const nameInput = page.locator('input[type="text"][name="name"]');
    await expect(nameInput).toBeVisible();

    // The input may be pre-filled with a suggested name
    const placeholder = await nameInput.getAttribute('placeholder');
    expect(placeholder).toBeTruthy();

    // Submit button
    const submitBtn = page.locator('button[type="submit"]', { hasText: /create household/i });
    await expect(submitBtn).toBeVisible();
  });

  test('onboard form has correct action and method', async ({ page }) => {
    await page.goto('/onboard');

    if (page.url().includes('/auth/login')) {
      test.skip(true, 'No authenticated session');
      return;
    }

    const form = page.locator('form[action="/onboard"]');
    await expect(form).toBeAttached();

    const method = await form.getAttribute('method');
    expect(method?.toLowerCase()).toBe('post');
  });

  test('onboard page shows helpful copy', async ({ page }) => {
    await page.goto('/onboard');

    if (page.url().includes('/auth/login')) {
      test.skip(true, 'No authenticated session');
      return;
    }

    // Description copy
    const body = await page.textContent('body');
    expect(body).toMatch(/family|household/i);
  });
});
