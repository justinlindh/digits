/**
 * 07-navigation.spec.ts — Cross-page navigation smoke tests.
 *
 * Verifies that clicking nav links moves between pages correctly
 * and that each page renders without a 500 / blank screen.
 */

import { test, expect } from '@playwright/test';
import { isServerUp, navLink } from './helpers';

test.beforeEach(async ({ page }, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

function isAuthOrOnboard(url: string) {
  return url.includes('/auth/login') || url.includes('/onboard');
}

const PAGES = [
  { path: '/',          titlePattern: /Dashboard|Overview|Digits/i },
  { path: '/phones',    titlePattern: /Lines|Digits/i },
  { path: '/calls',     titlePattern: /Calls|Digits/i },
  { path: '/links',     titlePattern: /Connected [Ff]amilies|Digits/i },
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

test('page chrome exposes nav links for the core sections', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  // Both layouts ship nav anchors to /, /phones, /links, /settings outside
  // of <main>. The navLink helper scopes to the layout-specific containers
  // (.rail__nav for v2, .dialup-channels + .dialup-toolbar for dialup).
  const expectedHrefs = ['/', '/phones', '/links', '/settings'];
  for (const href of expectedHrefs) {
    const link = navLink(page, href).first();
    await expect(link).toBeAttached();
  }
});

test('clicking Phones nav link goes to /phones', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  // In the dialup layout the same page can expose two anchors to /phones
  // (toolbar button + channel tile). Either works — take the first.
  await navLink(page, '/phones').first().click();
  await expect(page).toHaveURL('/phones');
  await expect(page.locator('h1', { hasText: 'Lines' })).toBeVisible();
});

test('clicking Settings nav link goes to /settings', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  await navLink(page, '/settings').first().click();
  await expect(page).toHaveURL('/settings');
  await expect(page.locator('h1', { hasText: 'Settings' })).toBeVisible();
});

test('clicking Links nav link goes to /links', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  await navLink(page, '/links').first().click();
  await expect(page).toHaveURL('/links');
  await expect(page.locator('h1', { hasText: /connected families/i })).toBeVisible();
});
