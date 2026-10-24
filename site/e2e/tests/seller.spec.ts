import { expect, test } from '@playwright/test'

import {
  LOCAL,
  PASSWORD,
  SELLER_ALIPAY,
  SELLER_ID_CARD,
  SELLER_REAL_NAME,
  authenticatePage,
  createListing,
  login,
  uniqueName,
} from '../support/api'
import { creditSeller } from '../support/db'

/**
 * FRONTEND-REBUILD §8 路径 B：
 * 开通分享者 → 挂单 → 改倍率 → 提现申请
 *
 * 开通、改倍率和提现都通过 UI 完成。挂单创建调用与前端「提交挂单」相同的
 * API：环境里没有可用的上游 Key，向导的 probe 步骤必然失败，无法进入可提交
 * 状态；创建出来的挂单随后出现在「我的挂单」里，后续操作全部走 UI。
 */
test.describe('关键路径 B：开通分享者 → 挂单 → 改倍率 → 提现申请', () => {
  test.skip(
    !LOCAL,
    '需要本地 SQLite 夹具（提现余额种子、避开真实上游 Key）；外部实例请按报告中的 llmjar 手工清单验证',
  )

  test('完整分享者路径', async ({ page }) => {
    test.setTimeout(240_000)

    const sellerName = uniqueName('e2e-sel')

    // --- 注册（UI） ---
    await page.goto('/register')
    await page.getByLabel('用户名').fill(sellerName)
    await page.getByLabel('邮箱').fill(`${sellerName}@example.com`)
    await page.getByLabel('密码', { exact: true }).fill(PASSWORD)
    await page.getByLabel('确认密码').fill(PASSWORD)
    await page.getByRole('button', { name: '创建账号' }).click()
    await page.waitForURL(/\/login/)
    await expect(page.getByRole('status')).toContainText('账号已创建')

    await authenticatePage(page, sellerName)

    // --- 开通分享者（三步） ---
    await page.goto('/seller')
    await page.waitForURL(/\/seller\/onboarding/)

    await page.getByLabel('我已阅读并同意以上条款').check()
    await page.getByRole('button', { name: '签署协议' }).click()
    await expect(page.getByRole('heading', { name: '实名认证' })).toBeVisible()

    await page.getByLabel('真实姓名').fill(SELLER_REAL_NAME)
    await page.getByLabel('身份证号（脱敏）').fill(SELLER_ID_CARD)
    await page.getByRole('button', { name: '提交实名信息' }).click()
    await expect(page.getByRole('heading', { name: '绑定支付宝' })).toBeVisible()

    await page.getByLabel('支付宝账号').fill(SELLER_ALIPAY)
    await page.getByRole('button', { name: '绑定并完成开通' }).click()
    await expect(page.getByText('分享者身份已开通')).toBeVisible()

    // --- 挂单（与向导提交的 payload 相同） ---
    const token = await login(sellerName)
    const listingId = await createListing(
      token,
      `sk-e2e-${Date.now()}-listing`,
      'deepseek-flash',
      900,
    )
    expect(listingId).toBeGreaterThan(0)

    await page.goto('/seller/listings')
    await expect(page.getByText('deepseek-flash').first()).toBeVisible()

    // --- 改倍率（UI） ---
    await page.getByRole('button', { name: '改倍率' }).first().click()
    const multiplierDialog = page.getByRole('dialog')
    await expect(multiplierDialog.getByText('修改倍率')).toBeVisible()
    await multiplierDialog
      .locator('input[aria-label="倍率（0.001x – 1.000x）"]')
      .fill('0.950')
    await multiplierDialog.getByRole('button', { name: '保存倍率' }).click()
    await expect(page.getByText('倍率已更新')).toBeVisible()
    await expect(page.getByText('0.95x').first()).toBeVisible()

    // --- 提现申请（UI；CI 没有买家成交，种子一笔 T+7 已结算收入） ---
    creditSeller(sellerName, 60_000_000)
    await page.goto('/seller/payouts')
    const requestButton = page.getByRole('button', { name: '申请提现' })
    await expect(requestButton).toBeEnabled()
    await requestButton.click()

    const payoutDialog = page.getByRole('dialog')
    await expect(payoutDialog.getByText('申请提现')).toBeVisible()
    await expect(payoutDialog.getByLabel('提现金额（元）')).toHaveValue('60')
    await payoutDialog.getByRole('button', { name: '提交申请' }).click()

    await expect(page.getByText('提现申请已提交，等待审批。')).toBeVisible()
    await expect(page.getByText('待审批').first()).toBeVisible()
    await expect(page.getByText('共 1 条', { exact: true })).toBeVisible()
  })
})
