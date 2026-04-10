/**
 * 06-links.spec.ts — Household Links page golden path.
 *
 * Verifies:
 *   - Page renders without errors
 *   - Create Invite and Accept Invite cards are visible
 *   - Active Links table renders
 *   - Pending Invites table renders
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

test.describe('Links page', () => {
  test('links page renders without error', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h1', { hasText: 'Connected Families' })).toBeVisible();
    await expect(page).toHaveTitle(/Connected Families|Digits/i);
  });

  test('create invite card is visible', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h2', { hasText: 'Create Invite' })).toBeVisible();

    const generateBtn = page.locator('button[type="submit"]', { hasText: /generate invite code/i });
    await expect(generateBtn).toBeVisible();
  });

  test('accept invite card is visible with code input', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h2', { hasText: 'Accept Invite' })).toBeVisible();

    const codeInput = page.locator('input[name="code"]');
    await expect(codeInput).toBeVisible();

    // Uppercase/font-mono tracking
    const cls = await codeInput.getAttribute('class');
    expect(cls).toMatch(/mono|uppercase/i);

    const acceptBtn = page.locator('button[type="submit"]', { hasText: /accept invite/i });
    await expect(acceptBtn).toBeVisible();
  });

  test('active links section renders table or empty state', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // "Connected Families" heading
    await expect(page.locator('h2', { hasText: 'Connected Families' })).toBeVisible();

    // Either linked family cards or the empty state message
    const disconnectBtn = page.locator('button', { hasText: 'Disconnect' });
    const emptyState = page.locator('text=No connected families yet.');

    const hasFamily = await disconnectBtn.first().isVisible().catch(() => false);
    const emptyVisible = await emptyState.isVisible().catch(() => false);

    expect(hasFamily || emptyVisible).toBeTruthy();
  });

  test('active links table has correct columns when populated', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const table = page.locator('table').first();
    const hasTable = await table.isVisible().catch(() => false);
    if (!hasTable) {
      test.skip(true, 'No active links — table not visible');
      return;
    }

    // Table headers
    const headers = table.locator('th');
    const headerTexts = await headers.allTextContents();
    const normalised = headerTexts.map(t => t.toLowerCase().trim());

    expect(normalised.some(h => h.includes('household'))).toBeTruthy();
    expect(normalised.some(h => h.includes('status'))).toBeTruthy();
    expect(normalised.some(h => h.includes('linked'))).toBeTruthy();
  });

  test('pending invites section renders table or empty state', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    await expect(page.locator('h2', { hasText: 'Pending Invites Sent' })).toBeVisible();

    const emptyState = page.locator('text=No pending invites');
    const table = page.locator('table').nth(1); // second table if it exists

    const emptyVisible = await emptyState.isVisible().catch(() => false);
    const tableVisible = await table.isVisible().catch(() => false);

    expect(emptyVisible || tableVisible).toBeTruthy();
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

  test('nav shows Links as active on /links', async ({ page }) => {
    await page.goto('/links');
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    const activeLink = page.locator('.nav-link.active');
    const text = await activeLink.textContent();
    expect(text?.toLowerCase()).toContain('connected families');
  });
});
