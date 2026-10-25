import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'

import { copy } from '@/copy'
import { squareModelsQuery } from '@/features/market/api'
import { CodeBlock } from '@/features/market/code-block'
import { currentBaseUrl } from '@/features/market/url'
import { cn } from '@/lib/cn'
import {
  Input,
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

const SECTION_IDS = ['quickstart', 'streaming', 'billing', 'errors'] as const

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
      className="scroll-mt-24 border-t border-line pt-10 first:border-t-0 first:pt-0 [&+section]:mt-10"
    >
      <h2 className="text-[24px] font-semibold tracking-tight text-ink">{title}</h2>
      <div className="mt-3 max-w-[760px] space-y-4 text-[15px] leading-7 text-ink-2">
        {children}
      </div>
    </section>
  )
}

function useScrollSpy() {
  const [active, setActive] = useState<SectionId>(SECTION_IDS[0])

  useEffect(() => {
    const elements = SECTION_IDS.map((id) => document.getElementById(id)).filter(
      (element): element is HTMLElement => element !== null,
    )

    function onScroll() {
      let next = elements[0]?.id ?? SECTION_IDS[0]
      for (const element of elements) {
        if (element.getBoundingClientRect().top + window.scrollY <= window.scrollY + 140) {
          next = element.id
        }
      }
      setActive(next as SectionId)
    }

    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  return active
}

function DocsPage() {
  const docs = copy.pages.docs
  const models = useQuery(squareModelsQuery())
  const baseUrl = currentBaseUrl()
  const [search, setSearch] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const active = useScrollSpy()

  // The first live model keeps the snippets runnable; the seed id is only a
  // fallback while the square request is in flight or unavailable.
  const model = models.data?.[0]?.model ?? 'deepseek-flash'
  const query = search.trim().toLowerCase()

  const keywords: Record<SectionId, string> = useMemo(
    () => ({
      quickstart: `${docs.sections.quickstart} ${docs.quickstart.body}`,
      streaming: `${docs.sections.streaming} ${docs.streaming.body}`,
      billing: `${docs.sections.billing} ${docs.billing.body} ${docs.billing.peakTitle}`,
      errors: `${docs.sections.errors} ${docs.errors.body}`,
    }),
    [docs],
  )

  const visible = SECTION_IDS.filter(
    (id) => !query || keywords[id].toLowerCase().includes(query),
  )

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        searchRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  return (
    <div className="mx-auto w-full max-w-[1200px] px-5 py-12 sm:px-6 sm:py-16">
      <header>
        <h1 className="text-[32px] font-semibold leading-tight tracking-tight text-ink">
          {docs.title}
        </h1>
        <p className="mt-2 max-w-2xl text-[15px] leading-6 text-ink-2">{docs.lead}</p>

        <div className="relative mt-6 max-w-[420px]">
          <svg
            width="16"
            height="16"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
            className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-ink-3"
          >
            <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.5" />
            <path d="m10.5 10.5 3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
          </svg>
          <Input
            ref={searchRef}
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder={docs.searchPlaceholder}
            aria-label={docs.searchAria}
            className="h-10 rounded-full pl-10 pr-14 text-[14px]"
          />
          <kbd className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 rounded-sm border border-line-strong bg-bg px-1.5 py-0.5 text-[12px] text-ink-3">
            ⌘K
          </kbd>
        </div>
      </header>

      <div className="mt-10 grid gap-10 lg:grid-cols-[200px_minmax(0,1fr)_180px] lg:gap-8 xl:grid-cols-[240px_minmax(0,760px)_200px] xl:gap-12">
        <aside className="hidden lg:block">
          <nav className="sticky top-24" aria-label={docs.toc}>
            <div className="mb-3 text-[13px] text-ink-3">{docs.toc}</div>
            {visible.length === 0 ? (
              <p className="text-[13px] text-ink-3">{docs.searchEmpty}</p>
            ) : (
              <ul className="space-y-1">
                {visible.map((id) => (
                  <li key={id}>
                    <a
                      href={`#${id}`}
                      aria-current={active === id ? 'location' : undefined}
                      className={cn(
                        'block rounded-full px-3 py-1.5 text-[14px] transition-colors',
                        active === id
                          ? 'bg-brand-soft font-medium text-brand-ink'
                          : 'text-ink-2 hover:bg-bg-soft hover:text-ink',
                      )}
                    >
                      {docs.sections[id]}
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </nav>
        </aside>

        <div className="min-w-0">
          <nav className="mb-6 flex flex-wrap gap-2 lg:hidden" aria-label={docs.toc}>
            {visible.map((id) => (
              <a
                key={id}
                href={`#${id}`}
                className={cn(
                  'rounded-full px-3 py-1.5 text-[14px]',
                  active === id
                    ? 'bg-brand-soft font-medium text-brand-ink'
                    : 'bg-bg-soft text-ink-2',
                )}
              >
                {docs.sections[id]}
              </a>
            ))}
          </nav>

          {visible.length === 0 ? (
            <p className="text-[15px] text-ink-3">{docs.searchEmpty}</p>
          ) : null}

          <div className={visible.length === 0 ? 'hidden' : undefined}>
            <Section id="quickstart" title={docs.sections.quickstart}>
              <p>{docs.quickstart.body}</p>
              <CodeBlock code={docs.quickstart.codeWithBase(baseUrl, model)} label="curl" />
              <p className="text-[13px] leading-6 text-ink-3">{docs.quickstart.note}</p>
            </Section>

            <Section id="streaming" title={docs.sections.streaming}>
              <p>{docs.streaming.body}</p>
              <CodeBlock code={docs.streaming.codeWithBase(baseUrl, model)} label="curl" />
              <p className="text-[13px] leading-6 text-ink-3">{docs.streaming.note}</p>
            </Section>

            <Section id="billing" title={docs.sections.billing}>
              <p>{docs.billing.body}</p>

              <h3 className="mt-6 text-[18px] font-semibold text-ink">{docs.billing.peakTitle}</h3>
              <Table minWidth={520} size="compact">
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
                      <TableCell className="text-ink-2">{row.rule}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              <p className="text-[13px] leading-6 text-ink-3">{docs.billing.peakNote}</p>

              <dl className="divide-y divide-line border-y border-line text-[15px]">
                <div className="py-3">
                  <dt className="font-medium text-ink">{docs.billing.perCallTitle}</dt>
                  <dd className="mt-0.5 text-ink-2">{docs.billing.perCall}</dd>
                </div>
                <div className="py-3">
                  <dt className="font-medium text-ink">{docs.billing.failureTitle}</dt>
                  <dd className="mt-0.5 text-ink-2">{docs.billing.failure}</dd>
                </div>
                <div className="py-3">
                  <dt className="font-medium text-ink">{docs.billing.settlementTitle}</dt>
                  <dd className="mt-0.5 text-ink-2">{docs.billing.settlement}</dd>
                </div>
              </dl>

              <ul className="list-disc space-y-1 pl-5">
                {docs.billing.items.map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>

              <p className="pt-1 text-[13px] leading-6 text-ink-3">{docs.billing.sniff}</p>
              <CodeBlock code={docs.billing.codeWithBase(baseUrl)} label="curl" />
            </Section>

            <Section id="errors" title={docs.sections.errors}>
              <p>{docs.errors.body}</p>
              <Table minWidth={560} size="compact">
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
                      <TableCell className="w-20 whitespace-nowrap font-medium">{row.code}</TableCell>
                      <TableCell>{row.meaning}</TableCell>
                      <TableCell className="text-ink-2">{row.action}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              <p className="pt-1 text-[13px] leading-6 text-ink-3">{docs.errors.testNote}</p>
              <CodeBlock code={docs.errors.codeWithBase(baseUrl, model)} label="curl" />
            </Section>
          </div>
        </div>

        <aside className="hidden lg:block">
          <nav className="sticky top-24" aria-label={docs.onThisPage}>
            <div className="mb-3 text-[13px] text-ink-3">{docs.onThisPage}</div>
            {visible.length === 0 ? null : (
              <ul>
                {visible.map((id) => (
                  <li key={id}>
                    <a
                      href={`#${id}`}
                      className={cn(
                        'block border-l-2 py-1 pl-3 text-[13px] transition-colors',
                        active === id
                          ? 'border-brand font-medium text-ink'
                          : 'border-line text-ink-3 hover:border-line-strong hover:text-ink-2',
                      )}
                    >
                      {docs.sections[id]}
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </nav>
        </aside>
      </div>
    </div>
  )
}
