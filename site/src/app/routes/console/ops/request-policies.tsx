/**
 * 运营后台 · 请求策略。
 *
 * 对应 new-api 的 系统设置 → 请求策略 → 会话与重试（routing）：渠道页顶部
 * 「最大重试」徽章直接落到这里。当前迁移重试预算（RetryTimes 与自动重试
 * 状态码）；会话亲和、请求过滤与渠道健康按需再补。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'

import {
  ErrorBlock,
  Field,
  OpsTabNav,
  PageSkeleton,
  RootOnly,
  Section,
  TextInput,
  type OpsTabLink,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import { parseHttpStatusCodeRules } from '@/features/admin/http-status-code-rules'
import {
  requestPolicyQuery,
  updateRequestPolicy,
  type OptionMap,
} from '@/features/admin/settings-api'
import { PageHeader, useToast } from '@/ui'

export const Route = createFileRoute('/console/ops/request-policies')({
  component: RequestPoliciesPage,
})

const tabs: readonly OpsTabLink[] = [
  { to: '/console/ops/settings', label: '系统设置' },
  { to: '/console/ops/request-policies', label: '请求策略' },
]

type PolicyForm = {
  RetryTimes: string
  AutomaticRetryStatusCodes: string
}

function formFrom(options: OptionMap): PolicyForm {
  return {
    RetryTimes: options.RetryTimes ?? '0',
    AutomaticRetryStatusCodes: options.AutomaticRetryStatusCodes ?? '',
  }
}

function RequestPoliciesPage() {
  return (
    <RootOnly>
      <RequestPoliciesContent />
    </RootOnly>
  )
}

function RequestPoliciesContent() {
  const toast = useToast()
  const queryClient = useQueryClient()
  const policy = useQuery(requestPolicyQuery())
  const [initial, setInitial] = useState<PolicyForm | null>(null)
  const [form, setForm] = useState<PolicyForm | null>(null)

  useEffect(() => {
    if (!policy.data || initial) return
    const next = formFrom(policy.data)
    setInitial(next)
    setForm(next)
  }, [policy.data, initial])

  const save = useMutation({
    mutationFn: (options: OptionMap) => updateRequestPolicy(options),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: requestPolicyQuery().queryKey,
        }),
        // 渠道页顶部的「最大重试」徽章读的是 /channel/ops，保存后一并刷新。
        queryClient.invalidateQueries({ queryKey: ['channel-ops'] }),
      ])
      toast.show({ title: '请求策略已保存', tone: 'success' })
    },
    onError: (error) =>
      toast.show({ title: adminErrorMessage(error), tone: 'error' }),
  })

  if (policy.isPending) {
    return (
      <>
        <OpsTabNav tabs={tabs} ariaLabel="运营后台导航" />
        <PageHeader title="请求策略" lead="渠道失败重试与自动重试状态码。" />
        <div className="mt-6">
          <PageSkeleton />
        </div>
      </>
    )
  }

  if (policy.isError || !initial || !form) {
    return (
      <>
        <OpsTabNav tabs={tabs} ariaLabel="运营后台导航" />
        <PageHeader title="请求策略" lead="渠道失败重试与自动重试状态码。" />
        <div className="mt-6">
          <ErrorBlock
            error={policy.error ?? new Error('加载失败')}
            onRetry={() => void policy.refetch()}
          />
        </div>
      </>
    )
  }

  const changes = (): OptionMap | null => {
    const retry = form.RetryTimes.trim()
    if (!/^\d+$/.test(retry)) {
      toast.show({ title: '最大重试次数必须是非负整数', tone: 'error' })
      return null
    }
    const retryTimes = Number(retry)
    if (!Number.isSafeInteger(retryTimes) || retryTimes > 99) {
      toast.show({ title: '最大重试次数需在 0–99 之间', tone: 'error' })
      return null
    }
    const codes = parseHttpStatusCodeRules(form.AutomaticRetryStatusCodes)
    if (!codes.ok) {
      toast.show({
        title: `自动重试状态码无效：${codes.invalidTokens.join('、')}`,
        tone: 'error',
      })
      return null
    }
    const next = {
      RetryTimes: String(retryTimes),
      AutomaticRetryStatusCodes: codes.normalized,
    }
    const delta: OptionMap = {}
    if (next.RetryTimes !== initial.RetryTimes) {
      delta.RetryTimes = next.RetryTimes
    }
    if (
      next.AutomaticRetryStatusCodes !==
      parseHttpStatusCodeRules(initial.AutomaticRetryStatusCodes).normalized
    ) {
      delta.AutomaticRetryStatusCodes = next.AutomaticRetryStatusCodes
    }
    return delta
  }

  async function handleSave() {
    const delta = changes()
    if (!delta) return
    if (Object.keys(delta).length === 0) {
      toast.show({ title: '没有需要保存的修改' })
      return
    }
    await save.mutateAsync(delta)
    const next = formFrom({ ...policy.data, ...delta })
    setInitial(next)
    setForm(next)
  }

  const dirty =
    form.RetryTimes !== initial.RetryTimes ||
    parseHttpStatusCodeRules(form.AutomaticRetryStatusCodes).normalized !==
      parseHttpStatusCodeRules(initial.AutomaticRetryStatusCodes).normalized

  return (
    <>
      <OpsTabNav tabs={tabs} ariaLabel="运营后台导航" />
      <PageHeader
        title="请求策略"
        lead="渠道失败重试与自动重试状态码。"
      />

      <div className="mt-6 space-y-6">
        <Section
          title="重试预算"
          hint="请求失败后切换渠道重试"
          action={
            <button
              type="button"
              className="inline-flex h-9 items-center rounded-md bg-brand px-3.5 text-body font-medium text-white disabled:opacity-60"
              disabled={save.isPending || !dirty}
              onClick={() => void handleSave()}
            >
              {save.isPending ? '保存中…' : '保存'}
            </button>
          }
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              label="最大重试次数"
              htmlFor="policy-retry-times"
              hint="不含首次请求；0 表示不重试。广场请求始终至少保留 2 次重试。"
            >
              <TextInput
                id="policy-retry-times"
                inputMode="numeric"
                value={form.RetryTimes}
                onChange={(e) =>
                  setForm({ ...form, RetryTimes: e.target.value })
                }
              />
            </Field>
            <Field
              label="自动重试状态码"
              htmlFor="policy-retry-codes"
              hint="如 401, 403, 429, 500-599；2xx、504、524 始终排除。"
            >
              <TextInput
                id="policy-retry-codes"
                value={form.AutomaticRetryStatusCodes}
                onChange={(e) =>
                  setForm({
                    ...form,
                    AutomaticRetryStatusCodes: e.target.value,
                  })
                }
              />
            </Field>
          </div>
        </Section>
      </div>
    </>
  )
}
