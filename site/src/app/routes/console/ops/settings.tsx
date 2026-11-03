/**
 * 运营后台 · 系统设置。
 *
 * 站点原生设置页，替代 new-api 的系统设置面板。读写 /api/option/（RootAuth）：
 * GET 已过滤敏感项，PUT 逐项保存，所以敏感值（SMTPToken、TurnstileSecretKey、
 * EpayKey）只写不回显。每组独立保存，只提交改动过的 key。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import { PageHeader, Switch, useToast } from '@/ui'

import {
  ErrorBlock,
  Field,
  OpsTabNav,
  PageSkeleton,
  RootOnly,
  Section,
  TextArea,
  TextInput,
  type OpsTabLink,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import {
  adminOptionsQuery,
  requestPolicyQuery,
  siteStatusQuery,
  updateOptions,
  updateRequestPolicy,
  type OptionMap,
} from '@/features/admin/settings-api'

export const Route = createFileRoute('/console/ops/settings')({
  component: SettingsPage,
})

const tabs: readonly OpsTabLink[] = [{ to: '/console/ops/settings', label: '系统设置' }]

type Scope = 'site' | 'auth' | 'smtp' | 'payment' | 'newUser'

const SECRET_FIELDS: Record<Scope, string[]> = {
  site: [],
  auth: ['TurnstileSecretKey'],
  smtp: ['SMTPToken'],
  payment: ['EpayKey'],
  newUser: [],
}

const SECRET_LABELS: Record<string, string> = {
  TurnstileSecretKey: 'Turnstile Secret Key',
  SMTPToken: 'SMTP 密码 / 授权码',
  EpayKey: '易支付商户密钥',
}

function text(map: OptionMap, key: string, fallback = ''): string {
  return map[key] ?? fallback
}

function truthy(map: OptionMap, key: string): boolean {
  return text(map, key) === 'true'
}

/** 额度按「1 元 = quotaPerUnit 额度」换算成元，供显示与输入使用。 */
function quotaToYuan(raw: string, quotaPerUnit: number): string {
  const value = Number(raw)
  if (!raw.trim() || !Number.isFinite(value)) return ''
  const yuan = value / quotaPerUnit
  return Number.isInteger(yuan) ? String(yuan) : yuan.toFixed(2)
}

function yuanToQuota(raw: string, quotaPerUnit: number): string | null {
  if (!raw.trim()) return '0'
  const value = Number(raw)
  if (!Number.isFinite(value) || value < 0) return null
  return String(Math.round(value * quotaPerUnit))
}

interface Initial {
  site: OptionMap
  auth: OptionMap
  smtp: OptionMap
  payment: OptionMap
  newUser: OptionMap
}

function initialFrom(options: OptionMap, quotaPerUnit: number): Initial {
  return {
    site: {
      ServerAddress: text(options, 'ServerAddress'),
      SystemName: text(options, 'SystemName'),
      Notice: text(options, 'Notice'),
      Footer: text(options, 'Footer'),
    },
    auth: {
      RegisterEnabled: String(truthy(options, 'RegisterEnabled')),
      PasswordRegisterEnabled: String(truthy(options, 'PasswordRegisterEnabled')),
      EmailVerificationEnabled: String(truthy(options, 'EmailVerificationEnabled')),
      EmailDomainRestrictionEnabled: String(truthy(options, 'EmailDomainRestrictionEnabled')),
      EmailDomainWhitelist: text(options, 'EmailDomainWhitelist'),
      TurnstileCheckEnabled: String(truthy(options, 'TurnstileCheckEnabled')),
      TurnstileSiteKey: text(options, 'TurnstileSiteKey'),
      TurnstileSecretKey: '',
    },
    smtp: {
      SMTPServer: text(options, 'SMTPServer'),
      SMTPPort: text(options, 'SMTPPort', '587'),
      SMTPAccount: text(options, 'SMTPAccount'),
      SMTPFrom: text(options, 'SMTPFrom'),
      SMTPToken: '',
      SMTPSSLEnabled: String(truthy(options, 'SMTPSSLEnabled')),
    },
    payment: {
      PayAddress: text(options, 'PayAddress'),
      EpayId: text(options, 'EpayId'),
      EpayKey: '',
      Price: text(options, 'Price'),
      MinTopUp: text(options, 'MinTopUp'),
      TopupGroupRatio: text(options, 'TopupGroupRatio'),
      CustomCallbackAddress: text(options, 'CustomCallbackAddress'),
    },
    newUser: {
      QuotaForNewUser: quotaToYuan(text(options, 'QuotaForNewUser'), quotaPerUnit),
      QuotaForInviter: quotaToYuan(text(options, 'QuotaForInviter'), quotaPerUnit),
      QuotaForInvitee: quotaToYuan(text(options, 'QuotaForInvitee'), quotaPerUnit),
    },
  }
}

function changedEntries(
  initial: OptionMap,
  current: OptionMap,
  secretFields: string[],
): [string, string][] {
  const entries: [string, string][] = []
  for (const [key, value] of Object.entries(current)) {
    if (secretFields.includes(key)) {
      if (value !== '') entries.push([key, value])
      continue
    }
    if (value !== (initial[key] ?? '')) entries.push([key, value])
  }
  return entries
}

function SettingsPage() {
  return (
    <RootOnly>
      <SettingsContent />
    </RootOnly>
  )
}

function SettingsContent() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const options = useQuery(adminOptionsQuery())
  const status = useQuery(siteStatusQuery())
  const policy = useQuery(requestPolicyQuery())
  const quotaPerUnit = status.data ?? 500_000

  const [initial, setInitial] = useState<Initial | null>(null)
  const [forms, setForms] = useState<Initial | null>(null)
  const retryInitial = policy.data?.RetryTimes ?? ''
  const [retryDraft, setRetryDraft] = useState<string | null>(null)
  const retryTimes = retryDraft ?? retryInitial

  useEffect(() => {
    if (!options.data || initial) return
    const next = initialFrom(options.data, quotaPerUnit)
    setInitial(next)
    setForms(next)
  }, [options.data, initial, quotaPerUnit])

  const save = useMutation({
    mutationFn: (entries: [string, string][]) => updateOptions(entries),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: adminOptionsQuery().queryKey })
      toast.show({ title: '设置已保存', tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function update(scope: Scope, key: string, value: string) {
    setForms((current) => (current ? { ...current, [scope]: { ...current[scope], [key]: value } } : current))
  }

  const saveRetry = useMutation({
    mutationFn: (value: string) => updateRequestPolicy({ RetryTimes: value }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: requestPolicyQuery().queryKey }),
        // 渠道页顶部的「最大重试」徽章读的是 /channel/ops，保存后一并刷新。
        queryClient.invalidateQueries({ queryKey: ['channel-ops'] }),
      ])
      setRetryDraft(null)
      toast.show({ title: '重试设置已保存', tone: 'success' })
    },
    onError: (error) => toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  function handleSaveRetry() {
    const raw = retryTimes.trim()
    if (!/^\d+$/.test(raw) || !Number.isSafeInteger(Number(raw))) {
      toast.show({ title: '最大重试次数必须是非负整数', tone: 'error' })
      return
    }
    if (raw === retryInitial) {
      toast.show({ title: '没有需要保存的修改' })
      return
    }
    saveRetry.mutate(raw)
  }

  async function handleSave(scope: Scope) {
    if (!initial || !forms) return
    const entries = changedEntries(initial[scope], forms[scope], SECRET_FIELDS[scope])
    if (entries.length === 0) {
      toast.show({ title: '没有需要保存的修改' })
      return
    }
    if (scope === 'newUser') {
      for (const key of ['QuotaForNewUser', 'QuotaForInviter', 'QuotaForInvitee']) {
        if (yuanToQuota(forms[scope][key], quotaPerUnit) === null) {
          toast.show({ title: '新用户额度必须是非负数字', tone: 'error' })
          return
        }
      }
      const normalized = entries.map(([key, value]) => [
        key,
        yuanToQuota(value, quotaPerUnit) ?? '0',
      ]) as [string, string][]
      await save.mutateAsync(normalized)
    } else {
      await save.mutateAsync(entries)
    }
    setInitial((current) => (current ? { ...current, [scope]: { ...forms[scope] } } : current))
    setForms((current) =>
      current
        ? { ...current, [scope]: { ...current[scope], ...Object.fromEntries(SECRET_FIELDS[scope].map((k) => [k, ''])) } }
        : current,
    )
  }

  function saving(scope: Scope): boolean {
    return save.isPending && save.variables !== undefined
      ? save.variables.some(([key]) => isScopeKey(scope, key))
      : false
  }

  if (options.isPending || status.isPending || policy.isPending) {
    return (
      <>
        <OpsTabNav tabs={tabs} ariaLabel="运营后台导航" />
        <PageHeader title="系统设置" lead="站点基础信息、注册登录、邮件与支付等运营配置。" />
        <div className="mt-6">
          <PageSkeleton />
        </div>
      </>
    )
  }

  if (options.isError || !forms || !initial) {
    return (
      <>
        <OpsTabNav tabs={tabs} ariaLabel="运营后台导航" />
        <PageHeader title="系统设置" lead="站点基础信息、注册登录、邮件与支付等运营配置。" />
        <div className="mt-6">
          <ErrorBlock
            error={options.error ?? new Error('加载失败')}
            onRetry={() => void options.refetch()}
          />
        </div>
      </>
    )
  }

  const site = forms.site
  const auth = forms.auth
  const smtp = forms.smtp
  const payment = forms.payment
  const newUser = forms.newUser

  return (
    <>
      <OpsTabNav tabs={tabs} ariaLabel="运营后台导航" />
      <PageHeader title="系统设置" lead="站点基础信息、注册登录、邮件与支付等运营配置。敏感值只写不回显。" />

      <div className="mt-6 space-y-6">
        <Section
          title="站点"
          hint="显示在控制台与分享页的基础信息"
          action={
            <button
              type="button"
              className="inline-flex h-9 items-center rounded-md bg-brand px-3.5 text-body font-medium text-white disabled:opacity-60"
              disabled={saving('site')}
              onClick={() => void handleSave('site')}
            >
              {saving('site') ? '保存中…' : '保存'}
            </button>
          }
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="站点地址" htmlFor="set-server-address" hint="例如 https://example.com，用于回调与分享链接">
              <TextInput
                id="set-server-address"
                value={site.ServerAddress}
                onChange={(e) => update('site', 'ServerAddress', e.target.value)}
              />
            </Field>
            <Field label="站点名称" htmlFor="set-system-name">
              <TextInput
                id="set-system-name"
                value={site.SystemName}
                onChange={(e) => update('site', 'SystemName', e.target.value)}
              />
            </Field>
            <Field label="公告" htmlFor="set-notice" className="sm:col-span-2">
              <TextArea
                id="set-notice"
                rows={3}
                value={site.Notice}
                onChange={(e) => update('site', 'Notice', e.target.value)}
              />
            </Field>
            <Field label="页脚" htmlFor="set-footer" className="sm:col-span-2" hint="支持 HTML">
              <TextArea
                id="set-footer"
                rows={2}
                value={site.Footer}
                onChange={(e) => update('site', 'Footer', e.target.value)}
              />
            </Field>
          </div>
        </Section>

        <Section
          title="注册登录"
          hint="注册开关、邮箱验证与人机验证"
          action={
            <button
              type="button"
              className="inline-flex h-9 items-center rounded-md bg-brand px-3.5 text-body font-medium text-white disabled:opacity-60"
              disabled={saving('auth')}
              onClick={() => void handleSave('auth')}
            >
              {saving('auth') ? '保存中…' : '保存'}
            </button>
          }
        >
          <div className="space-y-4">
            <ToggleRow
              label="开放注册"
              checked={auth.RegisterEnabled === 'true'}
              onChange={(v) => update('auth', 'RegisterEnabled', String(v))}
            />
            <ToggleRow
              label="允许密码注册"
              checked={auth.PasswordRegisterEnabled === 'true'}
              onChange={(v) => update('auth', 'PasswordRegisterEnabled', String(v))}
            />
            <ToggleRow
              label="注册需邮箱验证"
              checked={auth.EmailVerificationEnabled === 'true'}
              onChange={(v) => update('auth', 'EmailVerificationEnabled', String(v))}
            />
            <ToggleRow
              label="限制注册邮箱域名"
              checked={auth.EmailDomainRestrictionEnabled === 'true'}
              onChange={(v) => update('auth', 'EmailDomainRestrictionEnabled', String(v))}
            />
            <Field label="允许的邮箱域名" htmlFor="set-email-domains" hint="逗号分隔，例如 example.com,corp.com；开启域名限制前需先填写">
              <TextInput
                id="set-email-domains"
                value={auth.EmailDomainWhitelist}
                onChange={(e) => update('auth', 'EmailDomainWhitelist', e.target.value)}
              />
            </Field>
            <ToggleRow
              label="启用 Turnstile 人机验证"
              checked={auth.TurnstileCheckEnabled === 'true'}
              onChange={(v) => update('auth', 'TurnstileCheckEnabled', String(v))}
            />
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Turnstile Site Key" htmlFor="set-turnstile-site-key">
                <TextInput
                  id="set-turnstile-site-key"
                  value={auth.TurnstileSiteKey}
                  onChange={(e) => update('auth', 'TurnstileSiteKey', e.target.value)}
                />
              </Field>
              <SecretField
                id="set-turnstile-secret-key"
                label={SECRET_LABELS.TurnstileSecretKey}
                value={auth.TurnstileSecretKey}
                onChange={(v) => update('auth', 'TurnstileSecretKey', v)}
              />
            </div>
          </div>
        </Section>

        <Section
          title="邮件 SMTP"
          hint="用于邮箱验证与通知邮件"
          action={
            <button
              type="button"
              className="inline-flex h-9 items-center rounded-md bg-brand px-3.5 text-body font-medium text-white disabled:opacity-60"
              disabled={saving('smtp')}
              onClick={() => void handleSave('smtp')}
            >
              {saving('smtp') ? '保存中…' : '保存'}
            </button>
          }
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="SMTP 服务器" htmlFor="set-smtp-server">
              <TextInput
                id="set-smtp-server"
                value={smtp.SMTPServer}
                onChange={(e) => update('smtp', 'SMTPServer', e.target.value)}
              />
            </Field>
            <Field label="端口" htmlFor="set-smtp-port">
              <TextInput
                id="set-smtp-port"
                inputMode="numeric"
                value={smtp.SMTPPort}
                onChange={(e) => update('smtp', 'SMTPPort', e.target.value)}
              />
            </Field>
            <Field label="发信账号" htmlFor="set-smtp-account">
              <TextInput
                id="set-smtp-account"
                value={smtp.SMTPAccount}
                onChange={(e) => update('smtp', 'SMTPAccount', e.target.value)}
              />
            </Field>
            <Field label="发件人地址" htmlFor="set-smtp-from">
              <TextInput
                id="set-smtp-from"
                value={smtp.SMTPFrom}
                onChange={(e) => update('smtp', 'SMTPFrom', e.target.value)}
              />
            </Field>
            <SecretField
              id="set-smtp-token"
              label={SECRET_LABELS.SMTPToken}
              value={smtp.SMTPToken}
              onChange={(v) => update('smtp', 'SMTPToken', v)}
            />
            <ToggleRow
              label="使用 SSL 连接"
              checked={smtp.SMTPSSLEnabled === 'true'}
              onChange={(v) => update('smtp', 'SMTPSSLEnabled', String(v))}
            />
          </div>
        </Section>

        <Section
          title="支付"
          hint="易支付/epusdt 与充值入口"
          action={
            <button
              type="button"
              className="inline-flex h-9 items-center rounded-md bg-brand px-3.5 text-body font-medium text-white disabled:opacity-60"
              disabled={saving('payment')}
              onClick={() => void handleSave('payment')}
            >
              {saving('payment') ? '保存中…' : '保存'}
            </button>
          }
        >
          <div className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="支付网关地址" htmlFor="set-pay-address">
                <TextInput
                  id="set-pay-address"
                  value={payment.PayAddress}
                  onChange={(e) => update('payment', 'PayAddress', e.target.value)}
                />
              </Field>
              <Field label="易支付商户 ID" htmlFor="set-epay-id">
                <TextInput
                  id="set-epay-id"
                  value={payment.EpayId}
                  onChange={(e) => update('payment', 'EpayId', e.target.value)}
                />
              </Field>
              <SecretField
                id="set-epay-key"
                label={SECRET_LABELS.EpayKey}
                value={payment.EpayKey}
                onChange={(v) => update('payment', 'EpayKey', v)}
              />
              <Field label="充值回调地址" htmlFor="set-callback-address" hint="留空则使用站点地址 + /api/user/epay/notify">
                <TextInput
                  id="set-callback-address"
                  value={payment.CustomCallbackAddress}
                  onChange={(e) => update('payment', 'CustomCallbackAddress', e.target.value)}
                />
              </Field>
              <Field label="充值单价" htmlFor="set-price" hint="每 1 额度对应的价格（美元）">
                <TextInput
                  id="set-price"
                  inputMode="decimal"
                  value={payment.Price}
                  onChange={(e) => update('payment', 'Price', e.target.value)}
                />
              </Field>
              <Field label="最低充值金额" htmlFor="set-min-topup">
                <TextInput
                  id="set-min-topup"
                  inputMode="numeric"
                  value={payment.MinTopUp}
                  onChange={(e) => update('payment', 'MinTopUp', e.target.value)}
                />
              </Field>
            </div>
            <Field
              label="充值分组倍率"
              htmlFor="set-topup-group-ratio"
              hint='JSON 对象，例如 {"default":1}'
            >
              <TextArea
                id="set-topup-group-ratio"
                rows={2}
                value={payment.TopupGroupRatio}
                onChange={(e) => update('payment', 'TopupGroupRatio', e.target.value)}
              />
            </Field>
          </div>
        </Section>

        <Section
          title="新用户"
          hint={`按金额填写，1 元 = ${quotaPerUnit.toLocaleString()} 额度`}
          action={
            <button
              type="button"
              className="inline-flex h-9 items-center rounded-md bg-brand px-3.5 text-body font-medium text-white disabled:opacity-60"
              disabled={saving('newUser')}
              onClick={() => void handleSave('newUser')}
            >
              {saving('newUser') ? '保存中…' : '保存'}
            </button>
          }
        >
          <div className="grid gap-4 sm:grid-cols-3">
            <YuanField
              id="set-quota-new-user"
              label="新用户赠送"
              value={newUser.QuotaForNewUser}
              onChange={(v) => update('newUser', 'QuotaForNewUser', v)}
            />
            <YuanField
              id="set-quota-inviter"
              label="邀请人奖励"
              value={newUser.QuotaForInviter}
              onChange={(v) => update('newUser', 'QuotaForInviter', v)}
            />
            <YuanField
              id="set-quota-invitee"
              label="被邀请人奖励"
              value={newUser.QuotaForInvitee}
              onChange={(v) => update('newUser', 'QuotaForInvitee', v)}
            />
          </div>
        </Section>
        <Section
          title="渠道重试"
          hint="请求失败后的渠道切换与重试策略"
          action={
            <button
              type="button"
              className="inline-flex h-9 items-center rounded-md bg-brand px-3.5 text-body font-medium text-white disabled:opacity-60"
              disabled={saveRetry.isPending || policy.isError || retryTimes === retryInitial}
              onClick={handleSaveRetry}
            >
              {saveRetry.isPending ? '保存中…' : '保存'}
            </button>
          }
        >
          {policy.isError ? (
            <ErrorBlock error={policy.error} onRetry={() => void policy.refetch()} />
          ) : (
            <Field
              label="最大重试次数"
              htmlFor="set-retry-times"
              hint="请求失败后最多再试几次，0 表示不重试；广场请求始终至少保留 2 次重试"
            >
              <TextInput
                id="set-retry-times"
                inputMode="numeric"
                value={retryTimes}
                onChange={(e) => setRetryDraft(e.target.value)}
              />
            </Field>
          )}
        </Section>
      </div>
    </>
  )
}

function isScopeKey(scope: Scope, key: string): boolean {
  if (scope === 'site') return ['ServerAddress', 'SystemName', 'Notice', 'Footer'].includes(key)
  if (scope === 'auth') {
    return [
      'RegisterEnabled',
      'PasswordRegisterEnabled',
      'EmailVerificationEnabled',
      'EmailDomainRestrictionEnabled',
      'EmailDomainWhitelist',
      'TurnstileCheckEnabled',
      'TurnstileSiteKey',
      'TurnstileSecretKey',
    ].includes(key)
  }
  if (scope === 'smtp') {
    return ['SMTPServer', 'SMTPPort', 'SMTPAccount', 'SMTPFrom', 'SMTPToken', 'SMTPSSLEnabled'].includes(key)
  }
  if (scope === 'payment') {
    return [
      'PayAddress',
      'EpayId',
      'EpayKey',
      'Price',
      'MinTopUp',
      'TopupGroupRatio',
      'CustomCallbackAddress',
    ].includes(key)
  }
  return ['QuotaForNewUser', 'QuotaForInviter', 'QuotaForInvitee'].includes(key)
}

function ToggleRow({
  label,
  checked,
  onChange,
}: {
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <div className="flex items-center justify-between rounded-md border border-line bg-surface/40 px-4 py-3">
      <span className="text-body text-ink">{label}</span>
      <Switch checked={checked} onCheckedChange={onChange} aria-label={label} />
    </div>
  )
}

function SecretField({
  id,
  label,
  value,
  onChange,
}: {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
}) {
  return (
    <Field label={label} htmlFor={id} hint="已保存，留空表示不修改">
      <TextInput
        id={id}
        type="password"
        autoComplete="new-password"
        placeholder="留空表示不修改"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </Field>
  )
}

function YuanField({
  id,
  label,
  value,
  onChange,
}: {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
}) {
  return (
    <Field label={label} htmlFor={id}>
      <div className="flex items-center gap-2">
        <span className="text-body text-ink-3">¥</span>
        <TextInput
          id={id}
          inputMode="decimal"
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      </div>
    </Field>
  )
}
