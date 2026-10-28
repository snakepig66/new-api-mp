import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { writeClipboard } from '@/lib/clipboard'
import { cn } from '@/lib/cn'
import { formatYuan } from '@/lib/money'
import { formatDate, formatRelativeTime } from '@/lib/time'
import {
  Button,
  Dialog,
  Input,
  Switch,
  Tag,
  buttonClasses,
  useToast,
} from '@/ui'

import {
  deleteKey,
  revealTokenKey,
  saveKeyTokenLimits,
  setTokenStatus,
} from '@/features/console/api'
import {
  dateTimeInputToUnix,
  unixToDateTimeInput,
} from '@/features/console/format'
import { isTokenExpired } from '@/features/console/labels'
import {
  TOKEN_STATUS_DISABLED,
  TOKEN_STATUS_ENABLED,
  type ConsoleKey,
  type Strategy,
} from '@/features/console/types'

const page = copy.pages.consoleKeys
const shared = copy.pages.consoleShared

const strategyLabelMap: Record<Strategy, string> = {
  cheapest: page.strategy.cheapest,
  balanced: page.strategy.balanced,
  stable: page.strategy.stable,
}

export function maskKey(key: string): string {
  if (!key) return ''
  if (key.includes('*')) return key
  if (key.length <= 12) return key
  return `${key.slice(0, 7)}***********${key.slice(-4)}`
}

export interface KeyCardProps {
  item: ConsoleKey
}

export function KeyCard({ item }: { item: ConsoleKey }) {
  const { token, pref } = item
  const toast = useToast()
  const queryClient = useQueryClient()
  const expired = isTokenExpired(token)
  const strategy = pref?.strategy ?? null

  const [copying, setCopying] = useState(false)
  const [copied, setCopied] = useState(false)
  const [statusBusy, setStatusBusy] = useState(false)

  // Extend dialog state
  const [extendOpen, setExtendOpen] = useState(false)
  const [never, setNever] = useState(false)
  const [expiresAt, setExpiresAt] = useState('')
  const [saving, setSaving] = useState(false)
  const [extendError, setExtendError] = useState<string | null>(null)

  // Delete dialog state
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)

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
        error instanceof ApiError ? error.message : shared.errorHint,
      )
    } finally {
      setSaving(false)
    }
  }

  async function handleToggleStatus(nextChecked: boolean) {
    if (expired) {
      toast.show({ title: 'Key 已过期，请先延长有效期', tone: 'error' })
      return
    }
    setStatusBusy(true)
    try {
      const next = nextChecked ? TOKEN_STATUS_ENABLED : TOKEN_STATUS_DISABLED
      await setTokenStatus(token.id, next)
      toast.show({
        title: nextChecked ? 'Key 已启用' : 'Key 已停用',
        tone: 'success',
      })
      await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
    } catch (error) {
      toast.show({
        title: error instanceof ApiError ? error.message : shared.errorHint,
        tone: 'error',
      })
    } finally {
      setStatusBusy(false)
    }
  }

  async function handleCopyKey() {
    setCopying(true)
    try {
      let realKey = token.key
      // If the list returned a masked key, fetch the full plaintext key
      if (token.key.includes('*')) {
        realKey = await revealTokenKey(token.id)
      }
      const ok = await writeClipboard(realKey)
      if (ok) {
        setCopied(true)
        setTimeout(() => setCopied(false), 2000)
        toast.show({ title: '已复制 API Key', tone: 'success' })
      } else {
        toast.show({ title: shared.copyFailed, tone: 'error' })
      }
    } catch (error) {
      toast.show({
        title: error instanceof ApiError ? error.message : shared.copyFailed,
        tone: 'error',
      })
    } finally {
      setCopying(false)
    }
  }

  async function handleDelete() {
    setDeleting(true)
    setDeleteError(null)
    try {
      await deleteKey(token.id)
      toast.show({ title: 'Key 已删除', tone: 'success' })
      setDeleteOpen(false)
      await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
    } catch (error) {
      setDeleteError(error instanceof ApiError ? error.message : shared.errorHint)
    } finally {
      setDeleting(false)
    }
  }

  const totalQuota = token.used_quota + token.remain_quota
  const usedPercent =
    token.unlimited_quota || totalQuota <= 0
      ? 0
      : Math.min(100, Math.max(0, Math.round((token.used_quota / totalQuota) * 100)))

  return (
    <>
      <article
        data-testid="key-card"
        className={cn(
          'group relative flex flex-col justify-between rounded-xl border border-line bg-surface p-5 shadow-xs transition-all duration-200 hover:border-line-strong hover:shadow-sm',
          expired && 'opacity-60 bg-surface/60',
        )}
      >
        <div className="space-y-3.5">
          {/* Header: Name + Status Switch */}
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0 flex-1">
              <Link
                to="/console/keys/$id"
                params={{ id: String(token.id) }}
                className="block truncate font-semibold text-title text-ink hover:text-brand-ink transition-colors"
              >
                {token.name || shared.none}
              </Link>
              {/* Subtitle: Status indicator + Group */}
              <div className="mt-1 flex items-center gap-1.5 text-meta text-ink-3">
                <span
                  aria-hidden="true"
                  className={cn(
                    'h-2 w-2 shrink-0 rounded-full',
                    expired
                      ? 'bg-warn shadow-[0_0_0_3px_rgb(245_158_11/0.16)]'
                      : token.status === TOKEN_STATUS_ENABLED
                        ? 'bg-[var(--color-ok)] shadow-[0_0_0_3px_rgb(22_163_74/0.14)]'
                        : 'bg-ink-3 shadow-[0_0_0_3px_rgb(118_125_140/0.12)]',
                  )}
                />
                <span>
                  {expired ? '已过期' : token.status === TOKEN_STATUS_ENABLED ? '启用中' : '已停用'}
                </span>
                <span>·</span>
                <span className="font-mono">{token.group || 'default'}</span>
              </div>
            </div>

            <Switch
              checked={!expired && token.status === TOKEN_STATUS_ENABLED}
              disabled={expired || statusBusy}
              onCheckedChange={handleToggleStatus}
              aria-label={token.status === TOKEN_STATUS_ENABLED ? '停用 Key' : '启用 Key'}
            />
          </div>

          {/* Masked Key Box */}
          <div className="flex items-center justify-between gap-2 rounded-lg border border-line bg-bg/80 px-3 py-2">
            <span className="font-mono text-meta text-ink tabular-nums tracking-wide selection:bg-brand/20">
              {maskKey(token.key)}
            </span>
            <button
              type="button"
              onClick={handleCopyKey}
              disabled={copying}
              title="复制 API Key"
              aria-label="复制 API Key"
              className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-ink-3 transition-colors hover:bg-surface hover:text-ink focus:outline-none"
            >
              {copied ? (
                <svg
                  className="h-4 w-4 text-[var(--color-ok)]"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              ) : (
                <svg
                  className="h-4 w-4"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
                  <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
                </svg>
              )}
            </button>
          </div>

          {/* Quota Usage & Progress Bar */}
          <div className="space-y-1.5 pt-0.5">
            <div className="flex items-center justify-between text-meta">
              <span className="text-ink-2">
                已用 <span className="font-semibold text-ink tabular-nums">{formatYuan(token.used_quota)}</span>
              </span>
              <span className="text-ink-3 tabular-nums">
                {token.unlimited_quota
                  ? '∞ 不限额'
                  : page.spend.remaining(formatYuan(token.remain_quota))}
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-line">
              <div
                className={cn(
                  'h-full rounded-full transition-all duration-300',
                  usedPercent >= 90 ? 'bg-danger' : usedPercent >= 75 ? 'bg-warn' : 'bg-brand',
                )}
                style={{ width: `${token.unlimited_quota ? 0 : usedPercent}%` }}
              />
            </div>
          </div>

          {/* Expiry & Last Activity */}
          <div className="flex items-center justify-between text-caption text-ink-3">
            <span>
              {token.expired_time <= 0
                ? '永不过期'
                : expired
                  ? '已过期'
                  : `${formatDate(token.expired_time)} 到期`}
            </span>
            <span>
              {!token.accessed_time || token.accessed_time <= 0
                ? '从未使用'
                : `最近使用 ${formatRelativeTime(token.accessed_time)}`}
            </span>
          </div>
        </div>

        {/* Footer: Tags on left, Edit & Delete & Extend on right */}
        <div className="mt-4 flex items-center justify-between gap-2 border-t border-line/60 pt-3">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <Tag tone="neutral" className="text-caption">
              {pref?.model_scope === 'all'
                ? page.createDialog.scope.all
                : page.modelsUnit(pref?.models.length ?? 0)}
            </Tag>
            {strategy ? (
              <Tag tone="brand" className="text-caption">
                {strategyLabelMap[strategy]}
              </Tag>
            ) : null}
          </div>

          <div className="flex shrink-0 items-center gap-1.5">
            {expired ? (
              <Button
                variant="secondary"
                size="sm"
                onClick={openExtend}
                className="h-7 px-2.5 text-caption"
              >
                {page.extend.action}
              </Button>
            ) : null}
            <Link
              to="/console/keys/$id"
              params={{ id: String(token.id) }}
              className={cn(buttonClasses('secondary', 'sm'), 'h-7 px-2 text-caption')}
              title="编辑 Key"
              aria-label="编辑 Key"
            >
              <svg
                className="h-3.5 w-3.5"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
                <path d="m15 5 4 4" />
              </svg>
            </Link>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setDeleteOpen(true)}
              className="h-7 px-2 text-caption text-danger hover:border-danger hover:text-danger"
              title="删除 Key"
              aria-label="删除 Key"
            >
              <svg
                className="h-3.5 w-3.5"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M3 6h18" />
                <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" />
                <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
              </svg>
            </Button>
          </div>
        </div>
      </article>

      {/* Extend Dialog */}
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
              {shared.cancel}
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
          <label className="inline-flex items-center gap-2 text-meta text-ink">
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
            <p role="alert" className="text-meta text-danger">
              {extendError}
            </p>
          ) : null}
        </div>
      </Dialog>

      {/* Delete Dialog */}
      <Dialog
        open={deleteOpen}
        onOpenChange={(open) => {
          if (!deleting) setDeleteOpen(open)
        }}
        title="删除 API Key"
        description={`确定要删除「${token.name || shared.none}」吗？删除后此 Key 将立即失效且无法恢复。`}
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => setDeleteOpen(false)}
              disabled={deleting}
            >
              {shared.cancel}
            </Button>
            <Button
              variant="danger"
              loading={deleting}
              onClick={() => void handleDelete()}
            >
              {deleting ? shared.deleting : shared.delete}
            </Button>
          </>
        }
      >
        {deleteError ? (
          <p role="alert" className="text-meta text-danger">
            {deleteError}
          </p>
        ) : null}
      </Dialog>
    </>
  )
}
