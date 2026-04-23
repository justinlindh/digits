/**
 * 10-call-live.spec.ts -- /call/live observation deck
 *
 * Seeds a call via the DEV_MODE test-harness endpoint and verifies:
 *   - The deck page renders with both endpoint cards
 *   - The "End call" / "Disconnect" button opens the confirm dialog
 *   - After confirming, the call transitions to terminal state
 *   - Theme-specific chrome renders correctly (intercom vs dialup)
 *
 * Requires a server running with DEV_MODE=true and the dev database
 * seeded by `make dev-seed` (or `make dev-up`). Tests skip gracefully
 * when the server is unavailable.
 *
 * Dev-seed users (server/cmd/devseed/main.go):
 *   dev@digits.local       theme=dialup   lines: 2480001, 2480002, 2480003
 *   grandma@digits.local   theme=intercom lines: 5550001
 */

import { test, expect, Page } from '@playwright/test';
import { isServerUp, devLogin } from './helpers';

test.beforeEach(async ({}, testInfo) => {
  const up = await isServerUp();
  testInfo.skip(!up, 'Dev server not running');
});

/**
 * Seed an active call via the DEV_MODE harness endpoint.
 * Returns the new call ID.
 */
async function seedCall(page: Page, caller: string, callee: string): Promise<number> {
  const r = await page.request.post('/test-harness/start-call', {
    data: { caller, callee },
  });
  if (!r.ok()) {
    throw new Error(`seed-call failed: ${r.status()} ${await r.text()}`);
  }
  const body = await r.json();
  return body.id as number;
}

// ---------------------------------------------------------------------------
// Intercom theme (grandma@digits.local owns line 5550001; use two of her
// lines — but grandma only has one line. For the deck we need two distinct
// endpoints, so we use one line from grandma (5550001) and one from the
// primary dialup user (2480001). The test authenticates as grandma, who owns
// 5550001 and therefore satisfies the ownership check.
// ---------------------------------------------------------------------------

test.describe('call-live — intercom theme', () => {
  test('renders deck with two endpoint cards', async ({ page }) => {
    const ok = await devLogin(page, 'grandma@digits.local');
    if (!ok) {
      test.skip(true, 'dev-session not available or user needs onboarding');
      return;
    }

    const callID = await seedCall(page, '5550001', '2480001');
    await page.goto(`/call/live/${callID}`);

    // Page must not redirect to login.
    expect(page.url()).not.toContain('/auth/login');

    // Title heading is visible.
    await expect(page.locator('.deck-title')).toBeVisible();

    // Two endpoint cards (caller + callee).
    await expect(page.locator('.deck-card')).toHaveCount(2);

    // The active-call kill button should say "End call" for intercom theme.
    await expect(page.locator('.deck-kill')).toContainText('End call');

    // Intercom layout: rail header, not dialup window.
    await expect(page.locator('header.rail')).toBeVisible();
  });

  test('End call opens confirm dialog and transitions to terminal state', async ({ page }) => {
    const ok = await devLogin(page, 'grandma@digits.local');
    if (!ok) {
      test.skip(true, 'dev-session not available or user needs onboarding');
      return;
    }

    const callID = await seedCall(page, '5550001', '2480002');
    await page.goto(`/call/live/${callID}`);
    expect(page.url()).not.toContain('/auth/login');

    // Open confirm dialog.
    await page.locator('.deck-kill').click();
    await expect(page.locator('#deck-confirm')).toBeVisible();

    // Wait for the disconnect POST to complete after clicking confirm.
    const disconnectDone = page.waitForResponse(
      (r) => r.url().includes('/disconnect') && r.request().method() === 'POST',
    );
    await page.locator('.deck-confirm__go').click();
    await disconnectDone;

    // Reload to get server-rendered terminal state (.deck-ended-chip).
    await page.reload();
    await expect(
      page.locator('.deck-ended-chip, .deck-ended'),
    ).toBeVisible({ timeout: 5000 });

    // Kill button must be gone in terminal state.
    await expect(page.locator('.deck-kill')).toHaveCount(0);
  });
});

// ---------------------------------------------------------------------------
// Dialup theme (dev@digits.local owns lines 2480001..2480003)
// ---------------------------------------------------------------------------

test.describe('call-live — dialup theme', () => {
  test('renders MODEM chrome and Disconnect button', async ({ page }) => {
    const ok = await devLogin(page, 'dev@digits.local');
    if (!ok) {
      test.skip(true, 'dev-session not available or user needs onboarding');
      return;
    }

    const callID = await seedCall(page, '2480001', '2480002');
    await page.goto(`/call/live/${callID}`);
    expect(page.url()).not.toContain('/auth/login');

    // Dialup layout chrome is present.
    await expect(page.locator('.dialup-window')).toBeVisible();

    // Dialup theme uses "◉ Disconnect" for the kill button.
    await expect(page.locator('.deck-kill')).toContainText('Disconnect');

    // Two endpoint cards.
    await expect(page.locator('.deck-card')).toHaveCount(2);
  });

  test('Disconnect opens confirm dialog with dialup copy', async ({ page }) => {
    const ok = await devLogin(page, 'dev@digits.local');
    if (!ok) {
      test.skip(true, 'dev-session not available or user needs onboarding');
      return;
    }

    const callID = await seedCall(page, '2480002', '2480003');
    await page.goto(`/call/live/${callID}`);
    expect(page.url()).not.toContain('/auth/login');

    await page.locator('.deck-kill').click();
    await expect(page.locator('#deck-confirm')).toBeVisible();

    // Dialup confirm dialog says "Disconnect call?" not "End this call?"
    await expect(page.locator('.deck-confirm__title')).toContainText('Disconnect call?');
  });
});
