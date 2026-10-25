/**
 * Per-model verification result rendering shared by the listing list and the
 * listing detail page. Failed models are always shown individually with their
 * translated reason — including the "not in the key's /models list" case.
 */

import { copy } from '@/copy'
import { formatDateTime } from '@/lib/time'
import { Tag } from '@/ui'

import type { SellerVerification, VerifyModelResult } from './api'
import { modelVerifyReason, probeFailReasonLabel } from './errors'

const page = copy.pages.sellerVerification

function failedCount(results: VerifyModelResult[]): number {
  return results.filter((result) => !result.ok).length
}

export function VerificationTag({ verification }: { verification: SellerVerification }) {
  const failed = failedCount(verification.model_results)
  if (verification.ok && failed === 0) {
    return (
      <Tag tone="brand" dot>
        {page.passedAll}
      </Tag>
    )
  }
  return (
    <Tag tone="danger" dot>
      {page.failedCount(failed || 1)}
    </Tag>
  )
}

export function ModelResultList({
  results,
  compact = false,
}: {
  results: VerifyModelResult[]
  compact?: boolean
}) {
  if (results.length === 0) {
    return <span className="text-[13px] text-ink-3">{page.empty}</span>
  }
  return (
    <ul className={compact ? 'space-y-1' : 'space-y-2'}>
      {results.map((result) => (
        <li key={`${result.model}-${result.upstream_model}`} className="text-[13px] leading-5">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span
              aria-hidden="true"
              className={result.ok ? 'text-brand-ink' : 'text-danger'}
            >
              {result.ok ? '✓' : '✕'}
            </span>
            <span className={result.ok ? 'text-ink' : 'font-medium text-danger'}>
              {result.model}
            </span>
            <span className="text-ink-3">
              {result.ok ? page.modelPassed : page.modelFailed}
            </span>
            <span className="tabular-nums text-ink-3">{page.latency(result.latency_ms)}</span>
            {result.ok && result.prompt_cache_support ? (
              <span className="text-brand-ink">{page.cache}</span>
            ) : null}
            {result.upstream_model && result.upstream_model !== result.model ? (
              <span className="text-ink-3">{page.upstream(result.upstream_model)}</span>
            ) : null}
          </div>
          {!result.ok ? (
            <p className="mt-0.5 text-danger">{modelVerifyReason(result.fail_reason)}</p>
          ) : null}
        </li>
      ))}
    </ul>
  )
}

export function VerificationSummary({
  verification,
  compact = false,
}: {
  verification?: SellerVerification | null
  compact?: boolean
}) {
  if (!verification) {
    return <span className="text-[13px] text-ink-3">—</span>
  }
  return (
    <div className="space-y-2">
      <VerificationTag verification={verification} />
      {!verification.ok && verification.fail_reason ? (
        <p className="text-[13px] leading-5 text-danger">
          {page.overallReason}：{probeFailReasonLabel(verification.fail_reason)}
        </p>
      ) : null}
      <ModelResultList results={verification.model_results} compact={compact} />
      {!compact && verification.checked_at ? (
        <p className="text-[13px] text-ink-3">
          {page.checkedAt(formatDateTime(verification.checked_at))}
        </p>
      ) : null}
    </div>
  )
}
