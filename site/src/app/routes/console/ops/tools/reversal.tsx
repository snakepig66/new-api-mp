import { useMutation } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { formatYuan } from '@/lib/money'
import { Button, PageHeader, useToast } from '@/ui'

import {
  deanonBuyer,
  reverseAdminRequest,
  type AdminDeanonResult,
  type AdminReverseResult,
} from '@/features/admin/api'
import {
  Field,
  Notice,
  RootOnly,
  Section,
  TextInput,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'

export const Route = createFileRoute('/console/ops/tools/reversal')({
  component: AdminToolsPage,
})

const page = copy.pages.adminTools

function AdminToolsPage() {
  return (
    <RootOnly>
      <ToolsContent />
    </RootOnly>
  )
}

function ToolsContent() {
  return (
    <>
      <PageHeader title={page.title} lead={page.lead} />
      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <Section title={page.reverse.title} hint={page.reverse.lead}>
          <ReverseForm />
        </Section>
        <Section
          title={page.deanon.title}
          hint={page.deanon.lead}
        >
          <DeanonForm />
        </Section>
      </div>
    </>
  )
}

function accountLabel(account: string): string {
  const labels = page.account
  if (account === 'platform') return labels.platform
  const [prefix, id] = account.split(':')
  if (prefix === 'buyer') return labels.buyer(id)
  if (prefix === 'seller') return labels.seller(id)
  if (prefix === 'user') return labels.user(id)
  return account
}

function ReverseForm() {
  const toast = useToast()
  const [requestId, setRequestId] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<AdminReverseResult | null>(null)

  const mutation = useMutation({
    mutationFn: reverseAdminRequest,
    onSuccess: (data) => {
      setResult(data)
      toast.show({ title: page.reverse.success, tone: 'success' })
    },
    onError: (err) => {
      setResult(null)
      setError(adminErrorMessage(err))
    },
  })

  function submit() {
    const value = requestId.trim()
    if (!value) {
      setError(page.reverse.required)
      return
    }
    setError(null)
    setResult(null)
    mutation.mutate(value)
  }

  const balances = result ? Object.entries(result.balances) : []

  return (
    <div className="space-y-4">
      <Field label={page.reverse.requestId} htmlFor="reverse-request-id" error={error}>
        <TextInput
          id="reverse-request-id"
          mono={false}
          placeholder={page.reverse.placeholder}
          value={requestId}
          invalid={Boolean(error)}
          onChange={(event) => {
            setRequestId(event.target.value)
            setError(null)
          }}
        />
      </Field>
      <Button loading={mutation.isPending} onClick={submit}>
        {mutation.isPending ? page.reverse.submitting : page.reverse.submit}
      </Button>

      {result ? (
        <div className="border-t border-line pt-4">
          <h3 className="text-[13px] font-medium text-ink">
            {page.reverse.balancesTitle}
          </h3>
          {balances.length === 0 ? (
            <p className="mt-2 text-[13px] text-ink-3">{page.reverse.balancesEmpty}</p>
          ) : (
            <dl className="mt-2 divide-y divide-line border-y border-line">
              {balances.map(([account, value]) => (
                <div
                  key={account}
                  className="flex items-baseline justify-between gap-4 py-2"
                >
                  <dt className="min-w-0 truncate text-[13px] text-ink-3">
                    {accountLabel(account)}
                  </dt>
                  <dd className="shrink-0 tabular-nums text-[13px] tabular-nums text-ink">
                    {formatYuan(value)}
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      ) : null}
    </div>
  )
}

function DeanonForm() {
  const [sellerId, setSellerId] = useState('')
  const [code, setCode] = useState('')
  const [errors, setErrors] = useState<{ sellerId?: string; code?: string }>({})
  const [notFound, setNotFound] = useState(false)
  const [result, setResult] = useState<AdminDeanonResult | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: ({ sellerId: id, code: value }: { sellerId: number; code: string }) =>
      deanonBuyer(id, value),
    onSuccess: (data) => {
      setResult(data)
      setNotFound(false)
      setSubmitError(null)
    },
    onError: (err) => {
      setResult(null)
      if (err instanceof ApiError && err.status === 404) {
        setNotFound(true)
        setSubmitError(null)
        return
      }
      setNotFound(false)
      setSubmitError(adminErrorMessage(err))
    },
  })

  function submit() {
    const idText = sellerId.trim()
    const codeText = code.trim()
    const next: { sellerId?: string; code?: string } = {}
    if (!idText) next.sellerId = page.deanon.sellerRequired
    else if (!/^\d+$/.test(idText) || Number(idText) <= 0) {
      next.sellerId = page.deanon.sellerInvalid
    }
    if (!codeText) next.code = page.deanon.codeRequired
    setErrors(next)
    setNotFound(false)
    setResult(null)
    setSubmitError(null)
    if (Object.keys(next).length > 0) return
    mutation.mutate({ sellerId: Number(idText), code: codeText })
  }

  return (
    <div className="space-y-4">
      <Field label={page.deanon.sellerId} htmlFor="deanon-seller-id" error={errors.sellerId}>
        <TextInput
          id="deanon-seller-id"
          inputMode="numeric"
          placeholder={page.deanon.sellerPlaceholder}
          value={sellerId}
          invalid={Boolean(errors.sellerId)}
          onChange={(event) => {
            setSellerId(event.target.value)
            setErrors((current) => ({ ...current, sellerId: undefined }))
          }}
        />
      </Field>
      <Field label={page.deanon.code} htmlFor="deanon-code" error={errors.code}>
        <TextInput
          id="deanon-code"
          placeholder={page.deanon.codePlaceholder}
          value={code}
          invalid={Boolean(errors.code)}
          onChange={(event) => {
            setCode(event.target.value)
            setErrors((current) => ({ ...current, code: undefined }))
          }}
        />
      </Field>
      <Button loading={mutation.isPending} onClick={submit}>
        {mutation.isPending ? page.deanon.submitting : page.deanon.submit}
      </Button>

      {notFound ? (
        <Notice tone="warning" title={page.deanon.notFoundTitle}>
          {page.deanon.notFoundReason}
        </Notice>
      ) : null}

      {submitError ? (
        <Notice tone="warning">
          <span role="alert">{submitError}</span>
        </Notice>
      ) : null}

      {result ? (
        <div className="border-t border-line pt-4">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <span className="text-[13px] text-ink-3">{page.deanon.result}</span>
            <span className="tabular-nums text-[18px] font-medium tabular-nums text-ink">
              #{result.buyer_user_id}
            </span>
          </div>
          <p className="mt-1 text-[13px] text-ink-3">
            {page.account.seller(String(result.seller_id))} ·{' '}
            <span className="tabular-nums uppercase">{result.code}</span>
          </p>
        </div>
      ) : null}
    </div>
  )
}
