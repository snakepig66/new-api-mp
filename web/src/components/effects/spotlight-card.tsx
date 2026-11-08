/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import * as React from 'react'

import { cn } from '@/lib/utils'

export interface SpotlightCardProps extends React.HTMLAttributes<HTMLDivElement> {
  children?: React.ReactNode
  tilt?: boolean
}

export function SpotlightCard({
  children,
  className,
  tilt = false,
  onPointerMove,
  onPointerLeave,
  ...props
}: SpotlightCardProps) {
  const handlePointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const el = e.currentTarget
    const rect = el.getBoundingClientRect()
    const x = e.clientX - rect.left
    const y = e.clientY - rect.top
    el.style.setProperty('--spotlight-x', `${x}px`)
    el.style.setProperty('--spotlight-y', `${y}px`)

    if (tilt) {
      const rx = (y / rect.height - 0.5) * -4.5
      const ry = (x / rect.width - 0.5) * 5.5
      el.style.transform = `perspective(900px) rotateX(${rx.toFixed(2)}deg) rotateY(${ry.toFixed(2)}deg) translateY(-2px)`
    }
    onPointerMove?.(e)
  }

  const handlePointerLeave = (e: React.PointerEvent<HTMLDivElement>) => {
    const el = e.currentTarget
    if (tilt) {
      el.style.transform = ''
    }
    onPointerLeave?.(e)
  }

  return (
    <div
      className={cn('whale-spotlight-card', className)}
      onPointerMove={handlePointerMove}
      onPointerLeave={handlePointerLeave}
      {...props}
    >
      {children}
    </div>
  )
}
