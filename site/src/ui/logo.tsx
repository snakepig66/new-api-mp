import { copy } from '@/copy'

export interface LogoMarkProps {
  size?: number
  className?: string
}

/**
 * The token mark: a quote line inside a token square. One shape, used in the
 * header, the footer, empty states and the favicon.
 */
export function LogoMark({ size = 24, className }: LogoMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="none"
      aria-hidden="true"
      className={className}
    >
      <rect x="1" y="1" width="30" height="30" rx="7" stroke="currentColor" strokeWidth="2" />
      <path
        d="M8.5 21.5 13.25 14.5l3.75 3.75L23.5 9.5"
        stroke="currentColor"
        strokeWidth="2.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <circle cx="23.5" cy="9.5" r="1.9" fill="currentColor" />
    </svg>
  )
}

export interface LogoProps {
  className?: string
  markSize?: number
}

export function Logo({ className, markSize = 22 }: LogoProps) {
  return (
    <span className={className}>
      <span className="flex items-center gap-2">
        <LogoMark size={markSize} className="text-brand-dark" />
        <span className="text-[15px] font-semibold tracking-[0.02em] text-ink">
          {copy.brand.name}
        </span>
      </span>
    </span>
  )
}
