import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { LogoMark } from '@/ui'

export interface AuthSplitProps {
  headline: string
  body: string
  children: ReactNode
}

/**
 * Two-column auth frame: the market's promise on the left, one focused card
 * on the right. The left column is decoration only below lg; the form is
 * always centred on mobile.
 */
export function AuthSplit({ headline, body, children }: AuthSplitProps) {
  return (
    <div className="mx-auto grid min-h-[calc(100dvh-14rem)] max-w-[1180px] gap-12 px-5 py-12 lg:grid-cols-[minmax(0,1fr)_400px] lg:gap-20 lg:py-20">
      <div className="hidden lg:block">
        <LogoMark size={34} className="text-brand-dark" />
        <h1 className="mt-8 max-w-[520px] text-[32px] font-semibold leading-[1.25] tracking-tight text-ink">
          {headline}
        </h1>
        <p className="mt-4 max-w-md text-[13.5px] leading-7 text-muted">{body}</p>
        <dl className="mt-10 max-w-sm border-t border-line text-[12.5px]">
          {copy.pages.home.facts.map((fact) => (
            <div
              key={fact.label}
              className="flex items-baseline justify-between gap-4 border-b border-line/70 py-2.5"
            >
              <dt className="text-muted">{fact.label}</dt>
              <dd className="font-medium text-ink">{fact.value}</dd>
            </div>
          ))}
        </dl>
      </div>

      <div className="w-full max-w-[400px] justify-self-start lg:justify-self-end">{children}</div>
    </div>
  )
}
