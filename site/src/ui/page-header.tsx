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
        <h1 className="text-[24px] font-semibold leading-tight tracking-tight text-ink">{title}</h1>
        {lead ? <p className="mt-2 max-w-2xl text-[13.5px] leading-6 text-muted">{lead}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </div>
  )
}
