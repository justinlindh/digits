import { defineConfig, devices } from '@playwright/test';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false, // auth state is shared; run serially by default
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: [['html', { open: 'never' }], ['list']],

  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    // Don't follow cross-origin redirects blindly in assertions
    navigationTimeout: 10_000,
    actionTimeout: 10_000,
  },

  projects: [
    // Setup project: create authenticated session state
    {
      name: 'setup',
      testMatch: '**/auth.setup.ts',
    },
    // Main test project: reuse authenticated state
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        storageState: '.auth/user.json',
      },
      dependencies: ['setup'],
    },
  ],

  // No webServer block — tests skip gracefully when server is unavailable
});
