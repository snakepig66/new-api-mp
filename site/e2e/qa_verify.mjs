// Focused verification for the two fixes:
//   P1 admin users list: /console/ops/users must not show "数据加载失败"
//   P2 legacy /seller/listings* must redirect to /console/listings*
// Run: node e2e/qa_verify.mjs
import { chromium } from '@playwright/test'
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const SITE = 'D:/llmjar/new-api/site'
const E2E = path.join(SITE, 'e2e')
const RUN = path.join(E2E, '.tmp', 'verify')
const BIN = path.join(E2E, '.tmp', 'new-api-e2e.exe')
const PORT = 3102, VPORT = 5175
const BACK = `http://127.0.0.1:${PORT}`, FRONT = `http://127.0.0.1:${VPORT}`
const PASS = 'E2EPassw0rd!'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const procs = []
const stopAll = () => procs.forEach((p) => { try { p.kill() } catch {} })

async function waitFor(url, ms) {
  const end = Date.now() + ms
  while (Date.now() < end) { try { const r = await fetch(url); if (r.ok) return } catch {} ; await sleep(400) }
  throw new Error('timeout ' + url)
}
async function api(method, p, { token, body } = {}) {
  const h = { 'Content-Type': 'application/json' }
  if (token) h.Authorization = `Bearer ${token}`
  const r = await fetch(BACK + p, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) })
  const t = await r.text(); let j = null; try { j = t ? JSON.parse(t) : null } catch {}
  return { status: r.status, j }
}

async function main() {
  fs.rmSync(RUN, { recursive: true, force: true }); fs.mkdirSync(RUN, { recursive: true })
  const be = spawn(BIN, [], { cwd: RUN, env: { ...process.env, PORT: String(PORT), GIN_MODE: 'release', MP_ENABLED: 'true', MP_KEY_SECRET: 'verify', SESSION_SECRET: 'verify-s' }, stdio: ['ignore', 'ignore', 'ignore'] })
  procs.push(be)
  await waitFor(`${BACK}/api/status`, 60000)
  const s = await api('GET', '/api/setup')
  if (s.j?.data?.root_init === false) await api('POST', '/api/setup', { body: { username: 'admin', password: PASS, confirmPassword: PASS } })

  const fe = spawn(process.env.ComSpec || 'cmd.exe', ['/d', '/s', '/c', `bun run dev -- --host 127.0.0.1 --port ${VPORT} --strictPort`], { cwd: SITE, env: { ...process.env, MP_BACKEND: BACK }, stdio: ['ignore', 'ignore', 'ignore'] })
  procs.push(fe)
  await waitFor(FRONT, 120000)

  const login = await api('POST', '/api/user/login', { body: { username: 'admin', password: PASS } })
  const d = login.j.data
  const bundle = { accessToken: d.access_token, accessExpiresAt: d.access_expires_at ?? 0, sid: d.session?.sid ?? '', user: d.user ?? null }
  // onboard admin as seller so /console/listings is reachable
  for (const body of [{ agreement_version: 'v1' }, { real_name: 'QA', id_card_masked: '110101********1234' }, { alipay_account: 'qa@example.com' }]) {
    await api('POST', '/api/marketplace/seller/onboarding', { token: bundle.accessToken, body })
  }

  const browser = await chromium.launch({ headless: true })
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN' })
  await ctx.addInitScript((v) => window.localStorage.setItem('token-market.auth.v1', JSON.stringify(v)), bundle)
  const page = await ctx.newPage()
  const errs = []
  page.on('console', (m) => { if (m.type() === 'error') errs.push(m.text()) })
  page.on('response', (r) => { if (r.status() >= 400 && /\/api\//.test(r.url()) && !/auth\/refresh|setup|login/.test(r.url())) errs.push(`HTTP ${r.status()} ${r.url().replace(BACK, '')}`) })

  const results = {}
  // P1
  errs.length = 0
  await page.goto(FRONT + '/console/ops/users', { waitUntil: 'networkidle' })
  await page.waitForTimeout(1200)
  const usersText = await page.evaluate(() => document.body.innerText)
  results.p1_users_loadfail = usersText.includes('数据加载失败')
  results.p1_has_admin = usersText.includes('admin')
  results.p1_total = (usersText.match(/共\s*(\d+)\s*位用户/) || [])[1] || '?'
  results.p1_errors = [...errs]
  // /api/user/ direct
  const u = await api('GET', '/api/user/?p=1&page_size=20', { token: bundle.accessToken })
  results.p1_api_status = u.status

  // P2
  for (const [from, want] of [['/seller/listings', '/console/listings'], ['/seller/listings/new', '/console/listings/new'], ['/seller/records', '/console/records'], ['/seller/payouts', '/console/earnings']]) {
    await page.goto(FRONT + from, { waitUntil: 'networkidle' }).catch(() => {})
    await page.waitForTimeout(700)
    const final = page.url().replace(FRONT, '')
    results['p2_' + from] = { final, ok: final.startsWith(want) }
  }

  await browser.close()
  console.log('RESULTS ' + JSON.stringify(results, null, 2))
}

main().catch((e) => { console.error('FATAL', e); process.exit(1) }).finally(() => setTimeout(() => { stopAll(); process.exit(0) }, 300))
