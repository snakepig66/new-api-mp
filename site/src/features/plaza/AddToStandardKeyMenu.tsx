import { useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

import { copy } from '@/copy'
import { consoleKeysQuery, updateToken } from '@/features/console/api'
import { CreateKeyDialog } from '@/features/console/components/CreateKeyDialog'
import {
  TOKEN_STATUS_ENABLED,
  type ConsoleKey,
} from '@/features/console/types'
import { ensureAuth, useAuth } from '@/lib/auth'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Empty, Tag, useToast } from '@/ui'

const page = copy.pages.plaza.addToKeyStandard

export interface AddToStandardKeyMenuProps {
  model: string
  size?: 'sm' | 'md' | 'lg'
  className?: string
}

type KeyState = 'available' | 'all' | 'added' | 'disabled'

interface KeyOption {
  item: ConsoleKey
  state: KeyState
}

function splitLimits(value: string | null | undefined): string[] {
  return (value ?? '')
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
}

function keyState(item: ConsoleKey, model: string): KeyState {
  const token = item.token
  if (token.status !== TOKEN_STATUS_ENABLED) return 'disabled'
  if (!token.model_limits_enabled) return 'all'
  if (splitLimits(token.model_limits).includes(model)) return 'added'
  return 'available'
}

function withModel(token: ConsoleKey['token'], model: string): string {
  const limits = splitLimits(token.model_limits)
  if (!limits.includes(model)) limits.push(model)
  return limits.join(',')
}

function keyName(item: ConsoleKey): string {
  return item.token.name || `Key #${item.token.id}`
}

/**
 * Plaza row action: adds the model to the buyer's standard keys through
 * new-api's own token model limits (PUT /api/token/ with the full token row).
 * Standard keys without a model limit already serve every plaza model, so
 * they are shown as 已可用（全部模型） and cannot be selected.
 */
export function AddToStandardKeyMenu({
  model,
  size = 'sm',
  className,
}: AddToStandardKeyMenuProps) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { isLoggedIn } = useAuth()
  const currentHref = useRouterState({ select: (state) => state.location.href })

  const triggerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [checkingAuth, setCheckingAuth] = useState(false)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [submitting, setSubmitting] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [position, setPosition] = useState<{
    left: number
    width: number
    maxHeight: number
    top?: number
    bottom?: number
  }>({ left: 12, width: 320, maxHeight: 360, top: 0 })

  const keysQuery = useQuery({ ...consoleKeysQuery(), enabled: open })
  const options: KeyOption[] = (keysQuery.data ?? [])
    .filter((item) => item.token.key_type === 'standard')
    .map((item) => ({ item, state: keyState(item, model) }))
  const selectedCount = selected.size

  useEffect(() => {
    if (!open) {
      setSelected(new Set())
      return
    }
    const trigger = triggerRef.current
    if (!trigger) return
    const rect = trigger.getBoundingClientRect()
    const width = Math.min(320, window.innerWidth - 24)
    const left = Math.min(
      Math.max(12, rect.right - width),
      Math.max(12, window.innerWidth - width - 12),
    )
    const spaceBelow = window.innerHeight - rect.bottom - 16
    const spaceAbove = rect.top - 16
    const openUp = spaceBelow < 240 && spaceAbove > spaceBelow
    setPosition(
      openUp
        ? {
            left,
            width,
            maxHeight: Math.min(360, Math.max(160, spaceAbove)),
            bottom: window.innerHeight - rect.top + 8,
          }
        : {
            left,
            width,
            maxHeight: Math.min(360, Math.max(160, spaceBelow)),
            top: rect.bottom + 8,
          },
    )
  }, [open])

  useEffect(() => {
    if (!open) return
    function close() {
      setOpen(false)
    }
    function onPointerDown(event: PointerEvent) {
      const target = event.target as Node
      if (panelRef.current?.contains(target) || triggerRef.current?.contains(target)) return
      close()
    }
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') close()
    }
    function onScroll(event: Event) {
      if (panelRef.current?.contains(event.target as Node)) return
      close()
    }
    document.addEventListener('pointerdown', onPointerDown, true)
    document.addEventListener('keydown', onKeyDown)
    window.addEventListener('resize', close)
    window.addEventListener('scroll', onScroll, true)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown, true)
      document.removeEventListener('keydown', onKeyDown)
      window.removeEventListener('resize', close)
      window.removeEventListener('scroll', onScroll, true)
      triggerRef.current?.focus()
    }
  }, [open])

  async function handleTrigger() {
    if (open) {
      setOpen(false)
      return
    }
    if (isLoggedIn) {
      setOpen(true)
      return
    }
    setCheckingAuth(true)
    try {
      const authenticated = await ensureAuth()
      if (!authenticated) {
        void navigate({ to: '/login', search: { redirect: currentHref } })
        return
      }
      setOpen(true)
    } finally {
      setCheckingAuth(false)
    }
  }

  function toggle(id: number) {
    setSelected((previous) => {
      const next = new Set(previous)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function submit() {
    const targets = options.filter(
      (option) => option.state === 'available' && selected.has(option.item.token.id),
    )
    if (targets.length === 0) return
    setSubmitting(true)
    const results = await Promise.allSettled(
      targets.map((option) =>
        updateToken({
          ...option.item.token,
          model_limits_enabled: true,
          model_limits: withModel(option.item.token, model),
        }),
      ),
    )
    let added = 0
    let failed = 0
    for (const result of results) {
      if (result.status === 'fulfilled') added++
      else failed++
    }
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['console', 'keys'] }),
      queryClient.invalidateQueries({ queryKey: ['tokens', 'list'] }),
    ])
    setSubmitting(false)
    setOpen(false)
    if (added > 0 && failed === 0) {
      toast.show({ title: page.success(added), tone: 'success' })
    } else if (added > 0) {
      toast.show({ title: page.partial(added, failed), tone: 'default' })
    } else {
      toast.show({ title: page.failed, tone: 'error' })
    }
  }

  async function refreshAfterCreate() {
    await queryClient.invalidateQueries({ queryKey: ['console', 'keys'] })
    await queryClient.invalidateQueries({ queryKey: ['tokens', 'list'] })
  }

  return (
    <>
      <Button
        ref={triggerRef}
        type="button"
        variant="secondary"
        size={size}
        className={className}
        loading={checkingAuth}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => void handleTrigger()}
      >
        {page.action}
      </Button>

      {open
        ? createPortal(
            <div
              ref={panelRef}
              role="dialog"
              aria-label={page.dialogLabel}
              style={{
                position: 'fixed',
                left: position.left,
                width: position.width,
                maxHeight: position.maxHeight,
                top: position.top,
                bottom: position.bottom,
                zIndex: 50,
              }}
              className="flex min-h-0 flex-col overflow-hidden glass rounded-lg shadow-xl"
            >
              <div className="shrink-0 border-b border-line px-4 py-3">
                <p className="text-body font-semibold text-ink">{page.title}</p>
                <p className="mt-0.5 text-caption leading-5 text-ink-3">{page.hint}</p>
              </div>

              <div className="min-h-0 flex-1 overflow-y-auto">
                {keysQuery.isPending ? (
                  <div className="space-y-2 p-3" aria-label={page.loading}>
                    <div className="h-8 animate-pulse rounded-sm bg-line" />
                    <div className="h-8 animate-pulse rounded-sm bg-line" />
                    <div className="h-8 animate-pulse rounded-sm bg-line" />
                  </div>
                ) : keysQuery.isError ? (
                  <div className="p-3">
                    <p className="text-meta text-ink-2">{page.loadError}</p>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="mt-2"
                      onClick={() => void keysQuery.refetch()}
                    >
                      {page.retry}
                    </Button>
                  </div>
                ) : options.length === 0 ? (
                  <Empty
                    className="border-0 px-4 py-6"
                    title={page.emptyTitle}
                    reason={page.emptyReason}
                    next={page.emptyNext}
                  />
                ) : (
                  <ul className="divide-y divide-line">
                    {options.map(({ item, state }) => {
                      const token = item.token
                      const checked = state === 'all' || state === 'added'
                      const disabled = state !== 'available' || submitting
                      return (
                        <li key={token.id}>
                          <label
                            className={
                              disabled
                                ? 'flex cursor-not-allowed items-center gap-2.5 px-4 py-2.5 opacity-60'
                                : 'flex cursor-pointer items-center gap-2.5 px-4 py-2.5'
                            }
                          >
                            <input
                              type="checkbox"
                              className="h-3.5 w-3.5 shrink-0 accent-brand"
                              checked={checked}
                              disabled={disabled}
                              onChange={() => toggle(token.id)}
                            />
                            <span className="min-w-0 flex-1">
                              <span className="block truncate text-meta text-ink">
                                {keyName(item)}
                              </span>
                              <span className="block text-caption tabular-nums text-ink-3">
                                #{token.id}
                              </span>
                            </span>
                            <KeyStateTag state={state} />
                          </label>
                        </li>
                      )
                    })}
                  </ul>
                )}
              </div>

              <div className="flex shrink-0 items-center justify-between gap-2 border-t border-line px-3 py-2.5">
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => {
                    setOpen(false)
                    setCreateOpen(true)
                  }}
                >
                  {page.createKey}
                </Button>
                <Button
                  size="sm"
                  loading={submitting}
                  disabled={selectedCount === 0}
                  onClick={() => void submit()}
                >
                  {submitting ? page.adding : page.confirm(selectedCount)}
                </Button>
              </div>
            </div>,
            document.body,
          )
        : null}

      {createOpen ? (
        <CreateKeyDialog
          open
          onOpenChange={setCreateOpen}
          onCreated={() => void refreshAfterCreate()}
          initialModel={model}
          initialKeyType="standard"
        />
      ) : null}
    </>
  )
}

function KeyStateTag({ state }: { state: KeyState }) {
  if (state === 'all') return <Tag tone="brand">{page.allModels}</Tag>
  if (state === 'added') return <Tag tone="brand">{page.added}</Tag>
  if (state === 'disabled') return <Tag>{page.disabledKey}</Tag>
  return null
}
