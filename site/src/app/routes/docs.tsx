import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import type { ReactNode } from 'react'

import { copy } from '@/copy'
import { squareModelsQuery } from '@/features/market/api'
import { CodeBlock } from '@/features/market/code-block'
import { currentBaseUrl } from '@/features/market/url'
import {
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
} from '@/ui'

export const Route = createFileRoute('/docs')({
  component: DocsPage,
})

const SECTION_IDS = ['quickstart', 'streaming', 'billing', 'errors', 'terms', 'privacy'] as const

type SectionId = (typeof SECTION_IDS)[number]

function Section({
  id,
  title,
  children,
}: {
  id: SectionId
  title: string
  children: ReactNode
}) {
  return (
    <section
      id={id}
      className="mt-10 scroll-mt-24 border-t border-line pt-8 first:mt-0 first:border-t-0 first:pt-0"
    >
      <h2 className="text-[18px] font-semibold tracking-tight text-ink">{title}</h2>
      <div className="mt-3 space-y-3 text-[13.5px] leading-7 text-muted">{children}</div>
    </section>
  )
}

function DocsPage() {
  const docs = copy.pages.docs
  const models = useQuery(squareModelsQuery())
  const baseUrl = currentBaseUrl()
  // The first live model keeps the snippets runnable; the seed id is only a
  // fallback while the square request is in flight or unavailable.
  const model = models.data?.[0]?.model ?? 'deepseek-flash'

  return (
    <div className="mx-auto max-w-[1180px] px-5 py-10">
      <PageHeader title={docs.title} lead={docs.lead} />

      <div className="mt-8 lg:grid lg:grid-cols-[180px_minmax(0,760px)] lg:gap-12">
        <aside className="hidden lg:block">
          <nav className="sticky top-24" aria-label={docs.toc}>
            <div className="mb-2 text-[11.5px] text-muted">{docs.toc}</div>
            <ul className="space-y-1 border-l border-line">
              {SECTION_IDS.map((id) => (
                <li key={id}>
                  <a
                    href={`#${id}`}
                    className="-ml-px block border-l border-transparent py-0.5 pl-3 text-[12.5px] text-muted transition-colors hover:border-brand hover:text-ink"
                  >
                    {docs.sections[id]}
                  </a>
                </li>
              ))}
            </ul>
          </nav>
        </aside>

        <div className="min-w-0">
          <nav className="mb-6 flex flex-wrap gap-2 lg:hidden" aria-label={docs.toc}>
            {SECTION_IDS.map((id) => (
              <a
                key={id}
                href={`#${id}`}
                className="rounded-[3px] border border-line bg-surface px-2 py-1 text-[11.5px] text-muted"
              >
                {docs.sections[id]}
              </a>
            ))}
          </nav>

          <Section id="quickstart" title={docs.sections.quickstart}>
            <p>{docs.quickstart.body}</p>
            <CodeBlock code={docs.quickstart.codeWithBase(baseUrl, model)} label="curl" />
            <p className="text-[11.5px] leading-5">{docs.quickstart.note}</p>
          </Section>

          <Section id="streaming" title={docs.sections.streaming}>
            <p>{docs.streaming.body}</p>
            <CodeBlock code={docs.streaming.codeWithBase(baseUrl, model)} label="curl" />
            <p className="text-[11.5px] leading-5">{docs.streaming.note}</p>
          </Section>

          <Section id="billing" title={docs.sections.billing}>
            <p>{docs.billing.body}</p>

            <h3 className="mt-6 text-[13.5px] font-semibold text-ink">{docs.billing.peakTitle}</h3>
            <Table minWidth={520}>
              <TableHead>
                <TableRow interactive={false}>
                  <TableHeaderCell>{docs.billing.peakColumns.tier}</TableHeaderCell>
                  <TableHeaderCell>{docs.billing.peakColumns.window}</TableHeaderCell>
                  <TableHeaderCell>{docs.billing.peakColumns.rule}</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {docs.billing.peakRows.map((row) => (
                  <TableRow key={row.tier}>
                    <TableCell className="whitespace-nowrap font-medium">{row.tier}</TableCell>
                    <TableCell>{row.window}</TableCell>
                    <TableCell className="text-muted">{row.rule}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <p className="text-[11.5px] leading-5">{docs.billing.peakNote}</p>

            <dl className="divide-y divide-line border-y border-line text-[13px]">
              <div className="py-3">
                <dt className="font-medium text-ink">{docs.billing.perCallTitle}</dt>
                <dd className="mt-0.5">{docs.billing.perCall}</dd>
              </div>
              <div className="py-3">
                <dt className="font-medium text-ink">{docs.billing.failureTitle}</dt>
                <dd className="mt-0.5">{docs.billing.failure}</dd>
              </div>
              <div className="py-3">
                <dt className="font-medium text-ink">{docs.billing.settlementTitle}</dt>
                <dd className="mt-0.5">{docs.billing.settlement}</dd>
              </div>
            </dl>

            <ul className="list-disc space-y-1 pl-5">
              {docs.billing.items.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>

            <p className="pt-1 text-[11.5px] leading-5">{docs.billing.sniff}</p>
            <CodeBlock code={docs.billing.codeWithBase(baseUrl)} label="curl" />
          </Section>

          <Section id="errors" title={docs.sections.errors}>
            <p>{docs.errors.body}</p>
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
            <p className="pt-1 text-[11.5px] leading-5">{docs.errors.testNote}</p>
            <CodeBlock code={docs.errors.codeWithBase(baseUrl, model)} label="curl" />
          </Section>

          <Section id="terms" title={docs.sections.terms}>
            <p>{docs.terms.body}</p>
          </Section>

          <Section id="privacy" title={docs.sections.privacy}>
            <p>{docs.privacy.body}</p>
          </Section>
        </div>
      </div>
    </div>
  )
}
