import { copy } from '@/copy'
import { cn } from '@/lib/cn'

export interface SellerIdProps {
  id: number | string
  className?: string
}

/** "分享者 #10023" — never a name or an email. Sans + tabular, not mono. */
export function SellerId({ id, className }: SellerIdProps) {
  return (
    <span className={cn('inline-flex items-baseline gap-1 whitespace-nowrap', className)}>
      <span className="text-[13px] text-ink-3">{copy.identity.seller}</span>
      <span className="text-[14px] tabular-nums text-ink">#{id}</span>
    </span>
  )
}

export interface BuyerCodeProps {
  code: string
  className?: string
}

/** "买家 7F3A9C21" — the buyer-facing pseudonym used on seller records. */
export function CodeAssistant({ code, className }: BuyerCodeProps) {
  return (
    <span className={cn('inline-flex items-baseline gap-1 whitespace-nowrap', className)}>
      <span className="text-[13px] text-ink-3">{copy.identity.buyer}</span>
      <span className="text-[14px] uppercase tabular-nums tracking-[0.04em] text-ink">
        {code}
      </span>
    </span>
  )
}
