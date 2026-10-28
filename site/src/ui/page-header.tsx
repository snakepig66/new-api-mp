import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

export interface PageHeaderProps {
  title: string
  lead?: string
  actions?: ReactNode
  className?: string
}

export function PageHeader({ title, lead, actions, className }: PageHeaderProps) {
  return (
    <div
      className={cn(
        'flex flex-col gap-4 border-b border-line pb-6 sm:flex-row sm:items-end sm:justify-between',
        className,
      )}
    >
      <div className="min-w-0">
        <h1 className="text-h1 font-semibold leading-tight tracking-tight text-ink [text-shadow:0_1px_0_rgb(255_255_255/0.6)]">
          {title}
        </h1>
        {lead ? <p className="mt-2 max-w-2xl text-body leading-6 text-ink-2">{lead}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </div>
  )
}
