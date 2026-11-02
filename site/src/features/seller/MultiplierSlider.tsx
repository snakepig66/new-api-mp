import { useEffect, useRef, useState } from 'react'

import { cn } from '@/lib/cn'
import { Input } from '@/ui/input'
import { formatMultiplierText } from './wizard'

export interface MultiplierSliderProps {
  /** Current multiplier value, either as number (e.g. 0.8) or string ("0.80"). */
  value: number | string
  /** Callback fired when multiplier changes, with numeric value and 2-decimal text. */
  onChange: (value: number, text: string) => void
  /** Minimum multiplier, defaults to 0.01. */
  min?: number
  /** Maximum multiplier, defaults to 1.00. */
  max?: number
  /** Step precision, defaults to 0.01. */
  step?: number
  /** Whether controls are disabled. */
  disabled?: boolean
  /** Whether to show the multiplier capsule. Defaults to true. */
  showDiscount?: boolean
  /** Whether to show the numeric input. Defaults to true. */
  showInput?: boolean
  /** Base price in micro-yuan per 1M tokens. When provided, capsule shows actual price. */
  basePriceMicro?: number
  /** Class name for the container. */
  className?: string
  /** Class name for the range slider. */
  sliderClassName?: string
  /** Class name for the numeric input. */
  inputClassName?: string
  id?: string
  /** ARIA label for screen readers. */
  'aria-label'?: string
}

/**
 * Multiplier slider and input with mouse wheel support and 0.01 precision.
 * Rolling the mouse wheel over the slider or input increases / decreases by 0.01
 * while preventing page scrolling.
 */
export function MultiplierSlider({
  value,
  onChange,
  min = 0.01,
  max = 1.0,
  step = 0.01,
  disabled = false,
  showDiscount = true,
  showInput = true,
  basePriceMicro,
  className,
  sliderClassName,
  inputClassName,
  id,
  'aria-label': ariaLabel = '倍率',
}: MultiplierSliderProps) {
  const containerRef = useRef<HTMLDivElement>(null)

  // Normalize numeric value
  const numValue = typeof value === 'number' ? value : Number(value)
  const currentNum = Number.isFinite(numValue)
    ? Math.round(numValue * 100) / 100
    : min
  const clampedNum = Math.min(max, Math.max(min, currentNum))

  // Text state for the input box
  const [inputText, setInputText] = useState(() =>
    Number.isFinite(numValue) ? numValue.toString() : min.toFixed(2),
  )

  // Synchronize text state when value changes externally
  useEffect(() => {
    if (typeof value === 'number') {
      setInputText(value.toFixed(2))
    } else if (typeof value === 'string') {
      const parsed = Number(value)
      if (Number.isFinite(parsed)) {
        setInputText(value)
      }
    }
  }, [value])

  // Non-passive wheel event listener to handle mouse wheel adjustment & prevent window scroll
  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const handleWheel = (e: WheelEvent) => {
      if (disabled) return
      e.preventDefault()

      // Wheel up: e.deltaY < 0 (increase); Wheel down: e.deltaY > 0 (decrease)
      const direction = e.deltaY < 0 ? 1 : -1
      const next = Math.round((clampedNum + direction * step) * 100) / 100
      const clampedNext = Math.min(max, Math.max(min, next))

      if (clampedNext !== clampedNum) {
        const text = clampedNext.toFixed(2)
        setInputText(text)
        onChange(clampedNext, text)
      }
    }

    el.addEventListener('wheel', handleWheel, { passive: false })
    return () => {
      el.removeEventListener('wheel', handleWheel)
    }
  }, [clampedNum, disabled, max, min, onChange, step])

  const handleSliderChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const next = Math.round(Number(e.target.value) * 100) / 100
    const clampedNext = Math.min(max, Math.max(min, next))
    const text = clampedNext.toFixed(2)
    setInputText(text)
    onChange(clampedNext, text)
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const text = e.target.value
    setInputText(text)
    const parsed = Number(text)
    if (Number.isFinite(parsed) && parsed >= min && parsed <= max) {
      const rounded = Math.round(parsed * 100) / 100
      onChange(rounded, text)
    }
  }

  const handleInputBlur = () => {
    const parsed = Number(inputText)
    if (Number.isFinite(parsed)) {
      const clamped = Math.min(max, Math.max(min, Math.round(parsed * 100) / 100))
      const formatted = clamped.toFixed(2)
      setInputText(formatted)
      onChange(clamped, formatted)
    } else {
      const fallback = clampedNum.toFixed(2)
      setInputText(fallback)
      onChange(clampedNum, fallback)
    }
  }

  return (
    <div
      ref={containerRef}
      className={cn('inline-flex items-center gap-2.5', className)}
      title="滑动或使用鼠标滚轮微调倍率（精度 0.01）"
    >
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={clampedNum}
        disabled={disabled}
        aria-label={`${ariaLabel}滑块`}
        onChange={handleSliderChange}
        className={cn(
          'h-2 w-28 cursor-pointer accent-brand disabled:cursor-not-allowed sm:w-36',
          sliderClassName,
        )}
      />

      {showInput ? (
        <Input
          id={id}
          aria-label={ariaLabel}
          inputMode="decimal"
          disabled={disabled}
          value={inputText}
          onChange={handleInputChange}
          onBlur={handleInputBlur}
          className={cn(
            'h-8 w-20 px-2 text-center font-mono text-meta',
            inputClassName,
          )}
        />
      ) : null}

      {showDiscount ? (
        <span className="shrink-0 rounded bg-brand-soft/80 px-2 py-0.5 text-meta font-medium text-brand-ink">
          {formatMultiplierText(Math.round(clampedNum * 1000), basePriceMicro)}
        </span>
      ) : null}
    </div>
  )
}
