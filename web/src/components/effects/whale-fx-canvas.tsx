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
import { useEffect, useRef } from 'react'

import { cn } from '@/lib/utils'

interface Star {
  x: number // 0..1 normalized
  y: number // 0..1 normalized
  size: number
  twinkleSpeed: number
  twinklePhase: number
}

interface TrailParticle {
  x: number
  y: number
  vx: number
  vy: number
  alpha: number
  size: number
}

interface Ripple {
  x: number
  y: number
  radius: number
  alpha: number
  maxRadius: number
}

export interface WhaleFxCanvasProps {
  /**
   * 'full' renders ambient starfield, cursor motes, and click ripples (ideal for Landing/Auth pages).
   * 'subtle' omits background stars and only renders cursor trail motes and ripples (ideal for global layout).
   */
  variant?: 'full' | 'subtle'
  className?: string
  starCount?: number
  accentColor?: string // RGB comma separated e.g. '125, 211, 252'
  fixed?: boolean
}

export function WhaleFxCanvas({
  variant = 'full',
  className,
  starCount = 200,
  accentColor = '125, 211, 252',
  fixed = true,
}: WhaleFxCanvasProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const ctx = canvas.getContext('2d', { alpha: true })
    if (!ctx) return

    const reducedMotionQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
    let isReducedMotion = reducedMotionQuery.matches

    const onReducedMotionChange = (e: MediaQueryListEvent) => {
      isReducedMotion = e.matches
    }
    reducedMotionQuery.addEventListener('change', onReducedMotionChange)

    let width = 0
    let height = 0
    let dpr = 1
    let animId = 0
    let lastTime = performance.now()

    // 1. Initialize Stars for 'full' variant
    const stars: Star[] = []
    if (variant === 'full') {
      const count = window.innerWidth < 768 ? Math.round(starCount * 0.5) : starCount
      for (let i = 0; i < count; i++) {
        stars.push({
          x: Math.random(),
          y: Math.random(),
          size: 0.8 + Math.random() * 1.4,
          twinkleSpeed: 0.6 + Math.random() * 1.5,
          twinklePhase: Math.random() * Math.PI * 2,
        })
      }
    }

    // 2. Interactive Particles & Ripples
    const trails: TrailParticle[] = []
    const ripples: Ripple[] = []
    let lastMoveX = -9999
    let lastMoveY = -9999

    const handlePointerMove = (e: PointerEvent) => {
      if (isReducedMotion) return
      const rect = canvas.getBoundingClientRect()
      const x = e.clientX - rect.left
      const y = e.clientY - rect.top

      // Distance check to throttle particle generation
      const dx = x - lastMoveX
      const dy = y - lastMoveY
      if (Math.hypot(dx, dy) > 16) {
        lastMoveX = x
        lastMoveY = y
        if (trails.length < 24) {
          trails.push({
            x,
            y,
            vx: (Math.random() - 0.5) * 0.8,
            vy: (Math.random() - 0.5) * 0.8 - 0.2, // subtle upward drift
            alpha: 0.65,
            size: 1.2 + Math.random() * 1.6,
          })
        }
      }
    }

    const handlePointerDown = (e: PointerEvent) => {
      if (isReducedMotion) return
      // Only trigger ripple if clicking inside or near canvas
      const rect = canvas.getBoundingClientRect()
      const x = e.clientX - rect.left
      const y = e.clientY - rect.top
      if (x >= 0 && x <= rect.width && y >= 0 && y <= rect.height) {
        if (ripples.length < 8) {
          ripples.push(
            { x, y, radius: 4, alpha: 0.45, maxRadius: 85 },
            { x, y, radius: 2, alpha: 0.25, maxRadius: 130 }
          )
        }
      }
    }

    const handleResize = () => {
      if (!canvas) return
      const rect = canvas.getBoundingClientRect()
      width = rect.width
      height = rect.height
      dpr = Math.min(window.devicePixelRatio || 1, 2)
      canvas.width = Math.round(width * dpr)
      canvas.height = Math.round(height * dpr)
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)

      if (isReducedMotion && variant === 'full') {
        // Draw static stars once for reduced-motion
        ctx.clearRect(0, 0, width, height)
        for (const s of stars) {
          ctx.fillStyle = `rgba(${accentColor}, 0.35)`
          ctx.fillRect(s.x * width, s.y * height, s.size, s.size)
        }
      }
    }

    window.addEventListener('resize', handleResize)
    window.addEventListener('pointermove', handlePointerMove, { passive: true })
    window.addEventListener('pointerdown', handlePointerDown, { passive: true })

    handleResize()

    // 3. Animation Loop
    const render = (time: number) => {
      animId = requestAnimationFrame(render)

      if (document.hidden || isReducedMotion) {
        return
      }

      const dt = Math.min((time - lastTime) / 1000, 0.05)
      lastTime = time

      ctx.clearRect(0, 0, width, height)

      // A. Draw Stars
      if (variant === 'full') {
        for (const s of stars) {
          const t = Math.sin(time * 0.001 * s.twinkleSpeed + s.twinklePhase)
          const alpha = 0.2 + 0.5 * (0.5 + 0.5 * t)
          ctx.fillStyle = `rgba(226, 238, 255, ${alpha.toFixed(2)})`
          ctx.fillRect(s.x * width, s.y * height, s.size, s.size)
        }
      }

      // B. Draw Pointer Trail Motes
      for (let i = trails.length - 1; i >= 0; i--) {
        const p = trails[i]
        p.x += p.vx
        p.y += p.vy
        p.alpha *= 0.94
        if (p.alpha < 0.02) {
          trails.splice(i, 1)
          continue
        }
        ctx.fillStyle = `rgba(${accentColor}, ${p.alpha.toFixed(3)})`
        ctx.beginPath()
        ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2)
        ctx.fill()
      }

      // C. Draw Expanding Ripples
      for (let i = ripples.length - 1; i >= 0; i--) {
        const r = ripples[i]
        r.radius += dt * 75
        r.alpha *= 0.95
        if (r.alpha < 0.015 || r.radius > r.maxRadius) {
          ripples.splice(i, 1)
          continue
        }
        ctx.strokeStyle = `rgba(${accentColor}, ${r.alpha.toFixed(3)})`
        ctx.lineWidth = 1.2
        ctx.beginPath()
        ctx.arc(r.x, r.y, r.radius, 0, Math.PI * 2)
        ctx.stroke()
      }
    }

    if (!isReducedMotion) {
      animId = requestAnimationFrame(render)
    }

    return () => {
      cancelAnimationFrame(animId)
      window.removeEventListener('resize', handleResize)
      window.removeEventListener('pointermove', handlePointerMove)
      window.removeEventListener('pointerdown', handlePointerDown)
      reducedMotionQuery.removeEventListener('change', onReducedMotionChange)
    }
  }, [accentColor, starCount, variant])

  return (
    <canvas
      ref={canvasRef}
      aria-hidden='true'
      className={cn(
        'pointer-events-none z-0',
        fixed ? 'fixed inset-0 h-screen w-screen' : 'absolute inset-0 h-full w-full',
        className
      )}
    />
  )
}
