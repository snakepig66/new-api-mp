import { Link, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { Empty, PageHeader, Tag, buttonClasses } from '@/ui'

export const Route = createFileRoute('/models/$id')({
  component: ModelDetailPage,
})

function ModelDetailPage() {
  const { id } = Route.useParams()
  const detail = copy.pages.modelDetail

  return (
    <div className="mx-auto max-w-[1180px] px-5 py-10">
      <PageHeader
        title={id}
        lead={detail.lead}
        actions={<Tag tone="neutral">{copy.common.sample}</Tag>}
      />
      <div className="mt-6">
        <Empty
          title={detail.empty.title}
          reason={detail.empty.reason(id)}
          next={detail.empty.next}
          action={
            <Link to="/models" className={buttonClasses('secondary', 'md')}>
              {copy.actions.backToModels}
            </Link>
          }
        />
      </div>
    </div>
  )
}
