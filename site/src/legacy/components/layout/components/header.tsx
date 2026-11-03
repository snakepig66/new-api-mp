import type React from 'react'

import { cn } from '@/legacy/lib/utils'

type HeaderProps = React.HTMLAttributes<HTMLElement>

export function Header({ className, children, ...props }: HeaderProps) {
  return (
    <header className={cn('w-full shrink-0', className)} {...props}>
      {children}
    </header>
  )
}
