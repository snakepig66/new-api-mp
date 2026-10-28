/**
 * 渠道健康卡（P5）: one card per listing on 我的渠道.
 *
 * Everything here comes from two existing seller APIs:
 *   - sellerListingsQuery: vendor/models/status/key fingerprint and actions;
 *   - sellerListingsHealthQuery: latest probe + today's requests/rate/income.
 *
 * The dot is a three-state reading of today's success rate. A listing with no
 * requests has no sample and stays grey, so a brand-new channelis never shown
 * as "offline".
 */

import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import { formatDateTime } from '@/lib/time'
import { cn } from '@/lib/cn'
import { Multiplier, Tag } from '@/ui'

import type { SellerListing, SellerListingHealth } from './api'
import { vendorName } from './api'
import { probeFailReasonLabel } from './errors'
import { healthProbeLatencyMs, probeTypeLabel } from './probe'
import {
  listingHealthLevel,
  listingStatusView,
  pauseReasonView,
  successRateText,
  type ListingHealthLevel,
} from './status'

const page = copy.pages.sellerListings
const healthCopy = page.health
const common = copy.pages.sellerCommon

const dotClass: Record<ListingHealthLevel, string> = {
  healthy: 'bg-brand',
  degraded: 'bg-warn',
  offline: 'bg-danger',
  unknown: 'bg-ink-3',
}

const labelClass: Record<ListingHealthLevel, string> = {
  healthy: 'text-brand-ink',
  degraded: 'text-warn',
  offline: 'text-danger',
  unknown: 'text-ink-3',
}

/** Colour + label of the three-state health dot; exported for tests. */
export function healthLevelView(level: ListingHealthLevel): { label: string; dot: string } {
  switch (level) {
    case 'healthy':
      return { label: healthCopy.available, dot: dotClass.healthy }
    case 'degraded':
      return { label: healthCopy.degraded, dot: dotClass.degraded }
    case 'offline':
      return { label: healthCopy.offline, dot: dotClass.offline }
    default:
      return { label: healthCopy.noSample, dot: dotClass.unknown }
  }
}

function Metric({ label, value, hint }: { label: string; value: ReactNode; hint?: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[13px] text-ink-3">{label}</div>
      <div className="mt-1 tabular-nums text-[15px] font-medium text-ink">{value}</div>
      {hint ? <div className="mt-0.5 text-[12px] leading-4 text-ink-3">{hint}</div> : null}
    </div>
  )
}

function isZeroMicro(value: number | string): boolean {
  return String(value) === '0'
}

function limitsText(listing: SellerListing): string {
  const rpm = listing.rpm_limit > 0 ? String(listing.rpm_limit) : common.unlimited
  const cap = isZeroMicro(listing.daily_cap_micro)
    ? common.unlimited
    : formatYuan(listing.daily_cap_micro)
  return `${listing.concurrency_limit} / ${rpm} / ${cap}`
}

export interface ListingHealthCardProps {
  listing: SellerListing
  health?: SellerListingHealth | null
  actions?: ReactNode
}

export function ListingHealthCard({ listing, health, actions }: ListingHealthCardProps) {
  const level = listingHealthLevel(health)
  const levelView = healthLevelView(level)
  const status = listingStatusView(listing.status)
  const probe = health?.latest_probe ?? null
  const probeLabel = probe ? probeTypeLabel(probe.type) : ''
  const latency = healthProbeLatencyMs(probe)
  const rate = successRateText(health)
  const requests = health?.today_requests ?? 0
  const income = health?.today_income_micro ?? 0

  return (
    <article
      aria-label={`${healthCopy.ariaLabel} ${vendorName(listing.vendor)} #${listing.id}`}
      className="glass p-5"
    >
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span
              role="img"
              aria-label={levelView.label}
              className={cn('h-2.5 w-2.5 shrink-0 rounded-full', levelView.dot)}
            />
            <span className={cn('text-[13px] font-medium', labelClass[level])}>
              {levelView.label}
            </span>
            <span className="font-medium text-ink">{vendorName(listing.vendor)}</span>
            <span className="tabular-nums text-[13px] text-ink-3">#{listing.id}</span>
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-ink-3">
            <Tag tone={status.tone} dot>
              {status.label}
            </Tag>
            <span className="tabular-nums">
              {common.keyFingerprint} {listing.key_fingerprint8 || '—'}
            </span>
            <Multiplier milli={listing.multiplier_milli} />
            <span className="tabular-nums">{limitsText(listing)}</span>
          </div>
        </div>

        <div className="shrink-0 text-right">
          <div className="text-[13px] text-ink-3">{healthCopy.todayIncome}</div>
          <div className="tabular-nums text-[20px] font-semibold leading-tight text-ink">
            {formatYuan(income)}
          </div>
        </div>
      </div>

      <div className="mt-4 flex flex-wrap gap-1">
        {listing.models.map((model) => (
          <Tag key={model}>{model}</Tag>
        ))}
      </div>

      <dl className="mt-4 grid grid-cols-2 gap-3 border-t border-line pt-4 sm:grid-cols-4">
        <Metric
          label={healthCopy.successRate}
          value={rate}
          hint={requests > 0 ? healthCopy.requests(requests) : healthCopy.noSamples}
        />
        <Metric
          label={healthCopy.latency}
          value={latency !== null ? `${latency} ms` : '—'}
        />
        <div className="col-span-2 min-w-0">
          <div className="text-[13px] text-ink-3">
            {healthCopy.lastProbe}
            {probeLabel ? ` · ${probeLabel}` : ''}
          </div>
          {probe ? (
            <div className="mt-1 space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <Tag tone={probe.ok ? 'brand' : 'danger'} dot>
                  {probe.ok ? healthCopy.probePassed : healthCopy.probeFailed}
                </Tag>
                <span className="tabular-nums text-[12px] text-ink-3">
                  {healthCopy.checkedAt(formatDateTime(probe.created_at))}
                </span>
              </div>
              {!probe.ok && probe.fail_reason ? (
                <p className="text-[13px] leading-5 text-danger">
                  {healthCopy.failReason}：{probeFailReasonLabel(probe.fail_reason)}
                </p>
              ) : null}
            </div>
          ) : (
            <div className="mt-1 text-[13px] text-ink-3">{healthCopy.noProbe}</div>
          )}
        </div>
      </dl>

      {listing.status === 'paused' ? (
        <p className="mt-3 text-[13px] text-warn">
          {pauseReasonView(listing.pause_reason || 'manual')}
          {listing.resume_at ? ` · ${page.resumeAt(formatDateTime(listing.resume_at))}` : ''}
        </p>
      ) : null}

      {actions ? (
        <div className="mt-4 flex flex-wrap justify-end gap-1.5 border-t border-line pt-3">
          {actions}
        </div>
      ) : null}
    </article>
  )
}
