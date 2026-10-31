// Direct installer paths keep only the bar chart, grid, tooltip and canvas
// renderer in the chunk. The barrel modules re-export every chart, which
// defeats tree-shaking for this small dashboard chart.
import 'echarts/lib/chart/bar'
import 'echarts/lib/component/grid'
import 'echarts/lib/component/tooltip'
import 'echarts/lib/renderer/installCanvasRenderer'
import { init, type EChartsType } from 'echarts/core'
import { useEffect, useRef } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'

import type { UsageDayStat } from '../types'

interface TooltipParam {
  dataIndex: number
}

function tooltipHtml(item: UsageDayStat): string {
  return [
    `<div style="font-weight:600;margin-bottom:4px">${item.date}</div>`,
    `<div>${copy.pages.console.chart.requests} ${item.requests}</div>`,
    `<div>${copy.pages.console.chart.spend} ${formatYuan(item.total_debit_micro)}</div>`,
  ].join('')
}

/** Seven-day request bars, rendered on demand. */
export function UsageChart({ items }: { items: UsageDayStat[] }) {
  const hostRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const host = hostRef.current
    if (!host) return

    let chart: EChartsType | null = null
    try {
      chart = init(host, undefined, { renderer: 'canvas' })
      chart.setOption({
        animationDuration: 240,
        grid: { left: 4, right: 8, top: 18, bottom: 2, containLabel: true },
        tooltip: {
          trigger: 'axis',
          axisPointer: { type: 'shadow', shadowStyle: { color: 'rgba(77,107,254,0.06)' } },
          backgroundColor: '#ffffff',
          borderColor: '#eceef2',
          borderWidth: 1,
          padding: [10, 12],
          extraCssText: 'border-radius:12px;box-shadow:none;',
          textStyle: { color: '#4b5160', fontSize: 12 },
          formatter: (params: unknown) => {
            const list = Array.isArray(params) ? (params as TooltipParam[]) : []
            const index = list[0]?.dataIndex ?? -1
            const item = items[index]
            return item ? tooltipHtml(item) : ''
          },
        },
        xAxis: {
          type: 'category',
          data: items.map((item) => item.date.slice(5)),
          axisTick: { show: false },
          axisLine: { lineStyle: { color: '#eceef2' } },
          axisLabel: { color: '#8a909c', fontSize: 12 },
        },
        yAxis: {
          type: 'value',
          minInterval: 1,
          axisLabel: { color: '#8a909c', fontSize: 12 },
          splitLine: { lineStyle: { color: '#eceef2' } },
        },
        series: [
          {
            name: copy.pages.console.chart.requests,
            type: 'bar',
            data: items.map((item) => item.requests),
            barMaxWidth: 26,
            itemStyle: { color: '#4d6bfe', borderRadius: [4, 4, 0, 0] },
            emphasis: { itemStyle: { color: '#3b58f0' } },
          },
        ],
      })
    } catch {
      // A browser without canvas support simply shows the empty host box;
      // the numeric summary stays available in the recent-requests table.
    }

    let observer: ResizeObserver | null = null
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(() => chart?.resize())
      observer.observe(host)
    }

    return () => {
      observer?.disconnect()
      chart?.dispose()
      chart = null
    }
  }, [items])

  return (
    <div
      ref={hostRef}
      className="h-[220px] w-full"
      role="img"
      aria-label={copy.pages.console.chart.aria}
    />
  )
}
