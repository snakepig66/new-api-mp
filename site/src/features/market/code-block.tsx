import { useEffect, useRef, useState } from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

import { copyText } from './clipboard'

export interface CodeTabItem {
  key: string
  label: string
  code: string
}

export interface CodeBlockProps {
  /** Single-language body; ignored when `tabs` is set. */
  code?: string
  /** Label for the single-language tab, e.g. "curl". */
  label?: string
  /** Optional internal language switcher. */
  tabs?: CodeTabItem[]
  className?: string
}

/**
 * Dark code surface with a language tab row and one copy control.
 * `#0d1117` matches the GitHub dark canvas; the mono face appears here only.
 */
export function CodeBlock({ code = '', label, tabs, className }: CodeBlockProps) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const [activeKey, setActiveKey] = useState(tabs?.[0]?.key ?? '')
  const timer = useRef<number | null>(null)

  const activeTab = tabs?.find((tab) => tab.key === activeKey) ?? tabs?.[0]
  const body = activeTab?.code ?? code
  const activeLabel = activeTab?.label ?? label ?? ''

  useEffect(() => {
    return () => {
      if (timer.current !== null) window.clearTimeout(timer.current)
    }
  }, [])

  useEffect(() => {
    if (tabs && tabs.length > 0 && !tabs.some((tab) => tab.key === activeKey)) {
      setActiveKey(tabs[0].key)
    }
  }, [tabs, activeKey])

  async function handleCopy() {
    const ok = await copyText(body)
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
    <div className={cn('overflow-hidden rounded-md bg-[#0d1117]', className)}>
      <div className="flex h-10 items-center justify-between gap-3 border-b border-white/10 px-3">
        {tabs && tabs.length > 0 ? (
          <div
            className="flex items-center gap-1"
            role="tablist"
            aria-label={copy.pages.modelDetail.code.aria}
          >
            {tabs.map((tab) => (
              <button
                key={tab.key}
                type="button"
                role="tab"
                aria-selected={activeKey === tab.key}
                onClick={() => setActiveKey(tab.key)}
                className={cn(
                  'rounded-sm px-2.5 py-1 text-[13px] transition-colors',
                  activeKey === tab.key
                    ? 'bg-white/10 text-white'
                    : 'text-white/55 hover:text-white/85',
                )}
              >
                {tab.label}
              </button>
            ))}
          </div>
        ) : (
          <span className="px-1 text-[13px] text-white/55">{activeLabel}</span>
        )}

        <button
          type="button"
          onClick={() => void handleCopy()}
          className={cn(
            'shrink-0 rounded-sm px-2 py-1 text-[13px] transition-colors',
            state === 'failed'
              ? 'text-[#ff7b72]'
              : 'text-white/55 hover:bg-white/10 hover:text-white',
          )}
        >
          {buttonLabel}
        </button>
      </div>
      <pre className="overflow-x-auto p-4 font-mono text-[13px] leading-6 text-[#e6edf3]">
        <code>{body}</code>
      </pre>
      <span className="sr-only" aria-live="polite">
        {state === 'copied' ? copy.pages.modelDetail.code.copied : ''}
      </span>
    </div>
  )
}
