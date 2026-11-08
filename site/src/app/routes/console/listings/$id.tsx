import { Navigate, createFileRoute } from '@tanstack/react-router'

/**
 * 旧挂单详情深链接：跳到列表页并打开对应的编辑抽屉。
 */
export const Route = createFileRoute('/console/listings/$id')({
  component: ListingIdRedirect,
})

function ListingIdRedirect() {
  const { id } = Route.useParams()
  return <Navigate to="/console/listings" search={{ edit: Number(id) }} replace />
}
