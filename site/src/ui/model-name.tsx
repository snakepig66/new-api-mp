import type { HTMLAttributes } from 'react'

import { modelLogo } from '@/features/market/model-logo'
import { cn } from '@/lib/cn'

export interface ModelNameProps extends HTMLAttributes<HTMLSpanElement> {
  model: string
  size?: 'sm' | 'md'
  className?: string
}

/**
 * Renders a model name optionally prepended by its official vendor logo.
 *
 * md icon size is 20px, sm is 16px. If no vendor logo matches, renders only the text.
 * The model name is kept as an unbroken text node so testing-library queries continue to match.
 */
export function ModelName({ model, size = 'md', className, ...props }: ModelNameProps) {
  const logo = modelLogo(model)
  if (!logo) {
    return (
      <span className={className} {...props}>
        {model}
      </span>
    )
  }

  const iconSize = size === 'sm' ? 16 : 20
  const iconClass = size === 'sm' ? 'h-4 w-4' : 'h-5 w-5'

  return (
    <span
      className={cn('inline-flex items-center gap-1.5 align-middle', className)}
      {...props}
    >
      <img
        src={logo}
        alt=""
        aria-hidden="true"
        width={iconSize}
        height={iconSize}
        className={cn(iconClass, 'shrink-0 rounded-xs object-contain')}
      />
      <span>{model}</span>
    </span>
  )
}
