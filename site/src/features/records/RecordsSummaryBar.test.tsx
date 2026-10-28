import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { RecordsSummaryBar, type RecordsSummaryItem } from './RecordsSummaryBar'

const items: RecordsSummaryItem[] = [
  { label: '请求数', value: '1' },
  { label: '成功率', value: '100.0%', hint: '1 / 1' },
  { label: 'Tokens', value: '22 / 16', hint: '输入 / 输出' },
  { label: '实付', value: '¥0.000312' },
  { label: '节省', value: '¥0.000312' },
  { label: '平均耗时', value: '— / —', hint: '首字 / 总耗时' },
]

describe('RecordsSummaryBar', () => {
  it('fills the grid with six skeleton tiles while loading', () => {
    const { container } = render(<RecordsSummaryBar items={[]} loading />)
    const grid = container.firstElementChild as HTMLElement
    expect(grid.children).toHaveLength(6)
    expect(grid.querySelectorAll('.animate-pulse')).toHaveLength(12)
  })

  // Regression: the skeleton tiles used to share the same React key (all
  // labels empty), so the loaded tiles could not reconcile against them and
  // stale cells stayed in the grid, pushing 请求数/成功率/Tokens into the second
  // row. The grid must always hold exactly one cell per tile.
  it('replaces every skeleton cell when the data arrives', () => {
    const { container, rerender } = render(<RecordsSummaryBar items={[]} loading />)
    const grid = container.firstElementChild as HTMLElement
    rerender(<RecordsSummaryBar items={items} />)
    expect(grid.children).toHaveLength(6)
    expect(grid.querySelectorAll('.animate-pulse')).toHaveLength(0)
    expect(screen.getByText('请求数')).toBeTruthy()
    expect(screen.getByText('平均耗时')).toBeTruthy()
  })
})
