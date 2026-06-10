/**
 * 07-navigation.spec.ts: Cross-page navigation smoke tests.
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
  { path: '/phones',    titlePattern: /Pair|Digits/i },
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

  // Both layouts ship nav anchors to /, /links, /settings outside of <main>.
  // There is no longer a "Lines"/"Phones" nav item: pairing is reached via the
  // "+ Pair a handset" button on Overview. The navLink helper scopes to the
  // layout-specific containers (.rail__nav for v2, .dialup-channels +
  // .dialup-toolbar for dialup).
  const expectedHrefs = ['/', '/links', '/settings'];
  for (const href of expectedHrefs) {
    const link = navLink(page, href).first();
    await expect(link).toBeAttached();
  }
});

test('chrome no longer exposes a Phones/Lines nav link', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  // The "Lines" nav item was removed when the page merged into Overview. The
  // only /phones anchors now live in page content (the Overview pair button),
  // never in the rail / channels / toolbar chrome.
  await expect(navLink(page, '/phones')).toHaveCount(0);
});

test('clicking the Pair a handset button goes to /phones', async ({ page }) => {
  await page.goto('/');
  if (isAuthOrOnboard(page.url())) {
    test.skip(true, 'No authenticated session or needs onboarding');
    return;
  }

  // The pairing page is now reached from the Overview header action button
  // (anchor href="/phones") rather than a nav item.
  await page.locator('.page__head-actions a[href="/phones"]').first().click();
  await expect(page).toHaveURL('/phones');
  await expect(page.locator('h1', { hasText: /pair a handset/i })).toBeVisible();
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
