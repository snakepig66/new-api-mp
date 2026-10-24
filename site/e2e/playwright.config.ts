import { defineConfig, devices } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const siteDir = path.resolve(here, '..')

/**
 * E2E configuration.
 *
 * Local mode (default): Playwright starts a disposable Go backend on :3100
 * (fresh SQLite DB under e2e/.tmp/run) and the Vite dev server on :5173.
 * Test fixtures may seed the local DB directly (buyer quota, forced live
 * listing, seller balance) because no valid upstream key exists in CI.
 *
 * External mode: set E2E_BASE_URL=https://<deployed-host> to run against a
 * deployed instance. Specifications that need local DB fixtures skip
 * themselves with an explicit reason; the public/responsive spec still runs.
 */
const externalBase = process.env.E2E_BASE_URL?.replace(/\/+$/, '')

if (!process.env.E2E_PASS) {
  throw new Error(
    'E2E_PASS is required: set it to the password used for every throwaway E2E account.',
  )
}

if (!externalBase) {
  process.env.E2E_LOCAL = '1'
  process.env.E2E_BACKEND_URL = 'http://127.0.0.1:3100'
  process.env.E2E_DB_PATH = path.join(here, '.tmp', 'run', 'one-api.db')
} else {
  delete process.env.E2E_LOCAL
  process.env.E2E_BACKEND_URL = externalBase
}

const backendUrl = process.env.E2E_BACKEND_URL!
const frontendUrl = externalBase ?? 'http://127.0.0.1:5173'
const local = !externalBase

export default defineConfig({
  testDir: './tests',
  outputDir: path.resolve(siteDir, 'test-results'),
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 180_000,
  expect: { timeout: 20_000 },
  reporter: [['list']],
  use: {
    baseURL: frontendUrl,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
    locale: 'zh-CN',
    actionTimeout: 20_000,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } },
    },
  ],
  webServer: local
    ? [
        {
          command: 'node e2e/support/start-backend.mjs',
          cwd: siteDir,
          url: `${backendUrl}/api/status`,
          reuseExistingServer: false,
          timeout: 300_000,
          stdout: 'ignore',
          stderr: 'pipe',
        },
        {
          command: 'bun run dev -- --host 127.0.0.1 --port 5173 --strictPort',
          cwd: siteDir,
          url: frontendUrl,
          reuseExistingServer: false,
          timeout: 120_000,
          env: { MP_BACKEND: backendUrl },
          stdout: 'ignore',
          stderr: 'pipe',
        },
      ]
    : undefined,
})
