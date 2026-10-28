/**
 * 近 30 天按模型堆叠的双图：Tokens 用量与收入。
 *
 * ECharts 按需动态引入（与 IncomeChart 相同的模式），只有概览页会加载这一块；
 * 只注册用到的东西：BarChart + Grid/Tooltip/Legend + CanvasRenderer。
 * 金额只做展示：柱子使用后端整数微元数值，提示与坐标轴统一交给 formatYuan。
 */

import { useEffect, useRef } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'

import type { SellerOverviewDay, SellerOverviewModelSeries } from './api'

const page = copy.pages.consoleEarnings

const countFormat = new Intl.NumberFormat('zh-CN')
const compactFormat = new Intl.NumberFormat('zh-CN', {
  notation: 'compact',
  maximumFractionDigits: 1,
})

/** 分组数字，用于卡片与表格：2450000 -> "2,450,000"。 */
export function formatCount(value: number): string {
  return countFormat.format(value)
}

/** 后端把长尾模型折进 "other"，图例与提示统一渲染成「其他」。 */
function modelLabel(model: string): string {
  return model === 'other' ? page.charts.other : model
}

const PALETTE = [
  '#4d6bfe',
  '#34c3a2',
  '#f2a93b',
  '#9a6bff',
  '#4bb3ff',
  '#f272a1',
  '#7ac145',
  '#c98a2d',
]
const OTHER_COLOR = '#a3a8b3'

type Metric = 'tokens' | 'income_micro'

interface TooltipParam {
  seriesName?: string
  value?: number | string
  dataIndex?: number
  marker?: string
}

/** 图表窗口内的全部日期（各模型序列稠密对齐，缺的日期补 0）。 */
function windowDates(series: readonly SellerOverviewModelSeries[]): string[] {
  return Array.from(
    new Set(series.flatMap((row) => row.days.map((day) => day.date))),
  ).sort()
}

function seriesValues(days: readonly SellerOverviewDay[], dates: readonly string[], metric: Metric): number[] {
  const byDate = new Map(days.map((day) => [day.date, Number(day[metric])]))
  return dates.map((date) => byDate.get(date) ?? 0)
}

function StackedByModelChart({
  series,
  metric,
  ariaLabel,
}: {
  series: readonly SellerOverviewModelSeries[]
  metric: Metric
  ariaLabel: string
}) {
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
        components.LegendComponent,
        renderers.CanvasRenderer,
      ])
      if (disposed || !container.current) return

      const dates = windowDates(series)
      const labels = dates.map((date) => date.slice(5))

      try {
        chart = core.init(container.current, undefined, { renderer: 'canvas' })
        chart.setOption({
          animationDuration: 260,
          grid: { left: 6, right: 10, top: 14, bottom: 34, containLabel: true },
          legend: {
            type: 'scroll',
            bottom: 0,
            left: 0,
            icon: 'roundRect',
            itemWidth: 10,
            itemHeight: 10,
            itemGap: 12,
            textStyle: { color: '#8a909c', fontSize: 12 },
          },
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
              const list = Array.isArray(params) ? (params as TooltipParam[]) : []
              const index = list[0]?.dataIndex ?? -1
              const rows = list.filter((item) => Number(item.value ?? 0) > 0)
              const total = rows.reduce((sum, item) => sum + Number(item.value ?? 0), 0)
              const body = rows.map((item) => {
                const value = Number(item.value ?? 0)
                const text = metric === 'tokens' ? formatCount(value) : formatYuan(value)
                return `<div>${item.marker ?? ''}${item.seriesName ?? ''} ${text}</div>`
              })
              const totalText = metric === 'tokens' ? formatCount(total) : formatYuan(total)
              return [
                `<div style="font-weight:600;margin-bottom:4px">${dates[index] ?? ''}</div>`,
                ...body,
                `<div style="margin-top:4px">${page.charts.total} ${totalText}</div>`,
              ].join('')
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
              formatter: (value: number) =>
                metric === 'tokens' ? compactFormat.format(value) : formatYuan(Math.round(value)),
            },
          },
          series: series.map((row, index) => ({
            name: modelLabel(row.model),
            type: 'bar',
            stack: 'model',
            data: seriesValues(row.days, dates, metric),
            barMaxWidth: 14,
            itemStyle: {
              color: row.model === 'other' ? OTHER_COLOR : PALETTE[index % PALETTE.length],
            },
          })),
        })
      } catch {
        // 没有画布支持的环境（如测试里的 jsdom）保留空容器即可，数字仍出现在卡片与表格里。
      }

      if (typeof ResizeObserver !== 'undefined' && container.current) {
        observer = new ResizeObserver(() => chart?.resize())
        observer.observe(container.current)
      }
    })()

    return () => {
      disposed = true
      observer?.disconnect()
      try {
        chart?.dispose()
      } catch {
        // 画布不可用的环境里 dispose 也可能抛错，忽略即可。
      }
    }
  }, [series, metric])

  return (
    <div
      ref={container}
      className="h-[240px] w-full"
      role="img"
      aria-label={ariaLabel}
    />
  )
}

/** 近 30 天 Tokens 与收入双图，均按模型堆叠；空数据时渲染零序列。 */
export function OverviewCharts({ series }: { series: readonly SellerOverviewModelSeries[] }) {
  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <div className="min-w-0">
        <h3 className="text-body font-medium text-ink">{page.charts.tokensTitle}</h3>
        <div className="mt-3">
          <StackedByModelChart
            series={series}
            metric="tokens"
            ariaLabel={page.charts.tokensAria}
          />
        </div>
      </div>
      <div className="min-w-0">
        <h3 className="text-body font-medium text-ink">{page.charts.incomeTitle}</h3>
        <div className="mt-3">
          <StackedByModelChart
            series={series}
            metric="income_micro"
            ariaLabel={page.charts.incomeAria}
          />
        </div>
      </div>
    </div>
  )
}
