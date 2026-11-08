/**
 * 近 30 天收入柱状图。ECharts 按需动态引入，只有概览页会加载这一块。
 * 金额只做展示：柱子使用后端微元数值，提示与坐标轴统一交给 formatYuan。
 */

import { useEffect, useRef } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'

import type { SellerDailyIncome } from './api'

export function IncomeChart({ daily }: { daily: SellerDailyIncome[] }) {
  const container = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    let disposed = false
    let chart: import('echarts/core').EChartsType | null = null
    let observer: ResizeObserver | null = null

    void (async () => {
      const [core, charts, components, renderers] = await Promise.all([
        import('echarts/core'),
        import('echarts/charts'),
        import('echarts/components'),
        import('echarts/renderers'),
      ])
      core.use([
        charts.BarChart,
        components.GridComponent,
        components.TooltipComponent,
        renderers.CanvasRenderer,
      ])
      if (disposed || !container.current) return

      const values = daily.map((day) => Number(day.income_micro))
      const labels = daily.map((day) => day.date.slice(5))

      chart = core.init(container.current, undefined, { renderer: 'canvas' })
      chart.setOption({
        animationDuration: 260,
        grid: { left: 6, right: 10, top: 14, bottom: 6, containLabel: true },
        tooltip: {
          trigger: 'axis',
          backgroundColor: '#ffffff',
          borderColor: '#eceef2',
          borderWidth: 1,
          padding: [10, 12],
          extraCssText: 'border-radius:12px;box-shadow:none;',
          textStyle: { color: '#4b5160', fontSize: 12 },
          axisPointer: { type: 'shadow', shadowStyle: { color: 'rgba(77,107,254,0.06)' } },
          formatter: (params: unknown) => {
            const list = Array.isArray(params) ? params : [params]
            const first = list[0] as { axisValue?: string; value?: number | string } | undefined
            const value = Number(first?.value ?? 0)
            return `${first?.axisValue ?? ''}<br/>${
              formatYuan(Number.isSafeInteger(value) ? value : 0)
            }`
          },
        },
        xAxis: {
          type: 'category',
          data: labels,
          axisLine: { lineStyle: { color: '#eceef2' } },
          axisTick: { show: false },
          axisLabel: { color: '#8a909c', fontSize: 12, interval: 4 },
        },
        yAxis: {
          type: 'value',
          splitLine: { lineStyle: { color: '#eceef2' } },
          axisLabel: {
            color: '#8a909c',
            fontSize: 12,
            formatter: (value: number) => formatYuan(Math.round(value)),
          },
        },
        series: [
          {
            name: copy.pages.sellerConsole.chart.series,
            type: 'bar',
            data: values,
            barMaxWidth: 16,
            itemStyle: { color: '#4d6bfe', borderRadius: [4, 4, 0, 0] },
            emphasis: { itemStyle: { color: '#3b58f0' } },
          },
        ],
      })

      observer = new ResizeObserver(() => chart?.resize())
      observer.observe(container.current)
    })()

    return () => {
      disposed = true
      observer?.disconnect()
      chart?.dispose()
    }
  }, [daily])

  return (
    <div
      ref={container}
      className="h-[220px] w-full"
      role="img"
      aria-label={copy.pages.sellerConsole.chart.ariaLabel}
    />
  )
}
