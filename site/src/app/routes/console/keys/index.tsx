import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, createFileRoute, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import {
  Button,
  Dialog,
  Empty,
  Input,
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  buttonClasses,
  useToast,
} from '@/ui'

import { consoleKeysQuery, saveKeyTokenLimits } from '@/features/console/api'
import { CreateKeyDialog } from '@/features/console/components/CreateKeyDialog'
import { ErrorBlock, PageSkeleton, Section } from '@/features/console/components/DataState'
import { Notice } from '@/features/console/components/form'
import {
  dateTimeInputToUnix,
  unixToDateTimeInput,
} from '@/features/console/format'
import { soonestExpiringKey } from '@/features/console/key-expiry'
import { isTokenExpired, tokenStatusMeta } from '@/features/console/labels'
import {
  TOKEN_STATUS_EXPIRED,
  type ConsoleKey,
  type Strategy,
} from '@/features/console/types'

export const Route = createFileRoute('/console/keys/')({
  component: KeysPage,
})

const page = copy.pages.consoleKeys

const strategyLabelMap: Record<Strategy, string> = {
  cheapest: page.strategy.cheapest,
  balanced: page.strategy.balanced,
  stable: page.strategy.stable,
}

const strategyHintMap: Record<Strategy, string> = {
  cheapest: page.strategyHint.cheapest,
  balanced: page.strategyHint.balanced,
  stable: page.strategyHint.stable,
}

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
          <div className="mt-6">
            <Section title={copy.pages.console.nav.keys} hint={page.lead}>
            <Table minWidth={880}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell>{page.columns.name}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.models}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.strategy}</TableHeaderCell>
                  <TableHeaderCell numeric>{page.columns.spend}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.status}</TableHeaderCell>
                  <TableHeaderCell>{page.columns.actions}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {keys.data.map((item) => (
                  <KeyRow key={item.token.id} item={item} />
                ))}
              </TableBody>
            </Table>
          </Section>
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

function KeyRow({ item }: { item: ConsoleKey }) {
  const { token, pref } = item
  const toast = useToast()
  const queryClient = useQueryClient()
  const expired = isTokenExpired(token)
  const status = tokenStatusMeta(expired ? TOKEN_STATUS_EXPIRED : token.status)
  const strategy = pref?.strategy ?? null
  const [extendOpen, setExtendOpen] = useState(false)
  const [never, setNever] = useState(false)
  const [expiresAt, setExpiresAt] = useState('')
  const [saving, setSaving] = useState(false)
  const [extendError, setExtendError] = useState<string | null>(null)

  function openExtend() {
    setNever(false)
    setExpiresAt(unixToDateTimeInput(token.expired_time))
    setExtendError(null)
    setExtendOpen(true)
  }

  async function extend() {
    let expiredTime = -1
    if (!never) {
      const parsed = dateTimeInputToUnix(expiresAt)
      if (parsed === null || parsed <= Math.floor(Date.now() / 1000)) {
        setExtendError(page.extend.invalid)
        return
      }
      expiredTime = parsed
    }
    setSaving(true)
    setExtendError(null)
    try {
      await saveKeyTokenLimits({
        token_id: token.id,
        expired_time: expiredTime,
        allow_ips: token.allow_ips ?? '',
      })
      toast.show({ title: page.extend.extended, tone: 'success' })
      await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
      await queryClient.invalidateQueries({ queryKey: ['console', 'key', token.id] })
      setExtendOpen(false)
    } catch (error) {
      setExtendError(
        error instanceof ApiError ? error.message : copy.pages.consoleShared.errorHint,
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <TableRow interactive className={expired ? 'opacity-60' : undefined}>
      <TableCell>
        <Link
          to="/console/keys/$id"
          params={{ id: String(token.id) }}
          className="font-medium text-ink hover:text-brand-ink"
        >
          {token.name || copy.pages.consoleShared.none}
        </Link>
        <div className="tabular-nums text-[13px] text-ink-3">{token.key}</div>
      </TableCell>
      <TableCell numeric>
        <span className="text-[13px]">
          {pref?.model_scope === 'all'
            ? copy.pages.consoleKeys.createDialog.scope.all
            : copy.pages.consoleKeys.modelsUnit(pref?.models.length ?? 0)}
        </span>
      </TableCell>
      <TableCell>
        {strategy ? (
          <>
            <Tag tone="brand">{strategyLabelMap[strategy]}</Tag>
            <div className="mt-1 text-[13px] text-ink-3">{strategyHintMap[strategy]}</div>
          </>
        ) : (
          <span className="text-[13px] text-ink-3">{copy.pages.consoleShared.none}</span>
        )}
      </TableCell>
      <TableCell numeric>
        <div className="text-[13px]">{page.spend.used(formatYuan(token.used_quota))}</div>
        <div className="text-[13px] text-ink-3">
          {token.unlimited_quota
            ? page.spend.unlimited
            : page.spend.remaining(formatYuan(token.remain_quota))}
        </div>
      </TableCell>
      <TableCell>
        <Tag tone={status.tone} dot>
          {status.label}
        </Tag>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-2">
          <Link
            to="/console/keys/$id"
            params={{ id: String(token.id) }}
            className={buttonClasses('secondary', 'sm')}
          >
            {copy.pages.consoleShared.detail}
          </Link>
          {expired ? (
            <Button variant="secondary" size="sm" onClick={openExtend}>
              {page.extend.action}
            </Button>
          ) : null}
        </div>
      </TableCell>
    </TableRow>
      <Dialog
        open={extendOpen}
        onOpenChange={(open) => {
          if (!saving) setExtendOpen(open)
        }}
        title={page.extend.title}
        description={page.extend.hint}
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => setExtendOpen(false)}
              disabled={saving}
            >
              {copy.pages.consoleShared.cancel}
            </Button>
            <Button loading={saving} onClick={() => void extend()}>
              {saving ? page.extend.saving : page.extend.confirm}
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <Input
            aria-label={page.extend.newExpiry}
            type="datetime-local"
            disabled={never}
            value={expiresAt}
            onChange={(event) => setExpiresAt(event.target.value)}
          />
          <label className="inline-flex items-center gap-2 text-[13px] text-ink">
            <input
              type="checkbox"
              className="h-3.5 w-3.5 accent-brand"
              checked={never}
              onChange={(event) => {
                setNever(event.target.checked)
                if (event.target.checked) setExpiresAt('')
              }}
            />
            {page.extend.never}
          </label>
          {extendError ? (
            <p role="alert" className="text-[13px] text-danger">
              {extendError}
            </p>
          ) : null}
        </div>
      </Dialog>
    </>
  )
}
