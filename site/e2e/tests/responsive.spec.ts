import { expect, test, type Page } from '@playwright/test'

import {
  LOCAL,
  authenticatePage,
  createListing,
  ensureAccount,
  login,
  onboardSeller,
  uniqueName,
} from '../support/api'
import { forceListingLive } from '../support/db'

/**
 * FRONTEND-REBUILD §8: no horizontal overflow at 1440px and 390px.
 *
 * The document-level check catches page overflow. Tables are expected to
 * scroll inside their own overflow-x-auto container, which does not grow the
 * document. Dialog width is checked separately against calc(100vw - 2rem).
 */
async function expectNoHorizontalOverflow(page: Page, route: string) {
  const measurement = await page.evaluate(() => ({
    doc: document.documentElement.scrollWidth,
    body: document.body.scrollWidth,
    viewport: window.innerWidth,
  }))
  expect(
    measurement.doc,
    `${route} overflows horizontally: document=${measurement.doc} viewport=${measurement.viewport}`,
  ).toBeLessThanOrEqual(measurement.viewport + 1)
  expect(
    measurement.body,
    `${route} body overflows horizontally: body=${measurement.body} viewport=${measurement.viewport}`,
  ).toBeLessThanOrEqual(measurement.viewport + 1)
}

async function visitAndCheck(page: Page, route: string, width: number) {
  await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
  await page.goto(route)
  await page.waitForLoadState('domcontentloaded')
  // Some layouts render a visually hidden heading first (e.g. the auth brand
  // panel); always assert on a heading that is actually visible.
  await expect(page.locator('main :is(h1, h2):visible').first()).toBeVisible({ timeout: 30_000 })
  await page.waitForTimeout(500)
  await expectNoHorizontalOverflow(page, `${route} @${width}`)
}

const PUBLIC_ROUTES = ['/', '/models', '/docs', '/login', '/register']
const AUTH_ROUTES = [
  '/console',
  '/console/keys',
  '/console/records',
  '/console/wallet',
  '/seller',
  '/seller/onboarding',
  '/seller/listings',
  '/seller/listings/new',
  '/seller/records',
  '/seller/payouts',
  '/admin/prices',
  '/admin/per-call-prices',
  '/admin/holidays',
  '/admin/listings',
  '/admin/payouts',
  '/admin/tools',
]

test.describe('响应式：公开区', () => {
  for (const width of [1440, 390]) {
    test(`公开路由在 ${width}px 下不横向溢出`, async ({ page }) => {
      for (const route of PUBLIC_ROUTES) {
        await visitAndCheck(page, route, width)
      }
    })
  }

  for (const width of [1440, 390]) {
    test(`${width}px 下模型详情不横向溢出`, async ({ page }) => {
      test.skip(!LOCAL, '外部实例的行情由部署环境决定，模型详情由手工清单覆盖')
      const sellerName = uniqueName('e2e-r')
      const token = await ensureAccount(sellerName)
      await onboardSeller(token)
      const listingId = await createListing(token, `sk-e2e-${Date.now()}-responsive`)
      await forceListingLive(listingId)

      await visitAndCheck(page, '/models/deepseek-flash', width)
    })
  }
})

test.describe('响应式：控制台与运营后台', () => {
  test.skip(
    !LOCAL,
    '需要本地管理员账号与本地数据夹具；外部实例请按报告中的 llmjar 手工清单验证',
  )

  test.beforeAll(async () => {
    // start-backend created this root account from E2E_PASS.
    const adminToken = await login('admin')
    await onboardSeller(adminToken)
    const listingId = await createListing(
      adminToken,
      `sk-e2e-${Date.now()}-responsive-auth`,
      'deepseek-flash',
    )
    await forceListingLive(listingId)
  })

  for (const width of [1440, 390]) {
    test(`登录后的控制台路由在 ${width}px 下不横向溢出`, async ({ page }) => {
      await authenticatePage(page, 'admin')
      for (const route of AUTH_ROUTES) {
        await visitAndCheck(page, route, width)
      }
    })

    test(`弹窗在 ${width}px 下不超出视口`, async ({ page }) => {
      await authenticatePage(page, 'admin')
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })

      await page.goto('/console/keys')
      await page.getByRole('button', { name: '新建 Key' }).first().click()
      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()
      await page.waitForTimeout(300) // let the 160ms opening animation settle
      const box = await dialog.boundingBox()
      expect(box).not.toBeNull()
      if (box) {
        expect(box.width).toBeLessThanOrEqual(width - 32 + 1)
        expect(box.x).toBeGreaterThanOrEqual(0)
        expect(box.x + box.width).toBeLessThanOrEqual(width + 1)
      }
      await page.keyboard.press('Escape')
      await expect(dialog).toBeHidden()

      await page.goto('/admin/prices')
      await expect(page.getByRole('heading', { name: '价目表' })).toBeVisible()
      await page.getByRole('button', { name: '新增版本' }).click()
      const priceDialog = page.getByRole('dialog')
      await expect(priceDialog).toBeVisible()
      await page.waitForTimeout(300)
      const priceBox = await priceDialog.boundingBox()
      expect(priceBox).not.toBeNull()
      if (priceBox) {
        expect(priceBox.width).toBeLessThanOrEqual(width - 32 + 1)
        expect(priceBox.x).toBeGreaterThanOrEqual(0)
        expect(priceBox.x + priceBox.width).toBeLessThanOrEqual(width + 1)
      }
    })
  }
})
