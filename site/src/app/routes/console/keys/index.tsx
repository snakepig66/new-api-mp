import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { writeClipboard } from '@/lib/clipboard'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Empty,
  PageHeader,
  useToast,
} from '@/ui'

import { consoleKeysQuery } from '@/features/console/api'
import { CreateKeyDialog } from '@/features/console/components/CreateKeyDialog'
import { ErrorBlock, PageSkeleton } from '@/features/console/components/DataState'
import { KeyCard } from '@/features/console/components/KeyCard'
import { Notice } from '@/features/console/components/form'
import { soonestExpiringKey } from '@/features/console/key-expiry'
import { currentBaseUrl } from '@/features/market/url'

export const Route = createFileRoute('/console/keys/')({
  component: KeysPage,
})

const page = copy.pages.consoleKeys

export function KeysPage() {
  const keys = useQuery(consoleKeysQuery())
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [wizardOpen, setWizardOpen] = useState(false)

  function handleCreated() {
    void queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
  }

  function openDetail(tokenId: number) {
    void navigate({ to: '/console/keys/$id', params: { id: String(tokenId) } })
  }

  const expiring = keys.data ? soonestExpiringKey(keys.data) : null

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <Button size="sm" onClick={() => setWizardOpen(true)}>
            {page.create}
          </Button>
        }
      />

      <ApiAddressCard />

      {keys.isPending ? (
        <div className="mt-6">
          <PageSkeleton />
        </div>
      ) : keys.isError ? (
        <ErrorBlock
          className="mt-6"
          error={keys.error}
          onRetry={() => void keys.refetch()}
        />
      ) : keys.data.length === 0 ? (
        <Empty
          className="mt-6"
          title={page.empty.title}
          reason={page.empty.reason}
          next={page.empty.next}
          action={<Button onClick={() => setWizardOpen(true)}>{page.create}</Button>}
        />
      ) : (
        <>
          {expiring ? (
            <Notice className="mt-6" tone="warning" title={page.expirySoon.title}>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span>
                  {page.expirySoon.body(
                    expiring.token.name || copy.pages.consoleShared.none,
                    formatDateTime(expiring.token.expired_time),
                  )}
                </span>
                <Link
                  to="/console/keys/$id"
                  params={{ id: String(expiring.token.id) }}
                  className="shrink-0 font-medium underline underline-offset-2"
                >
                  {page.expirySoon.action}
                </Link>
              </div>
            </Notice>
          ) : null}

          <div className="mt-6 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {keys.data.map((item) => (
              <KeyCard key={item.token.id} item={item} />
            ))}
          </div>
        </>
      )}

      <CreateKeyDialog
        open={wizardOpen}
        onOpenChange={setWizardOpen}
        onCreated={handleCreated}
        onOpenDetail={openDetail}
      />
    </>
  )
}

function ApiAddressCard() {
  const toast = useToast()
  const [copied, setCopied] = useState(false)
  const baseUrl = `${currentBaseUrl()}/v1`

  async function handleCopy() {
    const ok = await writeClipboard(baseUrl)
    if (ok) {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
      toast.show({ title: '已复制 API 地址', tone: 'success' })
    } else {
      toast.show({ title: '复制失败', tone: 'error' })
    }
  }

  return (
    <div className="mt-6 rounded-xl border border-line bg-surface p-4 sm:p-5 shadow-xs">
      <div className="flex items-center gap-2 font-semibold text-title text-ink">
        <svg
          className="h-4 w-4 text-brand"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
        </svg>
        <span>API 地址</span>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-2.5">
        <span className="text-meta font-medium text-ink-2">主地址</span>
        <code className="rounded-md border border-line bg-bg px-2.5 py-1 font-mono text-meta text-ink tabular-nums selection:bg-brand/20">
          {baseUrl}
        </code>
        <Button
          variant="secondary"
          size="sm"
          onClick={handleCopy}
          className="h-7 px-2.5 text-caption"
        >
          {copied ? '已复制' : '复制'}
        </Button>
      </div>
      <p className="mt-2 text-caption text-ink-3">
        每把密钥可以单独设额度、到期时间和可用模型。
      </p>
    </div>
  )
}
