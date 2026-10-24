import { Link, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { Empty, PageHeader, buttonClasses } from '@/ui'

export const Route = createFileRoute('/register')({
  component: RegisterPage,
})

function RegisterPage() {
  const page = copy.pages.register

  return (
    <div className="mx-auto max-w-[720px] px-5 py-12">
      <PageHeader title={page.title} lead={page.lead} />
      <div className="mt-6">
        <Empty
          title={page.emptyTitle}
          reason={page.emptyReason}
          next={page.emptyNext}
          action={
            <Link to="/login" className={buttonClasses('primary', 'md')}>
              {page.action}
            </Link>
          }
        />
      </div>
    </div>
  )
}
