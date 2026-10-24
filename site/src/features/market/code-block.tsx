import { useEffect, useRef, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

import { copyText } from './clipboard'

export interface CodeBlockProps {
  code: string
  /** Small label on the header rule, e.g. "curl" or the tab name. */
  label?: string
  className?: string
}

/**
 * Paste-ready code with a copy button. The copy state is announced politely
 * and resets after a moment; no toast, because the button is already in view.
 */
export function CodeBlock({ code, label, className }: CodeBlockProps) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<number | null>(null)

  useEffect(() => {
    return () => {
      if (timer.current !== null) window.clearTimeout(timer.current)
    }
  }, [])

  async function handleCopy() {
    const ok = await copyText(code)
    setState(ok ? 'copied' : 'failed')
    if (timer.current !== null) window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setState('idle'), 1600)
  }

  const buttonLabel =
    state === 'copied'
      ? copy.pages.modelDetail.code.copied
      : state === 'failed'
        ? copy.pages.modelDetail.code.failed
        : copy.pages.modelDetail.code.copy

  return (
    <div className={cn('overflow-hidden rounded-[4px] border border-line', className)}>
      <div className="flex h-8 items-center justify-between gap-3 border-b border-line bg-wash px-3">
        <span className="font-numeric text-[11px] text-muted">{label ?? ''}</span>
        <button
          type="button"
          onClick={() => void handleCopy()}
          className="rounded-[3px] px-1.5 py-0.5 text-[11.5px] text-muted transition-colors hover:bg-surface hover:text-ink"
        >
          {buttonLabel}
        </button>
      </div>
      <pre className="code-block !rounded-none !border-0">
        <code>{code}</code>
      </pre>
      <span className="sr-only" aria-live="polite">
        {state === 'copied' ? copy.pages.modelDetail.code.copied : ''}
      </span>
    </div>
  )
}
