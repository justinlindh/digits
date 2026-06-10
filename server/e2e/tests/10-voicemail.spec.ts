import { test, expect, Page, APIRequestContext } from '@playwright/test';

async function setTheme(request: APIRequestContext, theme: string) {
  const resp = await request.post('/settings/theme', {
    form: { theme },
  });
  if (!resp.ok()) throw new Error(`setTheme ${theme}: ${resp.status()}`);
}

async function firstPhoneHref(page: Page): Promise<string | null> {
  // The line roster lives on Overview now; /phones is pairing-only.
  await page.goto('/');
  const link = page.locator('a[href^="/phones/"]:not([href="/phones"])').first();
  if (!(await link.isVisible())) return null;
  return (await link.getAttribute('href'))!;
}

async function readEnabledOnDetail(page: Page): Promise<boolean> {
  return page.locator('#voicemail-section input[name="enabled"]').isChecked();
}

async function clickEnableCheckbox(page: Page) {
  const cb = page.locator('#voicemail-section input[name="enabled"]');
  // Arm the response wait before the click. Registering it afterwards races a
  // fast localhost response: the POST can complete before the listener attaches,
  // so waitForResponse never sees the event and times out.
  const wait = page.waitForResponse(
    (r) => r.url().includes('/voicemail-toggle') && r.request().method() === 'POST',
  );
  await cb.click();
  await wait;
  // waitForResponse resolves when response bytes arrive, before htmx applies
  // the swap. Wait for the swapped checkbox to settle so the next read reflects
  // the new state rather than the pre-swap DOM.
  await expect(cb).toBeAttached();
}

test.describe('Voicemail (intercom theme)', () => {
  test.beforeEach(async ({ page }) => {
    await setTheme(page.request, 'intercom').catch(() => undefined);
  });

  test('detail page renders voicemail section with enable checkbox and ring timeout', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    await expect(page.locator('h2.panel__title', { hasText: /answering machine/i })).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="enabled"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="ring_timeout_seconds"]')).toBeVisible();

    // Removed fields must not be present.
    await expect(page.locator('#voicemail-section input[name="max_stored_messages"]')).toHaveCount(0);
    await expect(page.locator('#voicemail-section input[name="retrieval_code"]')).toHaveCount(0);
    await expect(page.locator('#voicemail-section input[name="max_message_seconds"]')).toHaveCount(0);

    // No accordion wrapper.
    await expect(page.locator('#voicemail-section details')).toHaveCount(0);
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

  test('saving ring timeout persists across reload', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    // Make sure voicemail is on so the fields aren't disabled.
    if (!(await readEnabledOnDetail(page))) {
      await clickEnableCheckbox(page);
    }

    await page.locator('#voicemail-section input[name="ring_timeout_seconds"]').fill('30');

    const wait = page.waitForResponse(
      (r) => r.url().endsWith('/voicemail') && r.request().method() === 'POST',
    );
    await page.locator('#voicemail-section button[type="submit"]').first().click();
    const resp = await wait;
    expect(resp.status()).toBe(200);

    // Reload, confirm value stuck.
    await page.goto(href);
    await expect(
      page.locator('#voicemail-section input[name="ring_timeout_seconds"]'),
    ).toHaveValue('30');

    // Cleanup: voicemail off.
    await clickEnableCheckbox(page);
  });

  test('pairing page does NOT show any line voicemail UI', async ({ page }) => {
    await page.goto('/phones');
    if (page.url().includes('/auth/login') || page.url().includes('/onboard')) {
      test.skip(true, 'No authenticated session or needs onboarding');
      return;
    }
    // /phones is pairing-only now: no line roster, so no per-line voicemail
    // controls or unheard chips should render here. The unheard chip lives on
    // the Overview line cards instead.
    await expect(page.locator('form[data-voicemail]')).toHaveCount(0);
    await expect(page.locator('.phone-voicemail')).toHaveCount(0);
    await expect(page.locator('.chip--msg')).toHaveCount(0);
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

  test('detail page renders voicemail section under dialup chrome', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    await expect(page.locator('#voicemail-section')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="enabled"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="ring_timeout_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section details')).toHaveCount(0);
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

  test('AM detail page renders voicemail controls inline', async ({ page }) => {
    const href = await firstPhoneHref(page);
    if (!href) {
      test.skip(true, 'No phones registered');
      return;
    }
    await page.goto(href);

    await expect(page.locator('.am-plate__label', { hasText: /answering machine/i })).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="enabled"]')).toBeVisible();
    await expect(page.locator('#voicemail-section input[name="ring_timeout_seconds"]')).toBeVisible();
    await expect(page.locator('#voicemail-section details')).toHaveCount(0);
  });
});
