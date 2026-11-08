import type { HTMLAttributes } from 'react'

import { modelLogo, withLogoVersion } from '@/features/market/model-logo'
import { vendorLogo } from '@/features/market/vendors'
import { cn } from '@/lib/cn'

export interface ModelNameProps extends HTMLAttributes<HTMLSpanElement> {
  model: string
  vendor?: string
  size?: 'sm' | 'md'
  className?: string
}

/**
 * Renders a model name optionally prepended by its official vendor logo.
 *
 * md icon size is 20px, sm is 18px. If no vendor logo matches, falls back to the
 * passed vendor's logo (if any). If still unmatched, renders only the text.
 * The model name is kept as an unbroken text node so testing-library queries continue to match.
 */
export function ModelName({ model, vendor, size = 'md', className, ...props }: ModelNameProps) {
  const logo = modelLogo(model) || (vendor ? vendorLogo(vendor) : '')
  if (!logo) {
    return (
      <span className={cn('whitespace-nowrap truncate', className)} title={model} {...props}>
        {model}
      </span>
    )
  }

  const iconSize = size === 'sm' ? 18 : 20
  const iconClass = size === 'sm' ? 'h-[18px] w-[18px]' : 'h-5 w-5'

  return (
    <span
      className={cn('inline-flex items-center gap-1.5 align-middle whitespace-nowrap min-w-0', className)}
      title={model}
      {...props}
    >
      <img
        src={withLogoVersion(logo)}
        alt=""
        aria-hidden="true"
        width={iconSize}
        height={iconSize}
        className={cn(iconClass, 'shrink-0 object-contain')}
      />
      <span className="whitespace-nowrap truncate">{model}</span>
    </span>
  )
}
