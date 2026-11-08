import { useEffect, useRef, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

import { copyText } from './clipboard'

export interface CopyButtonProps {
  text: string
  /** Idle label; defaults to the shared copy string. */
  label?: string
  copiedLabel?: string
  failedLabel?: string
  className?: string
}

/**
 * Small inline copy control for base_url / model rows. Copy success is shown
 * in place for 1.6s; failure asks the reader to select the text manually.
 */
export function CopyButton({
  text,
  label = copy.pages.home.copy,
  copiedLabel = copy.pages.home.copied,
  failedLabel = copy.pages.home.copyFailed,
  className,
}: CopyButtonProps) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<number | null>(null)

  useEffect(() => {
    return () => {
      if (timer.current !== null) window.clearTimeout(timer.current)
    }
  }, [])

  async function handleCopy() {
    const ok = await copyText(text)
    setState(ok ? 'copied' : 'failed')
    if (timer.current !== null) window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setState('idle'), 1600)
  }

  return (
    <button
      type="button"
      onClick={() => void handleCopy()}
      className={cn(
        'inline-flex h-7 shrink-0 items-center rounded-full px-2.5 text-meta text-ink-2 transition-colors',
        'hover:bg-brand/[0.06] hover:text-ink',
        state === 'failed' && 'text-danger',
        state === 'copied' && 'text-brand-ink',
        className,
      )}
    >
      {state === 'copied' ? copiedLabel : state === 'failed' ? failedLabel : label}
    </button>
  )
}
