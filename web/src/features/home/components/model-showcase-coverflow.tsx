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
import { Link } from '@tanstack/react-router'
import {
  ChevronLeft,
  ChevronRight,
  Copy,
  Check,
  ArrowUpRight,
  Sparkles,
} from 'lucide-react'
import { useState, useRef, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

interface ModelCardData {
  id: string
  name: string
  provider: string
  brandColor: string
  iconName: string
  tags: string[]
  context: string
  latency: string
  desc: string
}

const SHOWCASE_MODELS: ModelCardData[] = [
  {
    id: 'deepseek-chat',
    name: 'DeepSeek-V3 / R1',
    provider: 'DeepSeek',
    brandColor: '#5786FE',
    iconName: 'DeepSeek',
    tags: ['推理先锋', '开源强推'],
    context: '64K / 128K',
    latency: '< 0.8s',
    desc: '全新 MoE 架构与强化学习推理模型，代码生成与复杂数学解题能力行业拔尖。',
  },
  {
    id: 'claude-3-5-sonnet-20241022',
    name: 'Claude 3.5 Sonnet',
    provider: 'Anthropic',
    brandColor: '#D97757',
    iconName: 'Claude',
    tags: ['架构巅峰', '编程标杆'],
    context: '200K',
    latency: '< 1.1s',
    desc: '最受资深工程师推崇的智能体底座，具备卓越的上下文连贯性与代码重构能力。',
  },
  {
    id: 'gpt-4o',
    name: 'GPT-4o',
    provider: 'OpenAI',
    brandColor: '#10A37F',
    iconName: 'OpenAI',
    tags: ['全模态', '高并发路由'],
    context: '128K',
    latency: '< 0.9s',
    desc: 'OpenAI 旗舰全模态引擎，超低首字延迟，深度支持函数调用与视觉图表解析。',
  },
  {
    id: 'gemini-2.0-flash',
    name: 'Gemini 2.0 Flash',
    provider: 'Google',
    brandColor: '#8E75B2',
    iconName: 'Gemini',
    tags: ['百万窗口', '极致吞吐'],
    context: '1M Tokens',
    latency: '< 0.5s',
    desc: '原生支持超长文档解析与整库代码审阅，闪电般的吞吐速率与极高性价比。',
  },
  {
    id: 'qwen-2.5-72b-instruct',
    name: 'Qwen 2.5 72B',
    provider: 'Alibaba',
    brandColor: '#6950EF',
    iconName: 'Qwen',
    tags: ['中文旗舰', '指令精准'],
    context: '128K',
    latency: '< 1.0s',
    desc: '千问开源旗舰大模型，深谙中英双语文化背景与高难度结构化数据提炼。',
  },
  {
    id: 'llama-3.3-70b-instruct',
    name: 'Llama 3.3 70B',
    provider: 'Meta',
    brandColor: '#0467DF',
    iconName: 'Meta',
    tags: ['开放生态', '稳健泛化'],
    context: '128K',
    latency: '< 0.9s',
    desc: '开源社区最成熟的通用大模型，推理稳健，适配各类复杂业务微调与下游链路。',
  },
]

export function ModelShowcaseCoverflow({ className }: { className?: string }) {
  const { t } = useTranslation()
  const [activeIndex, setActiveIndex] = useState(0)
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const dragRef = useRef<{ startX: number; active: boolean }>({
    startX: 0,
    active: false,
  })

  const count = SHOWCASE_MODELS.length

  const handlePrev = useCallback(() => {
    setActiveIndex((prev) => (prev - 1 + count) % count)
  }, [count])

  const handleNext = useCallback(() => {
    setActiveIndex((prev) => (prev + 1) % count)
  }, [count])

  // Keyboard navigation when stage is focused
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (document.activeElement?.tagName === 'INPUT' || document.activeElement?.tagName === 'TEXTAREA') {
        return
      }
      if (e.key === 'ArrowLeft') {
        handlePrev()
      } else if (e.key === 'ArrowRight') {
        handleNext()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [handleNext, handlePrev])

  const copyToClipboard = (id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    navigator.clipboard.writeText(id)
    setCopiedId(id)
    setTimeout(() => {
      setCopiedId((current) => (current === id ? null : current))
    }, 1800)
  }

  // Pointer drag gestures
  const onPointerDown = (e: React.PointerEvent) => {
    dragRef.current = { startX: e.clientX, active: true }
  }

  const onPointerUp = (e: React.PointerEvent) => {
    if (!dragRef.current.active) return
    const deltaX = e.clientX - dragRef.current.startX
    dragRef.current.active = false
    if (Math.abs(deltaX) > 40) {
      if (deltaX > 0) {
        handlePrev()
      } else {
        handleNext()
      }
    }
  }

  return (
    <section
      className={cn(
        'relative mx-auto w-full max-w-6xl px-4 py-16 sm:px-6 lg:py-24',
        className
      )}
    >
      {/* Section Header */}
      <div className='flex flex-col items-center text-center'>
        <div className='mb-3 inline-flex items-center gap-2 rounded-full border border-sky-400/25 bg-sky-500/10 px-3.5 py-1 text-xs font-medium text-sky-400 backdrop-blur-md'>
          <Sparkles className='size-3.5 text-sky-300 animate-pulse' />
          <span>{t('Matrix of Industry-Leading Models')}</span>
          <span className='rounded bg-sky-400/20 px-1.5 py-0.2 text-[10px] text-sky-200'>
            0{activeIndex + 1} / 0{count}
          </span>
        </div>

        <h2 className='text-2xl font-bold tracking-tight sm:text-3xl lg:text-4xl'>
          <span>{t('High-Speed Direct Gateway')}</span>{' '}
          <span className='bg-gradient-to-r from-sky-400 via-indigo-400 to-cyan-300 bg-clip-text text-transparent'>
            {t('Native Protocol Switch')}
          </span>
        </h2>
        <p className='text-muted-foreground mt-3 max-w-2xl text-sm sm:text-base'>
          {t(
            'Compatible with standard OpenAI, Anthropic, and Gemini SDKs. Single token routing, ultra-low TTFT, and transparent real-time balance tracking.'
          )}
        </p>

        {/* Model Tabs Navigation */}
        <div className='mt-8 flex max-w-full flex-wrap items-center justify-center gap-1.5 sm:gap-2'>
          {SHOWCASE_MODELS.map((m, idx) => {
            const isActive = idx === activeIndex
            return (
              <button
                key={m.id}
                type='button'
                onClick={() => setActiveIndex(idx)}
                className={cn(
                  'flex items-center gap-2 rounded-full border px-3.5 py-1.5 text-xs font-medium transition-all duration-300',
                  isActive
                    ? 'border-sky-400/50 bg-sky-500/15 text-sky-300 shadow-[0_0_15px_rgba(56,189,248,0.2)] scale-105'
                    : 'border-border/40 bg-muted/20 text-muted-foreground hover:border-sky-500/30 hover:text-foreground'
                )}
              >
                <span
                  className='size-2 rounded-full'
                  style={{ backgroundColor: m.brandColor }}
                />
                <span>{m.provider}</span>
              </button>
            )
          })}
        </div>
      </div>

      {/* 3D CoverFlow Stage */}
      <div
        ref={containerRef}
        onPointerDown={onPointerDown}
        onPointerUp={onPointerUp}
        className='relative mt-12 flex h-[410px] w-full select-none items-center justify-center overflow-visible [perspective:1100px]'
      >
        {SHOWCASE_MODELS.map((item, idx) => {
          let diff = idx - activeIndex
          if (diff > count / 2) diff -= count
          if (diff < -count / 2) diff += count

          const isCenter = diff === 0
          const absDiff = Math.abs(diff)
          const isVisible = absDiff <= 2.2

          // 3D positioning
          const xOffset = diff * 290
          const zOffset = -absDiff * 160
          const rotateY = -diff * 20
          const scale = Math.max(0.7, 1 - absDiff * 0.14)
          const opacity = isCenter ? 1 : Math.max(0.15, 0.75 - absDiff * 0.28)
          const zIndex = 50 - Math.round(absDiff * 10)

          return (
            <div
              key={item.id}
              onClick={() => {
                if (!isCenter) setActiveIndex(idx)
              }}
              style={{
                transform: `translate3d(${xOffset}px, 0, ${zOffset}px) rotateY(${rotateY}deg) scale(${scale})`,
                opacity: isVisible ? opacity : 0,
                pointerEvents: isVisible ? 'auto' : 'none',
                zIndex,
                transition: 'transform 0.45s cubic-bezier(0.22, 1, 0.36, 1), opacity 0.45s ease',
              }}
              className={cn(
                'whale-spotlight-card absolute w-[320px] sm:w-[350px] rounded-2xl border p-6 backdrop-blur-xl transition-shadow',
                isCenter
                  ? 'border-sky-400/40 bg-card/90 shadow-[0_16px_40px_rgba(2,5,16,0.5),0_0_24px_rgba(56,189,248,0.15)] ring-1 ring-sky-400/30'
                  : 'border-border/50 bg-card/60 shadow-lg cursor-pointer hover:border-sky-400/30 hover:opacity-90'
              )}
            >
              {/* Card Top: Brand and Tags */}
              <div className='flex items-center justify-between'>
                <div className='flex items-center gap-3'>
                  <div
                    className='flex size-11 items-center justify-center rounded-xl p-2 shadow-xs ring-1 ring-white/10'
                    style={{
                      background: `radial-gradient(circle, ${item.brandColor}25 0%, ${item.brandColor}05 100%)`,
                    }}
                  >
                    {getLobeIcon(item.iconName, 26)}
                  </div>
                  <div>
                    <h3 className='text-base font-semibold text-foreground tracking-tight'>
                      {item.name}
                    </h3>
                    <p className='text-xs text-muted-foreground font-mono'>
                      {item.provider}
                    </p>
                  </div>
                </div>

                <span
                  className='rounded-full px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider'
                  style={{
                    backgroundColor: `${item.brandColor}20`,
                    color: item.brandColor,
                  }}
                >
                  {item.tags[0]}
                </span>
              </div>

              {/* Description */}
              <p className='mt-4 min-h-[44px] text-xs leading-relaxed text-muted-foreground line-clamp-2'>
                {item.desc}
              </p>

              {/* Metrics Grid */}
              <div className='mt-5 grid grid-cols-2 gap-2.5 rounded-xl border border-border/40 bg-muted/20 p-3 text-xs'>
                <div>
                  <span className='text-[10px] text-muted-foreground block font-mono uppercase tracking-wider'>
                    {t('Context Window')}
                  </span>
                  <span className='mt-0.5 font-medium text-foreground font-mono'>
                    {item.context}
                  </span>
                </div>
                <div>
                  <span className='text-[10px] text-muted-foreground block font-mono uppercase tracking-wider'>
                    {t('Latency')}
                  </span>
                  <span className='mt-0.5 font-medium text-emerald-500 dark:text-emerald-400 font-mono'>
                    {item.latency}
                  </span>
                </div>
              </div>

              {/* Action Buttons */}
              <div className='mt-5 flex items-center justify-between gap-2 pt-1 border-t border-border/30'>
                <button
                  type='button'
                  onClick={(e) => copyToClipboard(item.id, e)}
                  className='inline-flex items-center gap-1.5 rounded-lg border border-border/60 bg-muted/30 px-3 py-1.5 text-xs font-mono font-medium text-foreground/80 hover:border-sky-400/40 hover:bg-sky-500/10 hover:text-sky-300 transition-colors'
                  title={t('Copy model ID')}
                >
                  {copiedId === item.id ? (
                    <>
                      <Check className='size-3.5 text-emerald-400' />
                      <span className='text-emerald-400 font-sans'>{t('Copied')}</span>
                    </>
                  ) : (
                    <>
                      <Copy className='size-3.5 text-muted-foreground' />
                      <span className='truncate max-w-[140px]'>{item.id}</span>
                    </>
                  )}
                </button>

                <Link
                  to='/pricing'
                  className='inline-flex items-center gap-1 text-xs font-medium text-sky-400 hover:text-sky-300 transition-colors'
                >
                  <span>{t('Pricing')}</span>
                  <ArrowUpRight className='size-3.5' />
                </Link>
              </div>
            </div>
          )
        })}

        {/* Stage Left/Right Buttons */}
        <button
          type='button'
          onClick={handlePrev}
          aria-label='Previous model'
          className='group absolute -left-2 sm:left-4 z-60 flex size-10 items-center justify-center rounded-full border border-border/50 bg-background/80 shadow-md backdrop-blur-md transition-all hover:scale-110 hover:border-sky-400/50 hover:bg-sky-500/10'
        >
          <ChevronLeft className='size-5 text-muted-foreground group-hover:text-sky-400 transition-colors' />
        </button>

        <button
          type='button'
          onClick={handleNext}
          aria-label='Next model'
          className='group absolute -right-2 sm:right-4 z-60 flex size-10 items-center justify-center rounded-full border border-border/50 bg-background/80 shadow-md backdrop-blur-md transition-all hover:scale-110 hover:border-sky-400/50 hover:bg-sky-500/10'
        >
          <ChevronRight className='size-5 text-muted-foreground group-hover:text-sky-400 transition-colors' />
        </button>
      </div>
    </section>
  )
}
