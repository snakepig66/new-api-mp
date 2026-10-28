import { cn } from '@/lib/cn'

import { buttonClasses } from './button'

export interface PaginationProps {
  /** 1-based current page. Out-of-range values are clamped for display. */
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
  /** Pages shown on each side of the current page. Defaults to 1. */
  siblingCount?: number
  /** Smaller controls for table footers. */
  compact?: boolean
  disabled?: boolean
  /** Render the "第 X / Y 页 · 共 N 条" summary. */
  showSummary?: boolean
  ariaLabel?: string
  className?: string
}

type PageItem = number | 'ellipsis'

const labels = {
  nav: '分页',
  previous: '上一页',
  next: '下一页',
  summary: (current: number, count: number, total: number) =>
    `第 ${current} / ${count} 页 · 共 ${total} 条`,
  page: (page: number) => `第 ${page} 页`,
  current: (page: number) => `第 ${page} 页，当前页`,
}

/** First and last pages always show; the middle collapses into ellipses. */
function buildPages(current: number, count: number, siblingCount: number): PageItem[] {
  const visible = siblingCount * 2 + 5
  if (count <= visible) {
    return Array.from({ length: count }, (_, index) => index + 1)
  }

  const left = Math.max(current - siblingCount, 1)
  const right = Math.min(current + siblingCount, count)
  const dotsLeft = left > 2
  const dotsRight = right < count - 2

  if (!dotsLeft && dotsRight) {
    const head = Array.from({ length: 3 + siblingCount * 2 }, (_, index) => index + 1)
    return [...head, 'ellipsis', count]
  }

  if (dotsLeft && !dotsRight) {
    const tailLength = 3 + siblingCount * 2
    const tail = Array.from(
      { length: tailLength },
      (_, index) => count - tailLength + index + 1,
    )
    return [1, 'ellipsis', ...tail]
  }

  const middle = Array.from(
    { length: right - left + 1 },
    (_, index) => left + index,
  )
  return [1, 'ellipsis', ...middle, 'ellipsis', count]
}

function ChevronLeft() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path
        d="M7.5 2.5 4 6l3.5 3.5"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function ChevronRight() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path
        d="M4.5 2.5 8 6l-3.5 3.5"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

export function Pagination({
  page,
  pageSize,
  total,
  onPageChange,
  siblingCount = 1,
  compact = false,
  disabled = false,
  showSummary = false,
  ariaLabel,
  className,
}: PaginationProps) {
  const pageCount = Math.max(1, Math.ceil(total / Math.max(1, pageSize)))
  const current = Math.min(Math.max(1, page), pageCount)
  const locked = disabled
  const size = 'sm'
  const items = buildPages(current, pageCount, Math.max(0, siblingCount))

  function go(next: number) {
    if (locked) return
    const target = Math.min(Math.max(1, next), pageCount)
    if (target !== current) onPageChange(target)
  }

  const controlClass = cn(
    'disabled:pointer-events-none disabled:opacity-40',
    compact ? 'px-2' : '',
  )

  return (
    <nav
      aria-label={ariaLabel ?? labels.nav}
      className={cn('flex flex-wrap items-center gap-2', className)}
    >
      {showSummary ? (
        <span className="mr-auto text-[13px] leading-5 text-ink-3">
          {labels.summary(current, pageCount, total)}
        </span>
      ) : null}

      <div className={cn('flex items-center', compact ? 'gap-1' : 'gap-1.5')}>
        <button
          type="button"
          disabled={locked || current <= 1}
          onClick={() => go(current - 1)}
          aria-label={labels.previous}
          className={cn(buttonClasses('ghost', size, controlClass), compact && 'w-7 px-0')}
        >
          {compact ? <ChevronLeft /> : labels.previous}
        </button>

        {items.map((item, index) =>
          item === 'ellipsis' ? (
            <span
              key={`ellipsis-${index}`}
              aria-hidden="true"
              className={cn(
                'grid place-items-center text-[13px] text-ink-3',
                compact ? 'w-7' : 'w-8',
              )}
            >
              …
            </span>
          ) : (
            <button
              key={item}
              type="button"
              disabled={locked}
              onClick={() => go(item)}
              aria-label={item === current ? labels.current(item) : labels.page(item)}
              aria-current={item === current ? 'page' : undefined}
              className={cn(
                buttonClasses(item === current ? 'secondary' : 'ghost', size, 'px-0 tabular-nums'),
                compact ? 'w-7' : 'w-8',
                item === current && 'bg-white/85 font-medium text-brand-ink',
              )}
            >
              {item}
            </button>
          ),
        )}

        <button
          type="button"
          disabled={locked || current >= pageCount}
          onClick={() => go(current + 1)}
          aria-label={labels.next}
          className={cn(buttonClasses('ghost', size, controlClass), compact && 'w-7 px-0')}
        >
          {compact ? <ChevronRight /> : labels.next}
        </button>
      </div>
    </nav>
  )
}
