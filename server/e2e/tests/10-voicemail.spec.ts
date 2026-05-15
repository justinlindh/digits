/**
 * 10-voicemail.spec.ts -- Detail-page voicemail panel: enable toggle, the
 * "Advanced settings" disclosure, the unheard-count chip, and the 5-field
 * Save flow. The list-page no longer surfaces voicemail; that direction was
 * pulled per owner feedback.
 *
 * Tests:
 *   - Detail page renders the panel with the enable checkbox + Advanced disclosure.
 *   - Enabling voicemail via the checkbox round-trips state through the toggle endpoint.
 *   - Expanding Advanced reveals the 4 numeric/code fields.
 *   - Saving valid Advanced values persists across reload.
 *   - Bad retrieval code is rejected (400).
 *   - All three themes render their respective surface (intercom, dialup, am).
 *
 * Skips when the test household has no paired phones.
 */

import { test, expect, Page } from '@playwright/test';
import { isServerUp, setTheme } from './helpers';

test.beforeEach(async ({ page }, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

function isAuthOrOnboard(url: string) {
  return url.includes('/auth/login') || url.includes('/onboard');
}

async function firstPhoneHref(page: Page): Promise<string | null> {
  await page.goto('/phones');
  if (isAuthOrOnboard(page.url())) {
    return null;
  }
  const link = page.locator('a[href^="/phones/"]:not([href="/phones"])').first();
  if ((await link.count()) === 0) {
    return null;
  }
  return await link.getAttribute('href');
}

async function readEnabledOnDetail(page: Page): Promise<boolean> {
  const checkbox = page.locator('#voicemail-section input[name="enabled"]').first();
  await expect(checkbox).toBeVisible();
  return await checkbox.isChecked();
}

/**
 * Click the enable checkbox and wait for the toggle endpoint round-trip.
 * The checkbox has hx-post + hx-trigger=change, so a click triggers the swap
 * of the whole voicemail section.
 */
async function clickEnableCheckbox(page: Page) {
  const checkbox = page.locator('#voicemail-section input[name="enabled"]').first();
  await expect(checkbox).toBeVisible();
  const wait = page.waitForResponse(
    (r) => r.url().includes('/voicemail-toggle') && r.request().method() === 'POST',
  );
  await checkbox.click();
  await wait;
}

test.describe('Voicemail (intercom theme)', () => {
  test.beforeEach(async ({ page }) => {
    await setTheme(page.request, 'intercom').catch(() => undefined);
  });

  test('detail page renders panel with enable checkbox + Advanced disclosure', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    await expect(page.locator('h2.panel__title', { hasText: /answering machine/i })).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="enabled"]')).toBeVisible();
    await expect(page.locator('#voicemail-section details.voicemail-advanced')).toBeVisible();

    // Advanced should be collapsed by default; the 4 fields are hidden until expanded.
    const ringField = page.locator('#voicemail-section input[name="ring_timeout_seconds"]');
    await expect(ringField).not.toBeVisible();
  });

  test('expanding Advanced reveals all four detail fields', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    await page.locator('#voicemail-section details.voicemail-advanced > summary').click();
    await expect(page.locator('#voicemail-section input[name="ring_timeout_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="max_message_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="max_stored_messages"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="retrieval_code"]')).toBeVisible();
  });

  test('checkbox toggle swaps section partial and persists', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    // Baseline: ensure off.
    if (await readEnabledOnDetail(page)) {
      await clickEnableCheckbox(page);
    }
    expect(await readEnabledOnDetail(page)).toBe(false);

    // Toggle on.
    await clickEnableCheckbox(page);
    expect(await readEnabledOnDetail(page)).toBe(true);

    // Reload: persisted server-side.
    await page.goto(href);
    expect(await readEnabledOnDetail(page)).toBe(true);

    // Cleanup: turn off.
    await clickEnableCheckbox(page);
  });

  test('saving valid Advanced values persists across reload', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    // Make sure voicemail is on so the inner fields aren't disabled.
    if (!(await readEnabledOnDetail(page))) {
      await clickEnableCheckbox(page);
    }

    // Expand Advanced and edit.
    await page.locator('#voicemail-section details.voicemail-advanced > summary').click();
    await page.locator('#voicemail-section input[name="ring_timeout_seconds"]').fill('30');
    await page.locator('#voicemail-section input[name="max_message_seconds"]').fill('100');
    await page.locator('#voicemail-section input[name="max_stored_messages"]').fill('25');
    await page.locator('#voicemail-section input[name="retrieval_code"]').fill('*99');

    const wait = page.waitForResponse(
      (r) => r.url().endsWith('/voicemail') && r.request().method() === 'POST',
    );
    await page.locator('#voicemail-section button[type="submit"]').first().click();
    const resp = await wait;
    expect(resp.status()).toBe(200);

    // Reload, expand, confirm fields stuck.
    await page.goto(href);
    await page.locator('#voicemail-section details.voicemail-advanced > summary').click();
    await expect(
      page.locator('#voicemail-section input[name="ring_timeout_seconds"]'),
    ).toHaveValue('30');
    await expect(
      page.locator('#voicemail-section input[name="retrieval_code"]'),
    ).toHaveValue('*99');

    // Cleanup: voicemail off.
    await clickEnableCheckbox(page);
  });

  test('bad retrieval code is rejected with 400', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    // Bypass HTML5 client-side validation and exercise the server-side guard.
    // "12345" is digits-only (no * or #), which the handler rejects to avoid
    // a 7-digit-dial collision.
    const number = href.split('/').pop() as string;
    const resp = await page.request.post(`/phones/${number}/voicemail`, {
      form: {
        enabled: 'on',
        ring_timeout_seconds: '20',
        max_message_seconds: '90',
        max_stored_messages: '50',
        retrieval_code: '12345',
      },
      maxRedirects: 0,
    });
    expect(resp.status()).toBe(400);
    expect(await resp.text()).toMatch(/retrieval_code/i);
  });

  test('list page does NOT show a voicemail badge', async ({ page }) => {
    // Regression guard: the list-row voicemail UI was removed per owner
    // direction. If a future hand re-adds the badge, this test fails.
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto('/phones');
    await expect(page.locator('form[data-voicemail]')).toHaveCount(0);
    await expect(page.locator('.phone-voicemail')).toHaveCount(0);
  });
});

test.describe('Voicemail (dialup theme)', () => {
  test.beforeEach(async ({ page }) => {
    try {
      await setTheme(page.request, 'dialup');
    } catch (e) {
      test.skip(true, `setTheme failed: ${(e as Error).message}`);
    }
  });

  test.afterEach(async ({ page }) => {
    await setTheme(page.request, 'intercom').catch(() => undefined);
  });

  test('detail page renders the panel under dialup chrome', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    await expect(page.locator('#voicemail-section')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="enabled"]')).toBeVisible();
    await expect(page.locator('#voicemail-section details.voicemail-advanced')).toBeVisible();
  });
});

test.describe('Voicemail (answering-machine theme)', () => {
  test.beforeEach(async ({ page }) => {
    try {
      await setTheme(page.request, 'answering-machine');
    } catch (e) {
      test.skip(true, `setTheme failed: ${(e as Error).message}`);
    }
  });

  test.afterEach(async ({ page }) => {
    await setTheme(page.request, 'intercom').catch(() => undefined);
  });

  test('AM detail page renders the voicemail plate with AM-styled disclosure', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    await expect(page.locator('.am-plate__label', { hasText: /answering machine/i })).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="enabled"]')).toBeVisible();
    await expect(page.locator('#voicemail-section details.am-voicemail-advanced')).toBeVisible();

    // Expand and confirm AM-styled fields show.
    await page.locator('#voicemail-section details.am-voicemail-advanced > summary').click();
    await expect(page.locator('#voicemail-section input[name="ring_timeout_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="retrieval_code"]')).toBeVisible();
  });
});
