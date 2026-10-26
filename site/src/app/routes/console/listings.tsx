import { Outlet, createFileRoute } from '@tanstack/react-router'

import { SellerOnly } from '@/features/seller/SellerOnly'

export const Route = createFileRoute('/console/listings')({
  component: ListingsLayout,
})

function ListingsLayout() {
  return (
    <SellerOnly>
      <Outlet />
    </SellerOnly>
  )
}
