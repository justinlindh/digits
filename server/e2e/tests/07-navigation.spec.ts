/**
 * 07-navigation.spec.ts — Cross-page navigation smoke tests.
 *
 * Verifies that clicking nav links moves between pages correctly
 * and that each page renders without a 500 / blank screen.
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

const PAGES = [
  { path: '/',          titlePattern: /Dashboard|Digits/i },
  { path: '/phones',    titlePattern: /Lines|Digits/i },
  { path: '/calls',     titlePattern: /Calls|Digits/i },
  { path: '/links',     titlePattern: /Connected Families|Digits/i },
  { path: '/settings',  titlePattern: /Settings|Digits/i },
];

for (const { path, titlePattern } of PAGES) {
  test(`${path} loads without error`, async ({ page }) => {
    await page.goto(path);
    if (isAuthOrOnboard(page.url())) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }

    // Page title matches expectation
    await expect(page).toHaveTitle(titlePattern);

    // No 500 / error page
    const body = await page.textContent('body');
    expect(body).not.toMatch(/500|internal server error/i);
  });
}

test('sidebar nav links are all present and have correct hrefs', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  const expectedHrefs = ['/', '/phones', '/links', '/settings'];
  for (const href of expectedHrefs) {
    const link = page.locator(`#sidebar a[href="${href}"]`);
    await expect(link).toBeAttached();
  }
});

test('clicking Phones nav link goes to /phones', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  await page.locator('#sidebar a[href="/phones"]').click();
  await expect(page).toHaveURL('/phones');
  await expect(page.locator('h1', { hasText: 'Lines' })).toBeVisible();
});

test('clicking Settings nav link goes to /settings', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  await page.locator('#sidebar a[href="/settings"]').click();
  await expect(page).toHaveURL('/settings');
  await expect(page.locator('h1', { hasText: 'Settings' })).toBeVisible();
});

test('clicking Links nav link goes to /links', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  await page.locator('#sidebar a[href="/links"]').click();
  await expect(page).toHaveURL('/links');
  await expect(page.locator('h1', { hasText: 'Connected Families' })).toBeVisible();
});
