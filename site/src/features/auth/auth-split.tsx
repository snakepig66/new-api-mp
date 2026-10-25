import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { Logo } from '@/ui'

export interface AuthPoint {
  title: string
  body: string
}

export interface AuthSplitProps {
  headline: string
  body: string
  points: readonly AuthPoint[]
  children: ReactNode
}

/**
 * Standalone auth frame (no AppShell): a deep-blue promise panel on the left
 * and a focused white form column on the right. Below lg the panel collapses
 * to a short top banner and the form owns the rest of the viewport.
 */
export function AuthSplit({ headline, body, points, children }: AuthSplitProps) {
  return (
    <div className="min-h-dvh bg-bg lg:grid lg:grid-cols-[45%_55%]">
      <div className="relative overflow-hidden bg-[linear-gradient(160deg,#1c2b8f_0%,#4d6bfe_100%)] px-6 py-8 text-white sm:px-10 lg:flex lg:flex-col lg:justify-between lg:px-16 lg:py-16">
        <div>
          <p className="text-[14px] font-medium tracking-tight text-white/80">{copy.brand.name}</p>
          <h1 className="mt-3 max-w-[460px] text-[24px] font-semibold leading-[1.2] tracking-[-0.02em] sm:text-[28px] lg:mt-10 lg:text-[32px]">
            {headline}
          </h1>
          <p className="mt-3 hidden max-w-[420px] text-[15px] leading-7 text-white/70 lg:mt-5 lg:block">
            {body}
          </p>

          <ul className="mt-8 hidden space-y-5 lg:block">
            {points.map((point) => (
              <li key={point.title} className="flex items-start gap-3">
                <span className="mt-2 h-2 w-2 shrink-0 rounded-full bg-white/80" aria-hidden="true" />
                <div>
                  <p className="text-[15px] font-medium text-white">{point.title}</p>
                  <p className="mt-0.5 text-[14px] leading-6 text-white/70">{point.body}</p>
                </div>
              </li>
            ))}
          </ul>
        </div>

        <p className="hidden text-[13px] text-white/50 lg:block">按量结算，失败调用不收费。</p>
      </div>

      <div className="flex items-center justify-center px-5 py-12 sm:px-8 lg:py-16">
        <div className="w-full max-w-[380px]">
          <Logo markSize={22} />
          <div className="mt-8">{children}</div>
        </div>
      </div>
    </div>
  )
}
