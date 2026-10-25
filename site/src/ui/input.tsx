import { forwardRef, useState, type InputHTMLAttributes } from 'react'

import { cn } from '@/lib/cn'

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  invalid?: boolean
  mono?: boolean
}

/* 48px field on a soft grey bed; focus is a 1px blue border + 3px blue halo. */
const inputBase = cn(
  'h-12 w-full rounded-md border border-transparent bg-bg-soft px-4 text-[15px] text-ink transition-colors',
  'placeholder:text-ink-3',
  'focus:border-brand focus:bg-bg focus:outline-none focus:ring-[3px] focus:ring-brand-soft',
  'disabled:cursor-not-allowed disabled:bg-bg-soft disabled:text-ink-3',
  'aria-[invalid=true]:border-danger aria-[invalid=true]:focus:ring-danger/10',
)

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { className, invalid = false, mono = false, ...props },
  ref,
) {
  return (
    <input
      ref={ref}
      aria-invalid={invalid || undefined}
      className={cn(inputBase, mono && 'font-mono text-[14px]', className)}
      {...props}
    />
  )
})

export type PasswordInputProps = InputProps

/**
 * Password field with a show/hide affordance. Styling and props mirror Input;
 * the eye control is inline SVG so no icon dependency is added.
 */
export const PasswordInput = forwardRef<HTMLInputElement, PasswordInputProps>(
  function PasswordInput({ className, ...props }, ref) {
    const [visible, setVisible] = useState(false)

    return (
      <span className="relative block">
        <Input
          {...props}
          ref={ref}
          type={visible ? 'text' : 'password'}
          className={cn('pr-11', className)}
        />
        <button
          type="button"
          // TODO: move these two labels into copy.ts when the page layer touches forms.
          aria-label={visible ? '隐藏密码' : '显示密码'}
          aria-pressed={visible}
          onClick={() => setVisible((current) => !current)}
          className="absolute right-2 top-1/2 grid h-8 w-8 -translate-y-1/2 place-items-center rounded-full text-ink-3 transition-colors hover:bg-line/60 hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand"
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
            className="shrink-0"
          >
            <path
              d="M1.5 8S3.9 3.8 8 3.8 14.5 8 14.5 8 12.1 12.2 8 12.2 1.5 8 1.5 8Z"
              stroke="currentColor"
              strokeWidth="1.4"
              strokeLinejoin="round"
            />
            <circle cx="8" cy="8" r="2.1" stroke="currentColor" strokeWidth="1.4" />
            {visible ? (
              <path d="M2.4 2.4l11.2 11.2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            ) : null}
          </svg>
        </button>
      </span>
    )
  },
)
