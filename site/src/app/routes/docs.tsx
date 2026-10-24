import { createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { PageHeader, Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from '@/ui'

export const Route = createFileRoute('/docs')({
  component: DocsPage,
})

const sections = [
  { id: 'quickstart', label: copy.pages.docs.sections.quickstart },
  { id: 'billing', label: copy.pages.docs.sections.billing },
  { id: 'errors', label: copy.pages.docs.sections.errors },
  { id: 'terms', label: copy.pages.docs.sections.terms },
  { id: 'privacy', label: copy.pages.docs.sections.privacy },
]

function DocsPage() {
  const docs = copy.pages.docs

  return (
    <div className="mx-auto max-w-[1180px] px-5 py-10">
      <PageHeader title={docs.title} lead={docs.lead} />

      <div className="mt-8 lg:grid lg:grid-cols-[180px_minmax(0,760px)] lg:gap-12">
        <aside className="hidden lg:block">
          <nav className="sticky top-24" aria-label={docs.toc}>
            <div className="mb-2 text-[11.5px] text-muted">{docs.toc}</div>
            <ul className="space-y-1 border-l border-line">
              {sections.map((section) => (
                <li key={section.id}>
                  <a
                    href={`#${section.id}`}
                    className="-ml-px block border-l border-transparent py-0.5 pl-3 text-[12.5px] text-muted transition-colors hover:border-brand hover:text-ink"
                  >
                    {section.label}
                  </a>
                </li>
              ))}
            </ul>
          </nav>
        </aside>

        <div className="prose-doc min-w-0">
          <nav className="flex flex-wrap gap-2 lg:hidden" aria-label={docs.toc}>
            {sections.map((section) => (
              <a
                key={section.id}
                href={`#${section.id}`}
                className="rounded-[3px] border border-line bg-surface px-2 py-1 text-[11.5px] text-muted"
              >
                {section.label}
              </a>
            ))}
          </nav>

          <section>
            <h2 id="quickstart">{docs.sections.quickstart}</h2>
            <p>{docs.quickstart.body}</p>
            <pre className="code-block mt-4">
              <code>{docs.quickstart.code}</code>
            </pre>
            <p className="mt-2 text-[11.5px]">{docs.quickstart.note}</p>
          </section>

          <section>
            <h2 id="billing">{docs.sections.billing}</h2>
            <p>{docs.billing.body}</p>
            <ul className="mt-3 list-disc space-y-1 pl-5 text-[13px] text-muted">
              {docs.billing.items.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          </section>

          <section>
            <h2 id="errors">{docs.sections.errors}</h2>
            <p>{docs.errors.body}</p>
            <div className="mt-3">
              <Table minWidth={560}>
                <TableHead>
                  <TableRow interactive={false}>
                    <TableHeaderCell>{docs.errors.columns.code}</TableHeaderCell>
                    <TableHeaderCell>{docs.errors.columns.meaning}</TableHeaderCell>
                    <TableHeaderCell>{docs.errors.columns.action}</TableHeaderCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {docs.errors.rows.map((row) => (
                    <TableRow key={row.code}>
                      <TableCell numeric className="w-20">
                        {row.code}
                      </TableCell>
                      <TableCell>{row.meaning}</TableCell>
                      <TableCell className="text-muted">{row.action}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </section>

          <section>
            <h2 id="terms">{docs.sections.terms}</h2>
            <p>{docs.terms.body}</p>
          </section>

          <section>
            <h2 id="privacy">{docs.sections.privacy}</h2>
            <p>{docs.privacy.body}</p>
          </section>
        </div>
      </div>
    </div>
  )
}
