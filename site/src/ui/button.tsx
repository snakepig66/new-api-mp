import { forwardRef, type ButtonHTMLAttributes } from 'react'

import { cn } from '@/lib/cn'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'link'
export type ButtonSize = 'sm' | 'md' | 'lg'

const variants: Record<ButtonVariant, string> = {
  /* Brand-blue gradient pill with a soft glow: the single loud action on a page. */
  primary: 'btn-primary',
  /* Frosted glass pill. */
  secondary: 'btn-glass text-ink',
  ghost: 'text-ink-2 hover:bg-white/60 hover:text-ink',
  /* Destructive only: red type on glass, never a red block. */
  danger:
    'border border-danger/30 bg-white/60 text-danger backdrop-blur-md hover:bg-danger/5 active:bg-danger/10',
  link: 'h-auto px-0 text-brand underline-offset-4 hover:text-brand-hover hover:underline',
}

const sizes: Record<ButtonSize, string> = {
  sm: 'h-8 gap-1 px-3 text-meta',
  md: 'h-10 gap-1.5 px-5 text-body',
  lg: 'h-12 gap-2 px-6 text-body',
}

/** Classes for links that must look like buttons (TanStack Link, anchors). */
export function buttonClasses(
  variant: ButtonVariant = 'primary',
  size: ButtonSize = 'md',
  className?: string,
): string {
  return cn(
    'inline-flex select-none items-center justify-center whitespace-nowrap rounded-full font-medium transition-[color,background-color,border-color,box-shadow,filter,transform] duration-200',
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
