import { createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { PageHeader } from '@/ui'

export const Route = createFileRoute('/agreement')({
  component: AgreementPage,
})

function AgreementPage() {
  const page = copy.pages.agreement

  return (
    <div className="mx-auto max-w-[1200px] px-5 py-10">
      <PageHeader title={page.title} />
      <article className="mt-8 max-w-2xl space-y-4 text-body leading-7 text-ink-2">
        <p>{page.placeholder}</p>
        <p className="border-t border-line pt-4 text-meta leading-6 text-ink-3">
          {page.contactLabel}：{page.contact}
        </p>
      </article>
    </div>
  )
}
