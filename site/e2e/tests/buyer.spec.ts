import { expect, test } from '@playwright/test'

import {
  BACKEND,
  LOCAL,
  PASSWORD,
  authenticatePage,
  createListing,
  ensureAccount,
  fullTokenKey,
  listTokens,
  login,
  onboardSeller,
  uniqueName,
} from '../support/api'
import { creditBuyer, forceListingLive } from '../support/db'

/**
 * FRONTEND-REBUILD §8 路径 A：
 * 注册 → 建 Key → 加模型 → 调用 → 看记录
 *
 * The local harness supplies the market inventory (no valid vendor key exists
 * in CI), so the call reaches the marketplace relay and fails at the vendor.
 * The marketplace still writes a usage record; that is what the record step
 * asserts, together with the full UI flow around it.
 */
test.describe('关键路径 A：注册 → 建 Key → 加模型 → 调用 → 看记录', () => {
  test.skip(
    !LOCAL,
    '需要本地 SQLite 夹具（强制上架、买家额度）；外部实例请按报告中的 llmjar 手工清单验证',
  )

  test('完整买家路径', async ({ page }) => {
    test.setTimeout(240_000)

    const sellerName = uniqueName('e2e-s')
    const sellerToken = await ensureAccount(sellerName)
    await onboardSeller(sellerToken)
    const listingId = await createListing(
      sellerToken,
      `sk-e2e-${Date.now()}-supply`,
      ['deepseek-v4-pro', 'deepseek-flash'],
    )
    await forceListingLive(listingId)

    const buyerName = uniqueName('e2e-b')

    // --- 注册（UI） ---
    await page.goto('/register')
    await page.getByLabel('用户名').fill(buyerName)
    await page.getByLabel('邮箱').fill(`${buyerName}@example.com`)
    await page.getByLabel('密码', { exact: true }).fill(PASSWORD)
    await page.getByLabel('确认密码').fill(PASSWORD)
    await page.getByRole('button', { name: '创建账号' }).click()
    await page.waitForURL(/\/login/)
    await expect(page.getByRole('status')).toContainText('账号已创建')

    // --- 登录态（API token 注入，避免触发后端登录限流） ---
    await authenticatePage(page, buyerName)
    await page.goto('/console')
    await expect(page.getByRole('heading', { name: '控制台' })).toBeVisible()

    // --- 建 Key ---
    await page.goto('/console/keys')
    await page.getByRole('button', { name: '新建 Key' }).first().click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('新建 API Key')).toBeVisible()
    await dialog.getByLabel('Key 名称').fill('E2E 生产 Key')
    await dialog.getByRole('button', { name: '下一步' }).click()

    const modelCheckbox = dialog
      .locator('label')
      .filter({ hasText: 'deepseek-v4-pro' })
      .locator('input[type="checkbox"]')
    await expect(modelCheckbox).toBeVisible()
    await modelCheckbox.check()
    await dialog.getByRole('button', { name: '下一步' }).click()
    await dialog.getByRole('button', { name: '下一步' }).click()
    await expect(dialog.getByLabel('花费上限')).toBeVisible()
    await dialog.getByRole('button', { name: '创建 Key' }).click()
    await expect(page.getByText('Key 已创建')).toBeVisible()
    await expect(page.getByText('E2E 生产 Key').first()).toBeVisible()

    // --- 加模型（模型详情页） ---
    await page.goto('/models')
    await page.getByRole('link', { name: 'deepseek-flash', exact: true }).click()
    await page.waitForURL(/\/models\/deepseek-flash/)
    await page.getByRole('button', { name: '加到我的 Key' }).click()
    await expect(page.getByText(/已加入 Key/)).toBeVisible()

    // --- 调用（API，与文档中的 curl 等价） ---
    creditBuyer(buyerName)
    const buyerToken = await login(buyerName)
    const tokens = await listTokens(buyerToken)
    expect(tokens.length).toBeGreaterThan(0)
    const key = await fullTokenKey(buyerToken, tokens[0].id)

    const response = await fetch(`${BACKEND}/v1/chat/completions`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({
        model: 'deepseek-flash',
        messages: [{ role: 'user', content: '你好' }],
        max_tokens: 4,
      }),
    })
    // No reachable upstream in CI: the relay fails closed. What matters is
    // that the marketplace accepted the request and recorded it.
    expect(response.ok).toBe(false)

    // --- 看记录 ---
    await page.goto('/console/records')
    await expect(page.getByText('deepseek-flash').first()).toBeVisible()
    await expect(page.getByText('失败').first()).toBeVisible()
    await page.getByRole('row', { name: /请求详情/ }).first().click()
    const detail = page.getByRole('dialog')
    await expect(detail.getByText('请求详情')).toBeVisible()
    await expect(detail.getByText('deepseek-flash')).toBeVisible()
  })
})
