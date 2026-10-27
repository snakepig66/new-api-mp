import { forwardRef, useId, type ReactNode, type SelectHTMLAttributes } from 'react'

import { cn } from '@/lib/cn'

export interface SelectOption {
  value: string
  label: string
  disabled?: boolean
}

export interface SelectProps
  extends Omit<SelectHTMLAttributes<HTMLSelectElement>, 'children'> {
  options: SelectOption[]
  label?: string
  hint?: ReactNode
  error?: string
  /** Rendered as a disabled first option with an empty value. */
  placeholder?: string
  /** Classes for the field wrapper. */
  wrapperClassName?: string
}

/* Same bed, focus ring and disabled treatment as Input. */
const selectBase = cn(
  'h-12 w-full cursor-pointer appearance-none rounded-md border border-transparent bg-bg-soft px-4 pr-10 text-[15px] text-ink transition-colors',
  'focus:border-brand focus:bg-bg focus:outline-none focus:ring-[3px] focus:ring-brand-soft',
  'disabled:cursor-not-allowed disabled:bg-bg-soft disabled:text-ink-3',
  'aria-[invalid=true]:border-danger aria-[invalid=true]:focus:ring-danger/10',
)

/** Native select with the label / hint / error layout used across the console. */
export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  {
    options,
    label,
    hint,
    error,
    placeholder,
    className,
    wrapperClassName,
    id,
    disabled,
    ...props
  },
  ref,
) {
  const generatedId = useId()
  const selectId = id ?? generatedId
  const hintId = `${selectId}-hint`
  const errorId = `${selectId}-error`

  return (
    <div className={cn('min-w-0', wrapperClassName)}>
      {label ? (
        <label
          htmlFor={selectId}
          className="mb-2 block text-[14px] font-medium leading-5 text-ink"
        >
          {label}
        </label>
      ) : null}

      <div className="relative">
        <select
          {...props}
          ref={ref}
          id={selectId}
          disabled={disabled}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorId : hint ? hintId : undefined}
          className={cn(selectBase, className)}
        >
          {placeholder ? (
            <option value="" disabled>
              {placeholder}
            </option>
          ) : null}
          {options.map((option) => (
            <option key={option.value} value={option.value} disabled={option.disabled}>
              {option.label}
            </option>
          ))}
        </select>
        <svg
          width="14"
          height="14"
          viewBox="0 0 14 14"
          fill="none"
          aria-hidden="true"
          className={cn(
            'pointer-events-none absolute right-4 top-1/2 -translate-y-1/2 text-ink-3',
            disabled && 'opacity-50',
          )}
        >
          <path
            d="M3.5 5.25 7 8.75l3.5-3.5"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </div>

      {error ? (
        <p id={errorId} role="alert" className="mt-1 text-[13px] leading-5 text-danger">
          {error}
        </p>
      ) : hint ? (
        <p id={hintId} className="mt-1 text-[13px] leading-5 text-ink-3">
          {hint}
        </p>
      ) : null}
    </div>
  )
})
