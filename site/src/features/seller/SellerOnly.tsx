import { useQuery } from '@tanstack/react-query'
import { Navigate } from '@tanstack/react-router'
import type { ReactNode } from 'react'

import { copy } from '@/copy'

import { meQuery } from './api'
import { AsyncError, PageSkeleton } from './components'
import { sellerErrorMessage } from './errors'
import { isSellerActive } from './status'

/**
 * Guard for seller-only console pages. Buyers keep using the rest of the
 * console; only 我的渠道 / 收益与提现 bounce to onboarding.
 */
export function SellerOnly({ children }: { children: ReactNode }) {
  const me = useQuery(meQuery())

  if (me.isPending) return <PageSkeleton />
  if (me.isError || !me.data) {
    return (
      <AsyncError
        title={copy.pages.sellerCommon.loadError}
        reason={sellerErrorMessage(me.error)}
        onRetry={() => void me.refetch()}
        retryLabel={copy.actions.retry}
      />
    )
  }
  if (!isSellerActive(me.data.seller)) {
    return <Navigate to="/console/onboarding" replace />
  }
  return <>{children}</>
}
