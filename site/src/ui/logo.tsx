import { cn } from '@/lib/cn'

export interface LogoMarkProps {
  /** Square mark size in px. */
  size?: number
  className?: string
}

/** Brand mark: the jar outline on the brand-blue rounded square. */
export function LogoMark({ size = 24, className }: LogoMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      className={cn('shrink-0', className)}
      aria-hidden="true"
    >
      <rect width="32" height="32" rx="8" fill="#4D6BFE" />
      <rect x="11.5" y="6.5" width="9" height="3" rx="1.5" fill="#fff" />
      <rect
        x="8.75"
        y="11.25"
        width="14.5"
        height="14.5"
        rx="4.5"
        fill="none"
        stroke="#fff"
        strokeWidth="2.5"
      />
      <circle cx="16" cy="18.5" r="2.75" fill="#fff" />
    </svg>
  )
}

export interface LogoProps {
  className?: string
  markSize?: number
  /** Optional quiet suffix rendered after the wordmark. */
  subtitle?: string
}

/** Full wordmark: the jar mark plus "LLMJar" (Jar in the brand colour). */
export function Logo({ className, markSize = 24, subtitle }: LogoProps) {
  return (
    <span className={cn('inline-flex items-center gap-2', className)}>
      <LogoMark size={markSize} />
      <span
        className="font-semibold tracking-[-0.02em] text-ink"
        style={{ fontSize: `${markSize}px`, lineHeight: 1 }}
      >
        LLM<span className="text-brand">Jar</span>
      </span>
      {subtitle ? (
        <span className="text-[13px] leading-5 text-ink-3">{subtitle}</span>
      ) : null}
    </span>
  )
}
