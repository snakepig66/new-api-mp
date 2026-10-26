import { chromium } from 'playwright'

const base = 'https://llmjar.com'
const browser = await chromium.launch()
const context = await browser.newContext({ viewport: { width: 390, height: 900 } })
const page = await context.newPage()
const out = { base }

await page.goto(`${base}/share`, { waitUntil: 'domcontentloaded' })
out.shareHeading = await page.getByRole('heading', { name: '分享额度' }).count()
out.nav = await page.locator('header nav a').allTextContents()
out.oldSellerNav = (await page.getByText('成为分享者', { exact: true }).count()) > 0
out.overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)

for (const from of ['/seller', '/seller/records', '/admin/prices', '/admin/channel-models', '/ops-legacy']) {
  await page.goto(`${base}${from}`, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(900)
  const url = new URL(page.url())
  out[from] = { path: url.pathname, redirect: url.searchParams.get('redirect') }
}

console.log(JSON.stringify(out, null, 2))
await browser.close()
