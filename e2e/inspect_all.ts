import { chromium, type Page, type BrowserContext } from '../site/node_modules/@playwright/test';
import * as path from 'path';
import * as fs from 'fs';

const BASE_URL = 'http://127.0.0.1:3103';
const SHOTS_DIR = path.resolve(__dirname, 'shots');

if (!fs.existsSync(SHOTS_DIR)) {
  fs.mkdirSync(SHOTS_DIR, { recursive: true });
}

interface PageIssue {
  page: string;
  type: 'console_error' | 'page_error' | 'http_error';
  message: string;
  details?: string;
}

const issues: PageIssue[] = [];

function attachErrorListeners(page: Page, pageName: string) {
  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      const text = msg.text();
      // Ignore favicon or non-critical resource 404s if any
      issues.push({
        page: pageName,
        type: 'console_error',
        message: text,
      });
      console.log(`[CONSOLE ERROR] [${pageName}] ${text}`);
    }
  });

  page.on('pageerror', (err) => {
    issues.push({
      page: pageName,
      type: 'page_error',
      message: err.message,
      details: err.stack,
    });
    console.log(`[PAGE ERROR] [${pageName}] ${err.message}`);
  });

  page.on('response', (res) => {
    const status = res.status();
    const url = res.url();
    // Flag unexpected 4xx and 5xx errors from backend APIs
    if (status >= 400 && url.includes('/api/') && !url.includes('/api/user/login') && !url.includes('/api/setup')) {
      issues.push({
        page: pageName,
        type: 'http_error',
        message: `HTTP ${status} on ${url}`,
      });
      console.log(`[HTTP ERROR] [${pageName}] HTTP ${status} on ${url}`);
    }
  });
}

async function takeSnapshots(page: Page, urlPath: string, name: string) {
  // Desktop 1440x900
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${BASE_URL}${urlPath}`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(600);
  const desktopShot = path.join(SHOTS_DIR, `desktop_${name}.png`);
  await page.screenshot({ path: desktopShot, fullPage: true });

  // Mobile 390x844
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${BASE_URL}${urlPath}`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(600);
  const mobileShot = path.join(SHOTS_DIR, `mobile_${name}.png`);
  await page.screenshot({ path: mobileShot, fullPage: true });

  console.log(`[SHOT] Saved desktop and mobile screenshots for: ${name}`);
}

async function main() {
  console.log('--- STARTING PLAYWRIGHT E2E INSPECTION ---');
  const browser = await chromium.launch({ headless: true });

  try {
    // -------------------------------------------------------------
    // PHASE 1: PUBLIC PAGES (Desktop & Mobile)
    // -------------------------------------------------------------
    console.log('\n=== PHASE 1: PUBLIC PAGES ===');
    const publicPages = [
      { path: '/', name: 'home' },
      { path: '/plaza', name: 'plaza' },
      { path: '/models', name: 'models_market' },
      { path: '/models/deepseek-flash', name: 'model_detail' },
      { path: '/docs', name: 'docs' },
      { path: '/login', name: 'login' },
      { path: '/register', name: 'register' },
    ];

    const anonContext = await browser.newContext();
    const anonPage = await anonContext.newPage();
    attachErrorListeners(anonPage, 'public');

    for (const p of publicPages) {
      await takeSnapshots(anonPage, p.path, p.name);
    }
    await anonContext.close();

    // -------------------------------------------------------------
    // PHASE 2: ROOT LOGIN VIA UI
    // -------------------------------------------------------------
    console.log('\n=== PHASE 2: ROOT LOGIN VIA UI ===');
    const rootContext = await browser.newContext();
    const rootPage = await rootContext.newPage();
    attachErrorListeners(rootPage, 'root');

    await rootPage.setViewportSize({ width: 1440, height: 900 });
    await rootPage.goto(`${BASE_URL}/login`, { waitUntil: 'networkidle' });
    await rootPage.fill('input#login-identifier', 'root');
    await rootPage.fill('input#login-password', '123456');
    await rootPage.click('button[type="submit"]');

    await rootPage.waitForURL('**/console**', { timeout: 10000 });
    console.log('[LOGIN] Root logged in successfully, landed at:', rootPage.url());

    // -------------------------------------------------------------
    // PHASE 3: /ops-legacy NATIVE ADMIN
    // -------------------------------------------------------------
    console.log('\n=== PHASE 3: /ops-legacy NATIVE ADMIN ===');
    await takeSnapshots(rootPage, '/ops-legacy', 'ops_legacy');

    // -------------------------------------------------------------
    // PHASE 4: OPS PAGES INSPECTION
    // -------------------------------------------------------------
    console.log('\n=== PHASE 4: OPS PAGES INSPECTION ===');
    const opsPages = [
      { path: '/console', name: 'console_overview' },
      { path: '/console/keys', name: 'console_keys' },
      { path: '/console/records', name: 'console_records' },
      { path: '/console/wallet', name: 'console_wallet' },
      { path: '/console/listings', name: 'console_listings' },
      { path: '/console/earnings', name: 'console_earnings' },
      { path: '/console/account', name: 'console_account' },
      { path: '/console/ops/metrics', name: 'ops_metrics' },
      { path: '/console/ops/models', name: 'ops_models' },
      { path: '/console/ops/pricing', name: 'ops_pricing' },
      { path: '/console/ops/pricing/per-call', name: 'ops_pricing_percall' },
      { path: '/console/ops/review', name: 'ops_review' },
      { path: '/console/ops/review/payouts', name: 'ops_review_payouts' },
      { path: '/console/ops/risk', name: 'ops_risk' },
      { path: '/console/ops/tools', name: 'ops_tools' },
      { path: '/console/ops/tools/reversal', name: 'ops_tools_reversal' },
      { path: '/console/ops/trace', name: 'ops_trace' },
      { path: '/console/ops/trust-pool', name: 'ops_trustpool' },
    ];

    for (const p of opsPages) {
      await takeSnapshots(rootPage, p.path, p.name);
    }

    // -------------------------------------------------------------
    // PHASE 5: PLAZA TOGGLE EFFECT (Switch on/off deepseek-flash)
    // -------------------------------------------------------------
    console.log('\n=== PHASE 5: PLAZA TOGGLE EFFECT ===');
    await rootPage.setViewportSize({ width: 1440, height: 900 });
    await rootPage.goto(`${BASE_URL}/console/ops/models`, { waitUntil: 'networkidle' });
    await rootPage.waitForTimeout(500);

    // Find the row for deepseek-flash and find the plaza toggle button
    console.log('[PLAZA TOGGLE] Finding deepseek-flash row...');
    const flashRow = rootPage.locator('tr:has-text("deepseek-flash")').first();
    const plazaSwitch = flashRow.locator('button[aria-label*="广场"]');
    
    let currentLabel = await plazaSwitch.getAttribute('aria-label');
    console.log('[PLAZA TOGGLE] Current deepseek-flash switch aria-label:', currentLabel);

    if (currentLabel === '点击下架广场') {
      console.log('[PLAZA TOGGLE] Clicking switch to take down from plaza...');
      await plazaSwitch.click();
      await rootPage.waitForTimeout(1000);

      // Verify label changed
      const updatedLabel = await plazaSwitch.getAttribute('aria-label');
      console.log('[PLAZA TOGGLE] Updated switch aria-label on ops page:', updatedLabel);

      // Verify on /plaza
      await rootPage.goto(`${BASE_URL}/plaza`, { waitUntil: 'networkidle' });
      await rootPage.waitForTimeout(500);
      const existsOnPlaza = await rootPage.locator('text=deepseek-flash').isVisible().catch(() => false);
      console.log(`[PLAZA TOGGLE] After take down, deepseek-flash on /plaza: ${existsOnPlaza} (expected false)`);

      // Re-enable it
      await rootPage.goto(`${BASE_URL}/console/ops/models`, { waitUntil: 'networkidle' });
      await rootPage.waitForTimeout(500);
      const reFlashRow = rootPage.locator('tr:has-text("deepseek-flash")').first();
      const rePlazaSwitch = reFlashRow.locator('button[aria-label*="广场"]');
      await rePlazaSwitch.click();
      await rootPage.waitForTimeout(1000);

      // Verify on /plaza again
      await rootPage.goto(`${BASE_URL}/plaza`, { waitUntil: 'networkidle' });
      await rootPage.waitForTimeout(500);
      const reAppeared = await rootPage.locator('text=deepseek-flash').isVisible().catch(() => false);
      console.log(`[PLAZA TOGGLE] After re-enable, deepseek-flash on /plaza: ${reAppeared} (expected true)`);
    } else {
      console.log('[PLAZA TOGGLE] Model is currently not listed, clicking switch to put on plaza...');
      await plazaSwitch.click();
      await rootPage.waitForTimeout(1000);

      // Verify on /plaza
      await rootPage.goto(`${BASE_URL}/plaza`, { waitUntil: 'networkidle' });
      await rootPage.waitForTimeout(500);
      const appeared = await rootPage.locator('text=deepseek-flash').isVisible().catch(() => false);
      console.log(`[PLAZA TOGGLE] After putting on plaza, deepseek-flash on /plaza: ${appeared} (expected true)`);

      // Take down and re-put on plaza to test full cycle
      await rootPage.goto(`${BASE_URL}/console/ops/models`, { waitUntil: 'networkidle' });
      await rootPage.waitForTimeout(500);
      const row2 = rootPage.locator('tr:has-text("deepseek-flash")').first();
      const sw2 = row2.locator('button[aria-label*="广场"]');
      await sw2.click();
      await rootPage.waitForTimeout(1000);
      await rootPage.goto(`${BASE_URL}/plaza`, { waitUntil: 'networkidle' });
      const gone = await rootPage.locator('text=deepseek-flash').isVisible().catch(() => false);
      console.log(`[PLAZA TOGGLE] After take down, deepseek-flash on /plaza: ${gone} (expected false)`);

      // Put back on plaza
      await rootPage.goto(`${BASE_URL}/console/ops/models`, { waitUntil: 'networkidle' });
      await rootPage.waitForTimeout(500);
      const row3 = rootPage.locator('tr:has-text("deepseek-flash")').first();
      const sw3 = row3.locator('button[aria-label*="广场"]');
      await sw3.click();
      await rootPage.waitForTimeout(1000);
    }

    // -------------------------------------------------------------
    // PHASE 6: NON-ADMIN PERMISSION RESTRICTIONS
    // -------------------------------------------------------------
    console.log('\n=== PHASE 6: NON-ADMIN PERMISSION RESTRICTIONS ===');
    const buyerContext = await browser.newContext();
    const buyerPage = await buyerContext.newPage();
    attachErrorListeners(buyerPage, 'buyer');

    await buyerPage.setViewportSize({ width: 1440, height: 900 });
    await buyerPage.goto(`${BASE_URL}/login`, { waitUntil: 'networkidle' });
    await buyerPage.fill('input#login-identifier', 'buyer01');
    await buyerPage.fill('input#login-password', 'BuyerPassword123!');
    await buyerPage.click('button[type="submit"]');
    await buyerPage.waitForURL('**/console**', { timeout: 10000 });
    console.log('[BUYER] Buyer logged in successfully');

    // Check sidebar does NOT contain ops links
    const sidebarText = await buyerPage.locator('aside, [role="navigation"]').allInnerTexts();
    const joinedSidebar = sidebarText.join(' ');
    const hasOpsEntries = joinedSidebar.includes('运营看板') ||
                          joinedSidebar.includes('团体工具') ||
                          joinedSidebar.includes('广场渠道管理') ||
                          joinedSidebar.includes('广场模型上架') ||
                          joinedSidebar.includes('发布审核');
    console.log(`[BUYER PERMS] Buyer sidebar contains ops links: ${hasOpsEntries} (expected false)`);

    // Attempt direct navigation to /console/ops/*
    for (const target of ['/console/ops/models', '/console/ops/pricing', '/console/ops/metrics']) {
      await buyerPage.goto(`${BASE_URL}${target}`, { waitUntil: 'networkidle' });
      await buyerPage.waitForTimeout(500);
      const finalUrl = buyerPage.url();
      const redirected = !finalUrl.includes('/console/ops');
      console.log(`[BUYER PERMS] Access ${target} -> Landed at ${finalUrl} (Redirected away from ops: ${redirected})`);
    }

    await buyerContext.close();

    // -------------------------------------------------------------
    // PHASE 7: CREATE KEY DIALOG & CONFIG SNIPPETS
    // -------------------------------------------------------------
    console.log('\n=== PHASE 7: CREATE KEY DIALOG & CONFIG SNIPPETS ===');
    await rootPage.goto(`${BASE_URL}/console/keys`, { waitUntil: 'networkidle' });
    await rootPage.waitForTimeout(500);

    // Click "新建 Key" button
    console.log('[CREATE KEY] Opening CreateKeyDialog...');
    const newKeyBtn = rootPage.locator('button:has-text("新建 Key"), button:has-text("新建密钥")').first();
    await newKeyBtn.click();
    await rootPage.waitForTimeout(500);

    // Verify two types exist
    const standardCard = rootPage.locator('text=普通密钥').first();
    const sharedCard = rootPage.locator('text=共享密钥').first();
    console.log('[CREATE KEY] "普通密钥" visible:', await standardCard.isVisible());
    console.log('[CREATE KEY] "共享密钥" visible:', await sharedCard.isVisible());

    // 1. Create standard key
    await standardCard.click();
    await rootPage.fill('input[placeholder*="Key"]', 'E2E-Standard-Key');
    await rootPage.click('button:has-text("创建 Key"), button:has-text("立即创建")');
    await rootPage.waitForTimeout(1000);

    // Verify success modal
    const successTitle = await rootPage.locator('text=Key 已创建, text=密钥已创建').first().isVisible();
    console.log('[CREATE KEY] Standard key created, success modal visible:', successTitle);

    const keyTypeLabel = await rootPage.locator('text=普通密钥（模型广场）').first().isVisible();
    console.log('[CREATE KEY] Standard key type label in modal visible:', keyTypeLabel);

    // Check config snippets: Claude Code, Cursor, Cherry Studio
    const claudeSnippet = rootPage.locator('text=ANTHROPIC_BASE_URL').first();
    const cursorSnippet = rootPage.locator('pre:has-text("Base URL:")').first();
    const cherrySnippet = rootPage.locator('pre:has-text("API 地址:")').first();
    console.log('[CREATE KEY] Claude Code snippet visible:', await claudeSnippet.isVisible());
    console.log('[CREATE KEY] Cursor snippet visible:', await cursorSnippet.isVisible());
    console.log('[CREATE KEY] Cherry Studio snippet visible:', await cherrySnippet.isVisible());

    // Verify config snippet content correctness
    const claudeText = await claudeSnippet.innerText().catch(() => '');
    const cursorText = await cursorSnippet.innerText().catch(() => '');
    const cherryText = await cherrySnippet.innerText().catch(() => '');
    console.log('[CREATE KEY] Claude Code config preview:\n', claudeText);
    console.log('[CREATE KEY] Cursor config preview:\n', cursorText);
    console.log('[CREATE KEY] Cherry Studio config preview:\n', cherryText);

    // Test copy snippet button
    const copyConfigBtn = rootPage.locator('button:has-text("复制配置")').first();
    await copyConfigBtn.click();
    await rootPage.waitForTimeout(500);
    const copiedFeedback = await rootPage.locator('text=已复制').first().isVisible().catch(() => false);
    console.log('[CREATE KEY] Copy config button feedback:', copiedFeedback);

    // Close dialog
    await rootPage.click('button:has-text("完成")');
    await rootPage.waitForTimeout(500);

    // 2. Create shared key
    await newKeyBtn.click();
    await rootPage.waitForTimeout(500);
    await rootPage.locator('text=共享密钥').first().click();
    await rootPage.fill('input[placeholder*="Key"]', 'E2E-Shared-Key');
    await rootPage.click('button:has-text("创建 Key"), button:has-text("立即创建")');
    await rootPage.waitForTimeout(1000);

    const sharedKeyTypeLabel = await rootPage.locator('text=共享密钥（模型市场）').first().isVisible();
    console.log('[CREATE KEY] Shared key type label in modal visible:', sharedKeyTypeLabel);
    await rootPage.click('button:has-text("完成")');

    await rootContext.close();
    console.log('\n--- PLAYWRIGHT INSPECTION COMPLETE ---');
  } finally {
    await browser.close();
  }

  // Print summary of detected issues
  console.log('\n=============================================');
  console.log(`TOTAL ISSUES DETECTED: ${issues.length}`);
  console.log('=============================================');
  for (const iss of issues) {
    console.log(`[${iss.type.toUpperCase()}] [${iss.page}] ${iss.message}`);
    if (iss.details) {
      console.log(`  Details: ${iss.details}`);
    }
  }
}

main().catch((err) => {
  console.error('Fatal error running inspection:', err);
  process.exit(1);
});
