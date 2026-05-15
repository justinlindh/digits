/**
 * 10-voicemail.spec.ts -- Per-line voicemail toggle on /phones plus the 5-field
 * settings form on /phones/{number}.
 *
 * Tests:
 *   - List-row toggle flips voicemail on, badge appears, persists across reload.
 *   - Toggle flips it back off, badge disappears.
 *   - Detail page renders the voicemail panel with the 5 fields.
 *   - Saving valid values via the detail form persists and swaps the partial.
 *   - Bad retrieval code is rejected (400).
 *   - All three themes render the relevant surface (intercom, dialup, am).
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

/**
 * Click the list-row voicemail toggle and wait for the htmx swap. Works for
 * both the intercom (.phone-voicemail) and answering-machine (.am-voicemail)
 * themes since the form id is the same in both.
 */
async function toggleVoicemailFromList(page: Page, number: string) {
  await page.goto('/phones');
  // The /phones page renders the voicemail form once per layout (desktop
  // table, mobile card, am roster); only one is visible at a time per
  // viewport. Use .first() to grab whichever is currently rendered.
  const form = page.locator(`form[data-voicemail][data-line="${number}"]`).first();
  await expect(form).toBeVisible();
  const wait = page.waitForResponse(
    (r) => r.url().includes('/voicemail-toggle') && r.request().method() === 'POST',
  );
  await form.locator('button[type="submit"]').first().click();
  await wait;
}

async function readEnabledOnDetail(page: Page): Promise<boolean> {
  const checkbox = page.locator('#voicemail-section input[name="enabled"]').first();
  await expect(checkbox).toBeVisible();
  return await checkbox.isChecked();
}

test.describe('Voicemail (intercom theme)', () => {
  test.beforeEach(async ({ page }) => {
    await setTheme(page.request, 'intercom').catch(() => undefined);
  });

  test('list-row toggle flips state and shows the badge', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    const number = href.split('/').pop() as string;

    // Baseline: enable, then disable so we know the start state.
    await page.goto(href);
    if (await readEnabledOnDetail(page)) {
      await toggleVoicemailFromList(page, number);
    }
    // Now off; confirm no chip on the visible row.
    await page.goto('/phones');
    const visibleForm = page
      .locator(`form[data-voicemail][data-line="${number}"]:visible`)
      .first();
    await expect(visibleForm.locator('.phone-voicemail__chip')).toHaveCount(0);

    // Toggle on; chip should appear (muted "VM" because count is 0).
    await toggleVoicemailFromList(page, number);
    await expect(
      page
        .locator(`form[data-voicemail][data-line="${number}"]:visible .phone-voicemail__chip`)
        .first(),
    ).toBeVisible();

    // Reload: state must be persisted server-side.
    await page.goto('/phones');
    await expect(
      page
        .locator(`form[data-voicemail][data-line="${number}"]:visible .phone-voicemail__chip`)
        .first(),
    ).toBeVisible();

    // Detail page must reflect the same state.
    await page.goto(href);
    expect(await readEnabledOnDetail(page)).toBe(true);

    // Cleanup: toggle off so the next test starts clean.
    await toggleVoicemailFromList(page, number);
    await page.goto('/phones');
    await expect(
      page.locator(
        `form[data-voicemail][data-line="${number}"]:visible .phone-voicemail__chip`,
      ),
    ).toHaveCount(0);
  });

  test('detail page renders the voicemail panel with five fields', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);
    await expect(page.locator('h2.panel__title', { hasText: /voicemail/i })).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="enabled"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="ring_timeout_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="max_message_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="max_stored_messages"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="retrieval_code"]')).toBeVisible();
  });

  test('detail form save with valid values persists', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    // Enable so the inner fields aren't disabled.
    const enabled = page.locator('#voicemail-section input[name="enabled"]');
    if (!(await enabled.isChecked())) {
      await enabled.check();
    }

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

    // Reload and confirm fields stuck.
    await page.goto(href);
    await expect(
      page.locator('#voicemail-section input[name="ring_timeout_seconds"]'),
    ).toHaveValue('30');
    await expect(
      page.locator('#voicemail-section input[name="retrieval_code"]'),
    ).toHaveValue('*99');

    // Cleanup: turn voicemail back off.
    await enabled.uncheck();
    await page.locator('#voicemail-section button[type="submit"]').first().click();
  });

  test('bad retrieval code is rejected with 400', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    // Use the request context to bypass HTML5 client-side validation;
    // we want to assert the server-side guard rail. "12345" is digits only
    // (no * or #), which the server rejects to avoid 7-digit-dial collision.
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

  test('list-row toggle works under dialup chrome', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    const number = href.split('/').pop() as string;

    // Ensure baseline is OFF regardless of what an earlier test left behind.
    await page.goto(href);
    if (await readEnabledOnDetail(page)) {
      await toggleVoicemailFromList(page, number);
    }

    await page.goto('/phones');
    const form = page
      .locator(`form[data-voicemail][data-line="${number}"]:visible`)
      .first();
    await expect(form).toBeVisible();

    // Off -> on: expect the muted "VM" chip to appear.
    await toggleVoicemailFromList(page, number);
    await expect(
      page
        .locator(
          `form[data-voicemail][data-line="${number}"]:visible .phone-voicemail__chip`,
        )
        .first(),
    ).toBeVisible();

    // Cleanup.
    await toggleVoicemailFromList(page, number);
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

  test('AM list shows the voicemail toggle in the roster meta', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    const number = href.split('/').pop() as string;

    await page.goto('/phones');
    const form = page
      .locator(`form[data-voicemail][data-line="${number}"]:visible`)
      .first();
    await expect(form).toBeVisible();
    await expect(form).toHaveClass(/am-voicemail/);
  });

  test('AM detail page renders the voicemail plate with all five fields', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);
    await expect(page.locator('.am-plate__label', { hasText: /voicemail/i })).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="enabled"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="ring_timeout_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="max_message_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="max_stored_messages"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="retrieval_code"]')).toBeVisible();
  });
});
