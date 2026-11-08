import { defineConfig } from '@playwright/test'

import e2eConfig from './e2e/playwright.config'

/**
 * Root entry point so `bunx playwright test` works from site/.
 * The suite itself lives in site/e2e/ (config + tests).
 */
export default defineConfig({
  ...e2eConfig,
  testDir: './e2e/tests',
})
