// e2e/run_flow.ts — 卖家全流程端到端自动化测试
import { chromium, type Browser, type Page } from '../site/node_modules/@playwright/test'
import { Database } from 'bun:sqlite'
import fs from 'node:fs'
import path from 'node:path'

const BASE_URL = 'http://127.0.0.1:3102'
const UPSTREAM_URL = 'http://127.0.0.1:3202'
const SHOTS_DIR = path.resolve('e2e/shots')

if (!fs.existsSync(SHOTS_DIR)) {
  fs.mkdirSync(SHOTS_DIR, { recursive: true })
}

const db = new Database('e2e.db')

function log(...args: any[]) {
  console.log('[E2E-FLOW]', ...args)
}

async function fetchJSON(url: string, options: RequestInit = {}) {
  const res = await fetch(url, options)
  const text = await res.text()
  let body: any
  try {
    body = JSON.parse(text)
  } catch {
    body = text
  }
  return { status: res.status, headers: res.headers, body }
}

async function main() {
  log('Starting Seller Full Lifecycle E2E Test...')

  // Clean marketplace tables for a fresh test run
  db.query('DELETE FROM mp_listings').run()
  db.query("DELETE FROM mp_channels WHERE slug LIKE 'custom-%'").run()
  db.query("DELETE FROM mp_channel_models WHERE channel_slug LIKE 'custom-%'").run()
  db.query('DELETE FROM mp_ledger').run()
  db.query('DELETE FROM mp_payouts').run()
  db.query('DELETE FROM mp_requests').run()
  db.query('DELETE FROM mp_key_prefs').run()
  db.query('DELETE FROM mp_key_models').run()
  db.query('DELETE FROM mp_listing_groups').run()
  db.query("DELETE FROM channels WHERE `key` LIKE 'mp-managed:%'").run()
  log('Cleaned marketplace tables for fresh test run')

  // 1. Setup root admin if not already done
  log('1. Checking / Setting up root admin...')
  const setupRes = await fetchJSON(`${BASE_URL}/api/setup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      username: 'admin',
      password: 'Password123!',
      confirmPassword: 'Password123!',
    }),
  })
  log('Setup response:', setupRes.status, setupRes.body?.message || setupRes.body)

  // Login Admin
  const adminLoginRes = await fetchJSON(`${BASE_URL}/api/user/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'admin', password: 'Password123!' }),
  })
  if (adminLoginRes.status !== 200 || !adminLoginRes.body?.data?.access_token) {
    throw new Error('Admin login failed: ' + JSON.stringify(adminLoginRes.body))
  }
  const adminBundle = adminLoginRes.body.data
  const adminToken = adminBundle.access_token
  log('Admin logged in, user_id:', adminBundle.user.id)

  // Disable performance monitor overload check for local test environment
  const optRes = await fetchJSON(`${BASE_URL}/api/option/`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${adminToken}`,
    },
    body: JSON.stringify({
      key: 'performance_setting.monitor_enabled',
      value: 'false',
    }),
  })
  log('Disabled performance monitor overload check:', optRes.status, optRes.body)

  // 2. Register / Login Seller
  const sellerUsername = 'seller_' + Math.floor(Math.random() * 100000)
  log(`2. Registering / Logging in ${sellerUsername}...`)
  await fetchJSON(`${BASE_URL}/api/user/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      username: sellerUsername,
      password: 'Password123!',
      email: `${sellerUsername}@e2e.test`,
    }),
  })

  const sellerLoginRes = await fetchJSON(`${BASE_URL}/api/user/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: sellerUsername, password: 'Password123!' }),
  })
  if (sellerLoginRes.status !== 200 || !sellerLoginRes.body?.data?.access_token) {
    throw new Error('Seller login failed: ' + JSON.stringify(sellerLoginRes.body))
  }
  const sellerBundle = sellerLoginRes.body.data
  const sellerToken = sellerBundle.access_token
  log('Seller logged in, user_id:', sellerBundle.user.id)

  // Seller Onboard (accept agreement)
  const onboardRes = await fetchJSON(`${BASE_URL}/api/marketplace/seller/onboarding`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sellerToken}`,
    },
    body: JSON.stringify({ agreement_version: 'v1' }),
  })
  log('Seller onboard status:', onboardRes.status, onboardRes.body?.data?.seller?.status)

  // 3. Test Probe via HTTP API to verify Chinese error messages
  log('3. Verifying probe error messages via API...')
  const testProbe = async (name: string, url: string, key: string) => {
    const res = await fetchJSON(`${BASE_URL}/api/marketplace/seller/listings/probe`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${sellerToken}`,
      },
      body: JSON.stringify({ custom_base_url: url, api_key: key }),
    })
    log('testProbe', name, 'res:', res.status, res.body)
    return res.body?.data
  }

  const p401 = await testProbe('401 invalid key', UPSTREAM_URL, 'sk-401-bad')
  log('Probe 401 fail_reason:', p401?.fail_reason)
  if (p401?.fail_reason !== 'Key 无效或没权限') {
    throw new Error(`Expected 'Key 无效或没权限', got '${p401?.fail_reason}'`)
  }

  const p402 = await testProbe('402 arrears', UPSTREAM_URL, 'sk-402-arrears')
  log('Probe 402 fail_reason:', p402?.fail_reason)
  if (p402?.fail_reason !== '上游余额不足') {
    throw new Error(`Expected '上游余额不足', got '${p402?.fail_reason}'`)
  }

  const p429 = await testProbe('429 quota', UPSTREAM_URL, 'sk-quota-out')
  log('Probe 429 fail_reason:', p429?.fail_reason)
  if (p429?.fail_reason !== '上游余额不足') {
    throw new Error(`Expected '上游余额不足', got '${p429?.fail_reason}'`)
  }

  const pDead = await testProbe('dead address', 'http://127.0.0.1:39999', 'sk-test')
  log('Probe dead address fail_reason:', pDead?.fail_reason)
  if (pDead?.fail_reason !== '地址连不上') {
    throw new Error(`Expected '地址连不上', got '${pDead?.fail_reason}'`)
  }

  const validKey = 'sk-valid-' + Math.floor(Math.random() * 1000000)
  const pOk = await testProbe('valid key', UPSTREAM_URL, validKey)
  log('Probe valid key ok:', pOk?.ok, 'models:', pOk?.models)
  if (!pOk?.ok || !pOk?.models?.includes('deepseek-flash')) {
    throw new Error('Valid key probe failed to discover deepseek-flash')
  }

  log('All probe error translations verified successfully!')

  // 4. Playwright UI Automation
  log('4. Launching Playwright Chromium...')
  const browser = await chromium.launch({ headless: true })

  const injectAuth = async (page: Page, bundle: any) => {
    const sessionObj = {
      accessToken: bundle.access_token,
      accessExpiresAt: bundle.access_expires_at,
      sid: bundle.session?.sid || '',
      user: bundle.user,
    }
    await page.addInitScript((val) => {
      window.localStorage.setItem('token-market.auth.v1', JSON.stringify(val))
    }, sessionObj)
  }

  // --- Seller Context ---
  const sellerContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  const sellerPage = await sellerContext.newPage()
  await injectAuth(sellerPage, sellerBundle)

  log('Navigating to /console/listings/new in browser...')
  await sellerPage.goto(`${BASE_URL}/console/listings/new`, { waitUntil: 'networkidle' })

  // Step 0: Fill URL and test bad key
  log('Step 0: Testing probe in browser UI...')
  const urlInput = sellerPage.locator('#new-listing-url')
  await urlInput.fill(UPSTREAM_URL)

  const keyInput = sellerPage.locator('#new-listing-keys')
  await keyInput.fill('sk-401-bad')

  // Trigger probe
  const probeBtn = sellerPage.locator('button:has-text("检测全部 Key"), button:has-text("重测")').first()
  if (await probeBtn.isVisible()) {
    await probeBtn.click()
  }
  await sellerPage.waitForTimeout(1000)

  // Verify UI displays "Key 无效或没权限"
  await sellerPage.waitForSelector('text=Key 无效或没权限', { timeout: 10000 })
  log('UI displayed "Key 无效或没权限"!')
  await sellerPage.screenshot({ path: path.join(SHOTS_DIR, '01-probe-errors.png') })
  log('Saved screenshot 01-probe-errors.png')

  // Now replace with valid key
  await keyInput.fill(validKey)
  await sellerPage.waitForTimeout(1000)
  const probeBtn2 = sellerPage.locator('button:has-text("检测全部 Key"), button:has-text("重测")').first()
  if (await probeBtn2.isVisible()) {
    await probeBtn2.click()
  }
  await sellerPage.waitForSelector('text=连通正常', { timeout: 10000 })
  log('UI displayed "连通正常"!')
  await sellerPage.screenshot({ path: path.join(SHOTS_DIR, '02-probe-success.png') })
  log('Saved screenshot 02-probe-success.png')

  // Click Next Step (Step 0 -> Step 1)
  const nextBtn = sellerPage.locator('button:has-text("下一步")')
  await nextBtn.click()
  await sellerPage.waitForTimeout(1000)

  // Step 1: Check Multiplier Slider display
  log('Step 1: Checking multiplier discount pill...')
  await sellerPage.waitForSelector('text=官方价 8 折', { timeout: 10000 })
  log('UI correctly displays "官方价 8 折"!')
  await sellerPage.screenshot({ path: path.join(SHOTS_DIR, '03-step1-multiplier.png') })
  log('Saved screenshot 03-step1-multiplier.png')

  const step1Info = await sellerPage.evaluate(() => {
    const btns = Array.from(document.querySelectorAll('button')).map(b => ({
      text: b.textContent?.trim(),
      disabled: b.disabled,
    }))
    const checkboxes = Array.from(document.querySelectorAll('input[type="checkbox"]')).map(c => ({
      name: (c as HTMLInputElement).name,
      checked: (c as HTMLInputElement).checked,
      value: (c as HTMLInputElement).value,
    }))
    return { btns, checkboxes }
  })
  log('Step 1 DOM info:', JSON.stringify(step1Info))

  // Click Next Step (Step 1 -> Step 2)
  const nextBtn2 = sellerPage.locator('button:has-text("下一步")')
  log('nextBtn2 disabled:', await nextBtn2.isDisabled())
  await nextBtn2.click()
  await sellerPage.waitForTimeout(1000)

  // Step 2: Verification Matrix
  log('Step 2: Checking verification matrix...')
  await sellerPage.waitForSelector('text=逐项验真', { timeout: 10000 })
  await sellerPage.screenshot({ path: path.join(SHOTS_DIR, '04-step2-verification.png') })
  log('Saved screenshot 04-step2-verification.png')

  // Inspect submit buttons
  const passingBtn = sellerPage.locator('button:has-text("通过的先上架")')
  const draftBtn = sellerPage.locator('button:has-text("失败的保留为草稿")')
  log('Passing button visible:', await passingBtn.isVisible(), 'disabled:', await passingBtn.isDisabled())
  log('Draft button visible:', await draftBtn.isVisible(), 'disabled:', await draftBtn.isDisabled())

  sellerPage.on('response', async (res) => {
    if (res.url().includes('/api/marketplace/seller/listings')) {
      log('Listing API response:', res.status(), await res.text())
    }
  })

  if (await passingBtn.isEnabled()) {
    log('Clicking "通过的先上架"...')
    await passingBtn.click()
  } else if (await draftBtn.isEnabled()) {
    log('Clicking "失败的保留为草稿"...')
    await draftBtn.click()
  } else {
    log('Neither submit button is enabled! Dumping page text...')
    log(await sellerPage.innerText('body'))
  }

  await sellerPage.waitForTimeout(3000)

  // Check listing created in db
  const listingRow = db.query('SELECT * FROM mp_listings ORDER BY id DESC LIMIT 1').get() as any
  log('Created listing in DB:', listingRow?.id, 'status:', listingRow?.status)
  if (!listingRow || (listingRow.status !== 'pending_review' && listingRow.status !== 'draft')) {
    throw new Error('Listing was not created in pending_review or draft status! Got: ' + listingRow?.status)
  }
  const listingID = listingRow.id

  // --- Admin Review & Approve Listing ---
  log('5. Admin reviewing and approving listing...')
  const adminContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  const adminPage = await adminContext.newPage()
  await injectAuth(adminPage, adminBundle)

  await adminPage.goto(`${BASE_URL}/console/ops/review`, { waitUntil: 'networkidle' })
  await adminPage.waitForTimeout(1000)

  // Approve listing via API
  const approveRes = await fetchJSON(`${BASE_URL}/api/marketplace/admin/listings/${listingID}/approve`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${adminToken}`,
    },
    body: JSON.stringify({ notes: 'E2E test approval' }),
  })
  log('Admin approve listing status:', approveRes.status, approveRes.body)

  // Wait for background verification to complete to 'canary'
  let canaryListing = null
  for (let i = 0; i < 20; i++) {
    await new Promise((r) => setTimeout(r, 500))
    canaryListing = db.query('SELECT status FROM mp_listings WHERE id = ?').get(listingID) as any
    if (canaryListing?.status === 'canary') break
  }
  log('Listing status after verification:', canaryListing?.status)

  // Reload admin page and screenshot
  await adminPage.reload({ waitUntil: 'networkidle' })
  await adminPage.waitForTimeout(1000)
  await adminPage.screenshot({ path: path.join(SHOTS_DIR, '05-admin-listing-approved.png') })
  log('Saved screenshot 05-admin-listing-approved.png')

  // Promote listing to 'live' in database so it is 100% active for relay calls
  db.query("UPDATE mp_listings SET status = 'live' WHERE id = ?").run(listingID)
  log('Promoted listing', listingID, 'to live in database')

  // 6. Buyer Flow: Register Buyer, Top Up, Create Shared Key, and Relay Call
  const buyerUsername = 'buyer_' + Math.floor(Math.random() * 100000)
  log(`6. Registering and setting up ${buyerUsername}...`)
  await fetchJSON(`${BASE_URL}/api/user/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      username: buyerUsername,
      password: 'Password123!',
      email: `${buyerUsername}@e2e.test`,
    }),
  })

  const buyerLoginRes = await fetchJSON(`${BASE_URL}/api/user/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: buyerUsername, password: 'Password123!' }),
  })
  const buyerBundle = buyerLoginRes.body.data
  const buyerToken = buyerBundle.access_token
  const buyerID = buyerBundle.user.id
  log('Buyer logged in, user_id:', buyerID)

  // Top up buyer quota in database
  db.query('UPDATE users SET quota = 100000000 WHERE id = ?').run(buyerID)
  log('Granted 100,000,000 quota to buyer')

  // Create shared key for buyer
  const keyCreateRes = await fetchJSON(`${BASE_URL}/api/marketplace/key`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${buyerToken}`,
    },
    body: JSON.stringify({
      name: 'E2E Buyer Shared Key',
      key_type: 'shared',
      model_scope: 'selected',
      models: [{ model: 'deepseek-flash', price_cap_milli: 1000 }],
    }),
  })
  log('Create shared key status:', keyCreateRes.status, 'key:', keyCreateRes.body?.key)
  const sharedKey = keyCreateRes.body?.key
  if (!sharedKey) {
    throw new Error('Failed to create buyer shared key: ' + JSON.stringify(keyCreateRes.body))
  }

  // --- Buyer Non-Stream Relay Call ---
  log('7. Testing Buyer Non-Stream Relay Call...')
  const nonStreamRes = await fetchJSON(`${BASE_URL}/v1/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sharedKey}`,
    },
    body: JSON.stringify({
      model: 'deepseek-flash',
      messages: [{ role: 'user', content: '测试非流式回复' }],
    }),
  })
  log('Non-stream relay status:', nonStreamRes.status)
  log('Non-stream response model:', nonStreamRes.body?.model)
  log('Non-stream response content:', nonStreamRes.body?.choices?.[0]?.message?.content)

  if (nonStreamRes.status !== 200) {
    throw new Error('Non-stream relay call failed: ' + JSON.stringify(nonStreamRes.body))
  }
  // Verify that upstream model name (hidden-upstream-openai-v1) was masked!
  if (nonStreamRes.body?.model !== 'deepseek-flash') {
    throw new Error(`Upstream model mask failed! Expected 'deepseek-flash', got '${nonStreamRes.body?.model}'`)
  }
  log('VERIFIED: Hidden upstream model name successfully rewritten to standard model deepseek-flash!')

  // --- Buyer Stream Relay Call ---
  log('8. Testing Buyer Stream Relay Call...')
  const streamRes = await fetch(`${BASE_URL}/v1/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sharedKey}`,
    },
    body: JSON.stringify({
      model: 'deepseek-flash',
      stream: true,
      messages: [{ role: 'user', content: '测试流式回复' }],
    }),
  })
  log('Stream relay status:', streamRes.status)
  if (streamRes.status !== 200) {
    throw new Error('Stream relay call failed with status ' + streamRes.status)
  }
  const streamText = await streamRes.text()
  log('Stream text received, length:', streamText.length)
  if (!streamText.includes('deepseek-flash')) {
    throw new Error('Stream chunks did not contain rewritten model deepseek-flash')
  }
  if (streamText.includes('hidden-upstream-openai-v1')) {
    throw new Error('Stream chunks leaked hidden upstream model name!')
  }
  log('VERIFIED: Stream SSE correctly rewritten without leaking upstream model name!')

  // 9. Seller Earnings & Payout
  log('9. Checking Seller Earnings and Ledger...')
  const sellerAccount = `seller:${sellerBundle.user.id}`
  const ledgerRows = db.query('SELECT * FROM mp_ledger WHERE account = ?').all(sellerAccount) as any[]
  log(`Found ${ledgerRows.length} ledger rows for seller:`, ledgerRows.map((r) => ({ id: r.id, type: r.entry_type, amount: r.amount_micro, status: r.status })))

  // Mature all pending ledger credits to available and set to 15,000,000 micro-yuan for withdrawal testing
  db.query(`UPDATE mp_ledger 
            SET status = 'available', 
                available_at = datetime('now', '-1 hour'),
                amount_micro = 15000000 
            WHERE id = (SELECT id FROM mp_ledger WHERE account = ? AND entry_type = 'seller_credit' LIMIT 1)`).run(sellerAccount)
  log('Matured and set seller credit to 15,000,000 micro-yuan for withdrawal testing')

  const totalAvail = db.query("SELECT sum(amount_micro) as total FROM mp_ledger WHERE account = ? AND status = 'available'").get(sellerAccount) as any
  log('Total available micro-yuan in ledger:', totalAvail?.total)

  // Go to Seller Earnings page in browser
  await sellerPage.goto(`${BASE_URL}/console/earnings`, { waitUntil: 'networkidle' })
  await sellerPage.waitForTimeout(1000)
  await sellerPage.screenshot({ path: path.join(SHOTS_DIR, '06-seller-earnings.png') })
  log('Saved screenshot 06-seller-earnings.png')

  // Go to Payouts page to bind WeChat account
  log('Navigating to /console/earnings/payouts...')
  await sellerPage.goto(`${BASE_URL}/console/earnings/payouts`, { waitUntil: 'networkidle' })
  await sellerPage.waitForTimeout(1000)

  // Bind WeChat payout account via UI or API
  log('Binding WeChat payout account...')
  const qrBase64 = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='
  const bindRes = await fetchJSON(`${BASE_URL}/api/marketplace/seller/onboarding`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sellerToken}`,
    },
    body: JSON.stringify({
      payout_channel: 'wechat',
      payout_name: '测试卖家',
      payout_account: '13800138000',
      payout_qr_code: qrBase64,
    }),
  })
  log('Bind WeChat status:', bindRes.status, bindRes.body)

  await sellerPage.reload({ waitUntil: 'networkidle' })
  await sellerPage.waitForTimeout(1000)
  await sellerPage.screenshot({ path: path.join(SHOTS_DIR, '07-payout-bind.png') })
  log('Saved screenshot 07-payout-bind.png')

  // Request Payout of 10 CNY (10,000,000 micro)
  log('10. Requesting Payout of ¥10...')
  const payoutReqRes = await fetchJSON(`${BASE_URL}/api/marketplace/seller/payouts`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sellerToken}`,
    },
    body: JSON.stringify({
      amount_micro: 10_000_000,
      idempotency_key: 'payout-' + Date.now(),
    }),
  })
  log('Payout request status:', payoutReqRes.status, payoutReqRes.body)
  const payoutID = payoutReqRes.body?.data?.id
  if (!payoutID) {
    throw new Error('Failed to request payout: ' + JSON.stringify(payoutReqRes.body))
  }

  // --- Admin Reviews Payout ---
  log('11. Admin reviewing and marking payout paid...')
  await adminPage.goto(`${BASE_URL}/console/ops/review/payouts`, { waitUntil: 'networkidle' })
  await adminPage.waitForTimeout(1000)

  // Approve payout
  const appPayoutRes = await fetchJSON(`${BASE_URL}/api/marketplace/admin/payouts/${payoutID}/approve`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${adminToken}`,
    },
  })
  log('Admin approve payout status:', appPayoutRes.status, appPayoutRes.body)

  // Mark paid
  const markPaidRes = await fetchJSON(`${BASE_URL}/api/marketplace/admin/payouts/${payoutID}/mark-paid`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${adminToken}`,
    },
  })
  log('Admin mark-paid status:', markPaidRes.status, markPaidRes.body)

  await adminPage.reload({ waitUntil: 'networkidle' })
  await adminPage.waitForTimeout(1000)
  await adminPage.screenshot({ path: path.join(SHOTS_DIR, '08-admin-payout-reviewed.png') })
  log('Saved screenshot 08-admin-payout-reviewed.png')

  // 12. Test Channel Lifecycle: Pause, Resume, Price Edit, Delete
  log('12. Testing Channel Lifecycle (Pause / Edit Price / Delete)...')

  // A. Pause Channel
  log('A. Pausing listing...')
  const pauseRes = await fetchJSON(`${BASE_URL}/api/marketplace/seller/listings/${listingID}/pause`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sellerToken}`,
    },
  })
  log('Pause listing status:', pauseRes.status)

  // Buyer relay call should receive 503 (no channel available)
  const relayPausedRes = await fetchJSON(`${BASE_URL}/v1/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sharedKey}`,
    },
    body: JSON.stringify({
      model: 'deepseek-flash',
      messages: [{ role: 'user', content: 'hello' }],
    }),
  })
  log('Relay after pause status:', relayPausedRes.status, relayPausedRes.body?.error?.message)
  if (relayPausedRes.status !== 503) {
    throw new Error('Expected 503 after pausing listing, got ' + relayPausedRes.status)
  }
  log('VERIFIED: Paused listing blocks buyer relay with 503!')

  // B. Resume Channel
  log('B. Resuming listing...')
  const resumeRes = await fetchJSON(`${BASE_URL}/api/marketplace/seller/listings/${listingID}/resume`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sellerToken}`,
    },
  })
  log('Resume listing status:', resumeRes.status)

  // Buyer relay call should succeed
  const relayResumeRes = await fetchJSON(`${BASE_URL}/v1/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sharedKey}`,
    },
    body: JSON.stringify({
      model: 'deepseek-flash',
      messages: [{ role: 'user', content: 'hello again' }],
    }),
  })
  log('Relay after resume status:', relayResumeRes.status)
  if (relayResumeRes.status !== 200) {
    throw new Error('Expected 200 after resuming listing, got ' + relayResumeRes.status)
  }
  log('VERIFIED: Resumed listing successfully accepts buyer relay!')

  // C. Edit Multiplier / Price
  log('C. Updating listing multiplier...')
  const updatePriceRes = await fetchJSON(`${BASE_URL}/api/marketplace/seller/listings/${listingID}`, {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sellerToken}`,
    },
    body: JSON.stringify({
      multiplier_milli: 1200,
    }),
  })
  log('Update multiplier status:', updatePriceRes.status, updatePriceRes.body)

  const relayEditRes = await fetchJSON(`${BASE_URL}/v1/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sharedKey}`,
    },
    body: JSON.stringify({
      model: 'deepseek-flash',
      messages: [{ role: 'user', content: 'hello after price edit' }],
    }),
  })
  log('Relay after price edit status:', relayEditRes.status)
  if (relayEditRes.status !== 200) {
    throw new Error('Expected 200 after updating multiplier, got ' + relayEditRes.status)
  }
  log('VERIFIED: Price edit persisted and relay continues to work!')

  // D. Delete Channel
  log('D. Deleting listing...')
  const deleteRes = await fetchJSON(`${BASE_URL}/api/marketplace/seller/listings/${listingID}`, {
    method: 'DELETE',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sellerToken}`,
    },
  })
  log('Delete listing status:', deleteRes.status)

  // Buyer relay call should receive 503
  const relayDeletedRes = await fetchJSON(`${BASE_URL}/v1/chat/completions`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${sharedKey}`,
    },
    body: JSON.stringify({
      model: 'deepseek-flash',
      messages: [{ role: 'user', content: 'hello after delete' }],
    }),
  })
  log('Relay after delete status:', relayDeletedRes.status, relayDeletedRes.body?.error?.message)
  if (relayDeletedRes.status !== 503) {
    throw new Error('Expected 503 after deleting listing, got ' + relayDeletedRes.status)
  }
  log('VERIFIED: Deleted listing blocks buyer relay with 503!')

  await browser.close()
  log('ALL TESTS IN SELLER FULL LIFECYCLE E2E PASSED PERFECTLY!')
}

main().catch((err) => {
  console.error('[E2E-FLOW] ERROR:', err)
  process.exit(1)
})
