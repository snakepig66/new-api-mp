import { forwardRef, type ButtonHTMLAttributes } from 'react'

import { cn } from '@/lib/cn'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'link'
export type ButtonSize = 'sm' | 'md' | 'lg'

const variants: Record<ButtonVariant, string> = {
  primary:
    'bg-brand-dark text-white hover:bg-[#065d54] active:bg-[#054b44] shadow-[inset_0_1px_0_rgba(255,255,255,0.12)]',
  secondary:
    'border border-line-strong bg-surface text-ink hover:border-brand/50 hover:bg-wash active:bg-[#edf2f0]',
  ghost: 'text-muted hover:bg-wash hover:text-ink',
  danger: 'bg-danger text-white hover:bg-[#96180f] active:bg-[#7f140d]',
  link: 'h-auto px-0 text-brand-dark underline-offset-4 hover:underline',
}

const sizes: Record<ButtonSize, string> = {
  sm: 'h-8 px-3 text-[12.5px]',
  md: 'h-9 px-4 text-[13.5px]',
  lg: 'h-10 px-5 text-[14px]',
}

/** Classes for links that must look like buttons (TanStack Link, anchors). */
export function buttonClasses(
  variant: ButtonVariant = 'primary',
  size: ButtonSize = 'md',
  className?: string,
): string {
  return cn(
    'inline-flex select-none items-center justify-center gap-1.5 whitespace-nowrap rounded-[4px] font-medium transition-colors',
    variant !== 'link' &&
      'disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50',
    variants[variant],
    variant === 'link' ? '' : sizes[size],
    className,
  )
}

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: ButtonSize
  loading?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'primary', size = 'md', loading = false, className, disabled, children, ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      type={props.type ?? 'button'}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={buttonClasses(variant, size, className)}
      {...props}
    >
      {loading ? (
        <svg
          className="h-3.5 w-3.5 animate-spin"
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
        >
          <circle cx="8" cy="8" r="6" stroke="currentColor" strokeOpacity="0.25" strokeWidth="2" />
          <path d="M14 8a6 6 0 0 0-6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      ) : null}
      {children}
    </button>
  )
})
