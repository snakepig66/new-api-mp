#!/usr/bin/env node
/**
 * Starts a disposable marketplace backend for the Playwright suite.
 *
 * - builds the Go binary once (uses the shared Go build cache)
 * - wipes e2e/.tmp/run so every run starts from a clean SQLite database
 * - starts the binary with MP_ENABLED=true and a throwaway key secret
 * - initializes the root account through POST /api/setup
 * - stays in the foreground so Playwright's webServer keeps it alive
 *
 * No secrets are written to disk or printed: MP_KEY_SECRET is generated per
 * run in memory, and the account password comes from E2E_PASS.
 */
import { spawn, spawnSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const e2eDir = path.resolve(here, '..')
const siteDir = path.resolve(e2eDir, '..')
const repoDir = path.resolve(siteDir, '..')
const tmpDir = path.join(e2eDir, '.tmp')
const runDir = path.join(tmpDir, 'run')
const port = Number(process.env.E2E_BACKEND_PORT ?? 3100)
const base = `http://127.0.0.1:${port}`
const password = process.env.E2E_PASS

if (!password) {
  console.error('start-backend: E2E_PASS is required')
  process.exit(1)
}

fs.rmSync(runDir, { recursive: true, force: true })
fs.mkdirSync(runDir, { recursive: true })

const binary = path.join(tmpDir, process.platform === 'win32' ? 'new-api-e2e.exe' : 'new-api-e2e')

if (!process.env.E2E_SKIP_BUILD) {
  console.log('start-backend: building Go backend (go build)...')
  const build = spawnSync('go', ['build', '-o', binary, '.'], {
    cwd: repoDir,
    stdio: 'inherit',
    env: { ...process.env, CGO_ENABLED: '0' },
  })
  if (build.status !== 0) {
    console.error('start-backend: go build failed')
    process.exit(build.status ?? 1)
  }
}

const keySecret = `e2e-${Date.now()}-${Math.random().toString(36).slice(2)}-e2e-secret`
const child = spawn(binary, [], {
  cwd: runDir,
  stdio: ['ignore', 'pipe', 'pipe'],
  env: {
    ...process.env,
    PORT: String(port),
    GIN_MODE: 'release',
    MP_ENABLED: 'true',
    MP_KEY_SECRET: keySecret,
    SESSION_SECRET: `${keySecret}-session`,
  },
})

child.stdout.on('data', (chunk) => process.stdout.write(`backend: ${chunk}`))
child.stderr.on('data', (chunk) => process.stderr.write(`backend: ${chunk}`))

let stopping = false
function stop(code = 0) {
  if (stopping) return
  stopping = true
  try {
    child.kill()
  } catch {
    // already gone
  }
  setTimeout(() => process.exit(code), 200)
}

process.on('SIGINT', () => stop(0))
process.on('SIGTERM', () => stop(0))
process.on('exit', () => {
  try {
    child.kill()
  } catch {
    // already gone
  }
})
child.on('exit', (code) => {
  if (!stopping) {
    console.error(`start-backend: backend exited unexpectedly (${code})`)
    process.exit(code ?? 1)
  }
})

async function waitFor(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url)
      if (response.ok) return
    } catch {
      // not up yet
    }
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error(`start-backend: timed out waiting for ${url}`)
}

async function initializeRoot() {
  const statusResponse = await fetch(`${base}/api/setup`)
  const status = await statusResponse.json()
  // root_init=false means no root account has been created yet.
  if (status?.data?.root_init === false) {
    const response = await fetch(`${base}/api/setup`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password, confirmPassword: password }),
    })
    const body = await response.json().catch(() => null)
    if (!response.ok || body?.success === false) {
      throw new Error(`start-backend: root setup failed: ${body?.message ?? response.status}`)
    }
    console.log('start-backend: root account initialized')
  }
}

try {
  await waitFor(`${base}/api/status`, 60_000)
  await initializeRoot()
  const health = await fetch(`${base}/api/marketplace/status`).then((response) => response.json())
  if (!health?.enabled) {
    throw new Error('start-backend: marketplace is not enabled')
  }
  console.log(`start-backend: ready on ${base} (marketplace enabled)`)
} catch (error) {
  console.error(String(error))
  stop(1)
}
