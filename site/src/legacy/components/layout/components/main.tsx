import type React from 'react'

import { cn } from '@/legacy/lib/utils'

type MainProps = React.HTMLAttributes<HTMLElement> & {
  fluid?: boolean
}

export function Main({ className, fluid: _fluid = true, ...props }: MainProps) {
  return (
    <main
      className={cn('flex min-h-0 flex-1 flex-col overflow-hidden', className)}
      {...props}
    />
  )
}
