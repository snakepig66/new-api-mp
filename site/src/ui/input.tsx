import { forwardRef, type InputHTMLAttributes } from 'react'

import { cn } from '@/lib/cn'

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  invalid?: boolean
  mono?: boolean
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { className, invalid = false, mono = false, ...props },
  ref,
) {
  return (
    <input
      ref={ref}
      aria-invalid={invalid || undefined}
      className={cn(
        'h-9 w-full rounded-[4px] border border-line-strong bg-surface px-3 text-[13.5px] text-ink transition-colors',
        'placeholder:text-muted/60 hover:border-line-strong',
        'focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand/15',
        'disabled:cursor-not-allowed disabled:bg-wash disabled:text-muted',
        'aria-[invalid=true]:border-danger aria-[invalid=true]:focus:ring-danger/15',
        mono && 'font-numeric',
        className,
      )}
      {...props}
    />
  )
})
