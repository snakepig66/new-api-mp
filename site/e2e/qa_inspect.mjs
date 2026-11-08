// Full-site QA inspection: boots a disposable backend + vite, sweeps every route
// at desktop and mobile, captures console/page/HTTP errors, overflow, redirects
// and screenshots. Run: node qa_inspect.mjs
import { chromium } from '@playwright/test'
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const ROOT = 'D:/llmjar/new-api'
const SITE = path.join(ROOT, 'site')
const E2E = path.join(SITE, 'e2e')
const TMP = path.join(E2E, '.tmp', 'inspect')
const RUN = path.join(TMP, 'run')
const BIN = path.join(E2E, '.tmp', 'new-api-e2e.exe')
const PORT = 3101
const VPORT = 5174
const BACK = `http://127.0.0.1:${PORT}`
const FRONT = `http://127.0.0.1:${VPORT}`
const PASS = 'E2EPassw0rd!'
const SHOTS = path.join(ROOT, 'e2e', 'qa-shots')
const REPORT = path.join(ROOT, 'e2e', 'qa-report.json')

fs.rmSync(SHOTS, { recursive: true, force: true })
fs.mkdirSync(SHOTS, { recursive: true })
fs.rmSync(RUN, { recursive: true, force: true })
fs.mkdirSync(RUN, { recursive: true })

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
async function waitFor(url, ms, label) {
  const deadline = Date.now() + ms
  while (Date.now() < deadline) {
    try { const r = await fetch(url); if (r.ok) return } catch {}
    await sleep(400)
  }
  throw new Error(`timeout waiting for ${label || url}`)
}
async function api(method, p, { token, body } = {}) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(BACK + p, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const text = await res.text()
  let json = null
  try { json = text ? JSON.parse(text) : null } catch {}
  return { status: res.status, json, text }
}

const procs = []
function stopAll() {
  for (const p of procs) { try { p.kill() } catch {} }
}

async function main() {
  if (!fs.existsSync(BIN)) throw new Error(`backend binary missing: ${BIN} (run the e2e suite first)`)

  const secret = `inspect-${Date.now()}`
  const be = spawn(BIN, [], {
    cwd: RUN,
    env: { ...process.env, PORT: String(PORT), GIN_MODE: 'release', MP_ENABLED: 'true', MP_KEY_SECRET: secret, SESSION_SECRET: secret + '-s' },
    stdio: ['ignore', 'ignore', 'ignore'],
  })
  procs.push(be)
  await waitFor(`${BACK}/api/status`, 60000, 'backend')

  const setup = await api('GET', '/api/setup')
  if (setup.json?.data?.root_init === false) {
    await api('POST', '/api/setup', { body: { username: 'admin', password: PASS, confirmPassword: PASS } })
  }

  const fe = spawn(process.env.ComSpec || 'cmd.exe', ['/d', '/s', '/c', `bun run dev -- --host 127.0.0.1 --port ${VPORT} --strictPort`], {
    cwd: SITE, env: { ...process.env, MP_BACKEND: BACK }, stdio: ['ignore', 'ignore', 'ignore'],
  })
  procs.push(fe)
  await waitFor(FRONT, 120000, 'vite')

  // --- accounts ---
  async function ensureAccount(username) {
    await api('POST', '/api/user/register', { body: { username, password: PASS, email: `${username}@example.com` } })
    return login(username)
  }
  async function login(username) {
    const r = await api('POST', '/api/user/login', { body: { username, password: PASS } })
    const d = r.json?.data
    if (!d?.access_token) throw new Error('login failed ' + username + ' ' + r.text.slice(0, 200))
    return { accessToken: d.access_token, accessExpiresAt: d.access_expires_at ?? 0, sid: d.session?.sid ?? '', user: d.user ?? null }
  }
  async function onboard(token) {
    for (const body of [{ agreement_version: 'v1' }, { real_name: 'QA User', id_card_masked: '110101********1234' }, { alipay_account: 'qa@example.com' }]) {
      await api('POST', '/api/marketplace/seller/onboarding', { token, body })
    }
  }

  const adminBundle = await login('admin')
  await onboard(adminBundle.accessToken)
  const sellerBundle = await ensureAccount('qa_seller')
  await onboard(sellerBundle.accessToken)
  const buyerBundle = await ensureAccount('qa_buyer')

  const browser = await chromium.launch({ headless: true })
  const findings = []

  function attach(page, ctxLabel, route) {
    const errors = []
    page.on('console', (m) => { if (m.type() === 'error') errors.push({ kind: 'console', text: m.text() }) })
    page.on('pageerror', (e) => errors.push({ kind: 'pageerror', text: e.message, stack: e.stack }))
    page.on('response', (r) => {
      const s = r.status(); const u = r.url()
      if (s >= 400 && /\/api\//.test(u) && !/\/api\/(user\/login|setup|user\/register|status|marketplace\/status)/.test(u)) {
        errors.push({ kind: 'http', text: `HTTP ${s} ${u.replace(BACK, '')}` })
      }
    })
    return errors
  }

  async function inject(page, bundle) {
    await page.addInitScript((val) => { window.localStorage.setItem('token-market.auth.v1', JSON.stringify(val)) }, bundle)
  }

  async function sweep(label, routes, bundle, { mobileShot = false } = {}) {
    for (const route of routes) {
      const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN' })
      const page = await ctx.newPage()
      if (bundle) await inject(page, bundle)
      const errors = attach(page, label, route)
      let navErr = null
      try {
        await page.goto(FRONT + route, { waitUntil: 'networkidle', timeout: 30000 })
      } catch (e) { navErr = String(e.message || e) }
      await page.waitForTimeout(500)
      const info = await page.evaluate(() => {
        const doc = document.documentElement
        const bodyText = (document.body.innerText || '').slice(0, 4000)
        const heading = document.querySelector('main :is(h1,h2)')?.textContent?.trim() || ''
        // detect elements wider than viewport
        let wide = []
        for (const el of document.querySelectorAll('body *')) {
          const r = el.getBoundingClientRect()
          if (r.width > window.innerWidth + 2 && r.height > 20) {
            wide.push({ tag: el.tagName, cls: String(el.className).slice(0, 80), w: Math.round(r.width) })
          }
        }
        return {
          title: document.title,
          heading,
          url: location.pathname + location.search,
          docW: doc.scrollWidth,
          bodyW: document.body.scrollWidth,
          vw: window.innerWidth,
          textLen: bodyText.length,
          hasErrorWords: /出错了|Something went wrong|Uncaught|加载失败|页面不存在|404|500/.test(bodyText),
          sample: bodyText.slice(0, 240).replace(/\s+/g, ' '),
          wide: wide.slice(0, 4),
        }
      })
      const slug = (label + '_' + route.replace(/[^a-z0-9]+/gi, '_')).replace(/^_+|_+$/g, '')
      let shot = ''
      try {
        shot = path.join(SHOTS, `d_${slug}.png`)
        await page.screenshot({ path: shot, fullPage: true })
      } catch { shot = '' }

      const overflow = info.docW > info.vw + 1
      findings.push({ label, route, finalUrl: info.url, heading: info.heading, title: info.title, overflow, docW: info.docW, vw: info.vw, textLen: info.textLen, hasErrorWords: info.hasErrorWords, wide: info.wide, navErr, errors, screenshot: shot, sample: info.sample })

      if (mobileShot) {
        await page.setViewportSize({ width: 390, height: 844 })
        await page.waitForTimeout(200)
        const m = await page.evaluate(() => ({ docW: document.documentElement.scrollWidth, bodyW: document.body.scrollWidth, vw: window.innerWidth }))
        const mob = await page.evaluate(() => ({ docW: document.documentElement.scrollWidth, vw: window.innerWidth }))
        findings[findings.length - 1].mobileOverflow = m.docW > m.vw + 1
        findings[findings.length - 1].mobileDocW = m.docW
        try { await page.screenshot({ path: path.join(SHOTS, `m_${slug}.png`), fullPage: true }) } catch {}
      }

      const flag = (errors.length ? 'E' : '.') + (overflow ? 'O' : '.') + (info.hasErrorWords ? 'X' : '.')
      console.log(`[${flag}] ${label} ${route} -> ${info.url} h="${info.heading}" docW=${info.docW}/${info.vw} err=${errors.length}`)
      await ctx.close()
    }
  }

  const PUBLIC = ['/', '/plaza', '/models', '/models/deepseek-flash', '/docs', '/login', '/register', '/forgot-password', '/agreement', '/privacy', '/leaderboard', '/share', '/usage-logs']
  const CONSOLE = ['/console', '/console/keys', '/console/records', '/console/wallet', '/console/onboarding', '/console/listings', '/console/listings/new', '/console/earnings', '/console/earnings/payouts', '/console/account']
  const SELLER = ['/seller', '/seller/onboarding', '/seller/payouts', '/seller/records', '/seller/listings']
  const OPS = ['/console/ops', '/console/ops/channels', '/console/ops/metrics', '/console/ops/models', '/console/ops/plaza', '/console/ops/pricing', '/console/ops/pricing/per-call', '/console/ops/request-policies', '/console/ops/review', '/console/ops/review/payouts', '/console/ops/review/trust-pool', '/console/ops/risk', '/console/ops/settings', '/console/ops/tools', '/console/ops/tools/reversal', '/console/ops/tools/risk', '/console/ops/trace', '/console/ops/trust-pool', '/console/ops/users']
  const ADMIN = ['/admin', '/admin/channel-models', '/admin/holidays', '/admin/listings', '/admin/payouts', '/admin/per-call-prices', '/admin/pool', '/admin/prices', '/admin/tools']

  await sweep('public', PUBLIC, null, { mobileShot: true })
  await sweep('console', CONSOLE, buyerBundle, { mobileShot: true })
  await sweep('seller', SELLER, sellerBundle)
  await sweep('ops', OPS, adminBundle)
  await sweep('admin', ADMIN, adminBundle)

  // --- 404 / unknown route ---
  {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    const page = await ctx.newPage()
    const errors = attach(page, 'misc', '/no-such-route')
    await page.goto(FRONT + '/no-such-route-xyz', { waitUntil: 'networkidle' }).catch(() => {})
    await page.waitForTimeout(400)
    const t = await page.evaluate(() => ({ heading: document.querySelector('main :is(h1,h2)')?.textContent?.trim() || '', text: (document.body.innerText || '').slice(0, 300).replace(/\s+/g, ' ') }))
    findings.push({ label: 'misc', route: '/no-such-route-xyz', finalUrl: page.url().replace(FRONT, ''), heading: t.heading, hasErrorWords: true, errors, sample: t.text })
    await page.screenshot({ path: path.join(SHOTS, 'd_misc_404.png'), fullPage: true })
    await ctx.close()
  }

  await browser.close()
  fs.writeFileSync(REPORT, JSON.stringify(findings, null, 2))
  console.log('\n=== SUMMARY ===')
  console.log('routes checked:', findings.length)
  console.log('with console/page/http errors:', findings.filter((f) => f.errors && f.errors.length).length)
  console.log('desktop overflow:', findings.filter((f) => f.overflow).map((f) => f.label + f.route).join(', ') || 'none')
  console.log('mobile overflow:', findings.filter((f) => f.mobileOverflow).map((f) => f.label + f.route).join(', ') || 'none')
  console.log('report:', REPORT)
}

main().catch((e) => { console.error('FATAL', e); stopAll(); process.exit(1) }).finally(() => { setTimeout(() => { stopAll(); process.exit(0) }, 300) })
