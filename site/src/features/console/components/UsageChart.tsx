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
    `<div>${copy.pages.console.chart.saved} ${formatYuan(item.saved_micro)}</div>`,
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
          axisPointer: { type: 'shadow' },
          backgroundColor: '#ffffff',
          borderColor: '#e1e7e5',
          borderWidth: 1,
          padding: [8, 10],
          textStyle: { color: '#10201d', fontSize: 12 },
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
          axisLine: { lineStyle: { color: '#e1e7e5' } },
          axisLabel: { color: '#61716d', fontSize: 11 },
        },
        yAxis: {
          type: 'value',
          minInterval: 1,
          axisLabel: { color: '#61716d', fontSize: 11 },
          splitLine: { lineStyle: { color: '#f4f7f6' } },
        },
        series: [
          {
            name: copy.pages.console.chart.requests,
            type: 'bar',
            data: items.map((item) => item.requests),
            barMaxWidth: 26,
            itemStyle: { color: '#0e9f8e', borderRadius: [2, 2, 0, 0] },
            emphasis: { itemStyle: { color: '#087266' } },
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
