import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it } from 'vitest'

import {
  ChannelModelsEditor,
  derivePrices,
  parseMultiplierText,
  selectableUpstream,
} from '@/features/seller/ChannelModelsEditor'
import type { PoolModelOption, SellerModelConfig, UpstreamModelOption } from '@/features/seller/api'

const price = {
  input_micro_per_mtok: 1_000_000,
  output_micro_per_mtok: 2_000_000,
  cache_read_micro_per_mtok: 100_000,
  cache_write_micro_per_mtok: 0,
}

const pool: PoolModelOption[] = [
  { model: 'deepseek-v4-flash', vendor: 'deepseek', price },
  { model: 'glm-5', vendor: 'zhipu', price },
]

const upstream: UpstreamModelOption[] = [
  { upstream: 'deepseek-v4-flash-20261001', suggested_model: 'deepseek-v4-flash', priced: true },
  { upstream: 'mystery-model', suggested_model: null, priced: false },
]

function Harness({ initial = [] as SellerModelConfig[], excluded = [] as string[] }) {
  const [value, setValue] = useState<SellerModelConfig[]>(initial)
  return (
    <>
      <ChannelModelsEditor
        upstream={upstream}
        pool={pool}
        value={value}
        onChange={setValue}
        excluded={excluded}
      />
      <output data-testid="value">{JSON.stringify(value)}</output>
    </>
  )
}

function current(): SellerModelConfig[] {
  return JSON.parse(screen.getByTestId('value').textContent ?? '[]') as SellerModelConfig[]
}

afterEach(cleanup)

describe('ChannelModelsEditor helpers', () => {
  it('derives the four unit prices from official × coefficient × multiplier', () => {
    expect(derivePrices(price, 500)).toEqual({
      input_micro_per_mtok: 500_000,
      output_micro_per_mtok: 1_000_000,
      cache_read_micro_per_mtok: 50_000,
      cache_write_micro_per_mtok: 0,
    })
    expect(derivePrices(price, 1000, 800).input_micro_per_mtok).toBe(800_000)
  })

  it('parses multipliers between 0.001 and 1', () => {
    expect(parseMultiplierText('0.85')).toBe(850)
    expect(parseMultiplierText('0.001')).toBe(1)
    expect(parseMultiplierText('0')).toBeNull()
    expect(parseMultiplierText('1.2')).toBeNull()
    expect(parseMultiplierText('abc')).toBeNull()
  })

  it('only offers matched, priced, not-yet-listed upstream models', () => {
    expect(selectableUpstream(upstream, {}, pool)).toEqual([
      { upstream: 'deepseek-v4-flash-20261001', model: 'deepseek-v4-flash' },
    ])
    expect(selectableUpstream(upstream, {}, pool, ['deepseek-v4-flash'])).toEqual([])
    expect(selectableUpstream(upstream, { 'mystery-model': 'glm-5' }, pool)).toHaveLength(2)
  })
})

describe('ChannelModelsEditor', () => {
  it('ticks an auto-matched upstream model and keeps its upstream alias', () => {
    render(<Harness />)
    fireEvent.click(screen.getByLabelText('deepseek-v4-flash-20261001'))
    expect(current()).toEqual([
      {
        model: 'deepseek-v4-flash',
        upstream_model: 'deepseek-v4-flash-20261001',
        multiplier_milli: 1000,
      },
    ])
  })

  it('disables an upstream model the platform has no price for', () => {
    render(<Harness />)
    expect((screen.getByLabelText('mystery-model') as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByText('平台暂无此模型价格')).toBeTruthy()
  })

  it('select all adds every selectable model and clear empties it', () => {
    render(<Harness />)
    fireEvent.click(screen.getByRole('button', { name: '全选' }))
    expect(current().map((c) => c.model)).toEqual(['deepseek-v4-flash'])
    fireEvent.click(screen.getByRole('button', { name: '清空' }))
    expect(current()).toEqual([])
  })

  it('updates the multiplier and the derived prices', () => {
    render(
      <Harness
        initial={[{ model: 'glm-5', upstream_model: 'glm-5', multiplier_milli: 1000 }]}
      />,
    )
    fireEvent.change(screen.getByLabelText('glm-5 倍率'), { target: { value: '0.5' } })
    expect(current()[0].multiplier_milli).toBe(500)
    expect(screen.getByTestId('derived-glm-5').textContent).toContain('0.50')
  })

  it('removes a selected model', () => {
    render(
      <Harness
        initial={[{ model: 'glm-5', upstream_model: 'glm-5', multiplier_milli: 1000 }]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '移除' }))
    expect(current()).toEqual([])
  })
})
