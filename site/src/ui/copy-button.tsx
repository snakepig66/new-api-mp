import { useEffect, useRef, useState } from 'react'

import { copy } from '@/copy'
import { writeClipboard } from '@/lib/clipboard'
import { cn } from '@/lib/cn'

import { Button } from './button'
import { useToast } from './toast'

export interface CopyButtonProps {
  /** Plain text, or a lazy resolver (e.g. reveal a masked key before copying). */
  text: string | (() => string | Promise<string>)
  /** Idle label / accessible name. */
  label?: string
  /** Success toast text; null (default) keeps the copy silent. */
  toast?: string | null
  variant?: 'icon' | 'button'
  className?: string
}

/**
 * Unified copy control: idle → busy → ✓ for two seconds, with an optional
 * success toast and a shared failure toast (P4: 复制反馈统一). The clipboard
 * write itself lives in @/lib/clipboard so every caller shares one fallback.
 */
export function CopyButton({
  text,
  label = '复制',
  toast = null,
  variant = 'button',
  className,
}: CopyButtonProps) {
  const [state, setState] = useState<'idle' | 'busy' | 'done'>('idle')
  const timer = useRef<number | null>(null)
  const toastApi = useToast()

  useEffect(
    () => () => {
      if (timer.current) window.clearTimeout(timer.current)
    },
    [],
  )

  const handleCopy = async () => {
    if (state === 'busy') return
    setState('busy')
    try {
      const value = (typeof text === 'function' ? await text() : text).trim()
      if (!value) {
        setState('idle')
        return
      }
      const ok = await writeClipboard(value)
      if (!ok) {
        toastApi.show({ title: copy.common.copyFailed, tone: 'error' })
        setState('idle')
        return
      }
      setState('done')
      if (toast !== null) toastApi.show({ title: toast, tone: 'success' })
      if (timer.current) window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setState('idle'), 2000)
    } catch {
      toastApi.show({ title: copy.common.copyFailed, tone: 'error' })
      setState('idle')
    }
  }

  if (variant === 'icon') {
    return (
      <button
        type="button"
        onClick={() => void handleCopy()}
        disabled={state === 'busy'}
        title={label}
        aria-label={label}
        className={cn(
          'inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-ink-3 transition-colors hover:bg-surface hover:text-ink focus:outline-none disabled:opacity-60',
          className,
        )}
      >
        {state === 'done' ? (
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
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </svg>
        )}
      </button>
    )
  }

  return (
    <Button
      variant="secondary"
      size="sm"
      loading={state === 'busy'}
      onClick={() => void handleCopy()}
      className={className}
    >
      {state === 'done' ? copy.common.copied : label}
    </Button>
  )
}
