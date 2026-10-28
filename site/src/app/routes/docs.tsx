import { useQuery } from '@tanstack/react-query'
import { Link, createFileRoute } from '@tanstack/react-router'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'

import { copy } from '@/copy'
import { squareModelsQuery } from '@/features/market/api'
import { copyText } from '@/features/market/clipboard'
import { CodeBlock, type CodeTabItem } from '@/features/market/code-block'
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

const SECTION_IDS = [
  'quickstart',
  'text',
  'images',
  'embeddings',
  'errors',
  'billing',
  'clients',
  'seller',
] as const

type SectionId = (typeof SECTION_IDS)[number]

function BaseUrl({
  value,
  label,
  hint,
  copyLabel,
  copiedLabel,
  failedLabel,
}: {
  value: string
  label: string
  hint: string
  copyLabel: string
  copiedLabel: string
  failedLabel: string
}) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<number | null>(null)

  useEffect(() => {
    return () => {
      if (timer.current !== null) window.clearTimeout(timer.current)
    }
  }, [])

  async function handleCopy() {
    const ok = await copyText(value)
    setState(ok ? 'copied' : 'failed')
    if (timer.current !== null) window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setState('idle'), 1600)
  }

  return (
    <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-line bg-bg-soft px-4 py-3">
      <span className="text-[12px] font-medium tracking-wide text-ink-3">{label}</span>
      <code className="min-w-0 break-all font-mono text-[21px] font-medium leading-7 tracking-tight text-ink sm:text-[24px]">
        {value}
      </code>
      <button
        type="button"
        onClick={() => void handleCopy()}
        className={cn(
          'ml-auto shrink-0 rounded-full border border-line-strong bg-bg px-3 py-1 text-[13px] transition-colors',
          state === 'failed'
            ? 'text-danger'
            : 'text-ink-2 hover:border-brand/40 hover:text-brand-ink',
        )}
      >
        {state === 'copied' ? copiedLabel : state === 'failed' ? failedLabel : copyLabel}
      </button>
      <p className="w-full text-[13px] leading-5 text-ink-3">{hint}</p>
    </div>
  )
}

function Section({
  id,
  title,
  children,
}: {
  id: SectionId
  title: string
  children: ReactNode
}) {
  const docs = copy.pages.docs

  return (
    <section
      id={id}
      className="scroll-mt-24 border-t border-line pt-10 first:border-t-0 first:pt-0 [&+section]:mt-10"
    >
      <h2 className="text-[24px] font-semibold tracking-tight text-ink">{title}</h2>
      <BaseUrl
        value={docs.baseUrl}
        label={docs.baseUrlLabel}
        hint={docs.baseUrlHint}
        copyLabel={docs.baseUrlCopy}
        copiedLabel={docs.baseUrlCopied}
        failedLabel={docs.baseUrlFailed}
      />
      <div className="mt-5 max-w-[760px] space-y-4 text-[15px] leading-7 text-ink-2">
        {children}
      </div>
    </section>
  )
}

function requestTabs(
  labels: { curl: string; python: string; node: string },
  examples: { curl: string; python: string; node: string },
): CodeTabItem[] {
  return [
    { key: 'curl', label: labels.curl, code: examples.curl },
    { key: 'python', label: labels.python, code: examples.python },
    { key: 'node', label: labels.node, code: examples.node },
  ]
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
  const baseUrl = docs.baseUrl
  const [search, setSearch] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const active = useScrollSpy()

  // The first live model keeps the snippets runnable; the seed id is only a
  // fallback while the square request is in flight or unavailable.
  const model = models.data?.[0]?.model ?? 'deepseek-flash'
  const query = search.trim().toLowerCase()

  const keywords: Record<SectionId, string> = useMemo(
    () => ({
      quickstart: [
        docs.sections.quickstart,
        docs.quickstart.body,
        ...docs.quickstart.steps.map((step) => `${step.title} ${step.body}`),
      ].join(' '),
      text: [
        docs.sections.text,
        docs.text.body,
        docs.text.note,
        docs.text.streamTitle,
        docs.text.streamBody,
        docs.text.streamNote,
      ].join(' '),
      images: [docs.sections.images, docs.images.body, docs.images.note].join(' '),
      embeddings: [docs.sections.embeddings, docs.embeddings.body, docs.embeddings.note].join(' '),
      errors: [
        docs.sections.errors,
        docs.errors.body,
        docs.errors.rows.map((row) => `${row.code} ${row.meaning} ${row.action}`).join(' '),
      ].join(' '),
      billing: [
        docs.sections.billing,
        docs.billing.body,
        docs.billing.formulaTitle,
        docs.billing.formula,
        docs.billing.formulaNote,
        docs.billing.cacheTitle,
        docs.billing.cacheBody,
        docs.billing.flatTitle,
        docs.billing.flatBody,
        docs.billing.sellerFeeTitle,
        docs.billing.sellerFee,
        ...docs.billing.items,
      ].join(' '),
      clients: [
        docs.sections.clients,
        docs.clients.body,
        docs.clients.note,
        ...docs.clients.items.map((item) => `${item.name} ${item.body}`),
      ].join(' '),
      seller: [
        docs.sections.seller,
        docs.seller.body,
        ...docs.seller.steps.map((step) => `${step.title} ${step.body}`),
        docs.seller.verifyTitle,
        docs.seller.verifyBody,
        docs.seller.patrolTitle,
        docs.seller.patrolBody,
        docs.seller.payoutTitle,
        docs.seller.payoutBody,
        docs.seller.note,
      ].join(' '),
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
    <div className="mx-auto w-full max-w-[1240px] px-5 py-12 sm:px-6 sm:py-16">
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

      <div className="mt-10 grid gap-10 lg:grid-cols-[240px_minmax(0,1fr)] xl:grid-cols-[240px_minmax(0,760px)_200px] xl:gap-12">
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

              <ol className="grid gap-3 sm:grid-cols-3">
                {docs.quickstart.steps.map((step, index) => (
                  <li key={step.title} className="rounded-lg border border-line bg-bg p-3">
                    <span className="grid h-6 w-6 place-items-center rounded-full bg-brand-soft text-[12px] font-medium tabular-nums text-brand-ink">
                      {index + 1}
                    </span>
                    <div className="mt-2 text-[14px] font-medium text-ink">{step.title}</div>
                    <p className="mt-1 text-[13px] leading-5 text-ink-2">{step.body}</p>
                  </li>
                ))}
              </ol>

              <CodeBlock
                tabs={requestTabs(docs.codeTabs, {
                  curl: docs.quickstart.code.curl(baseUrl, model),
                  python: docs.quickstart.code.python(baseUrl, model),
                  node: docs.quickstart.code.node(baseUrl, model),
                })}
              />
              <p className="text-[13px] leading-6 text-ink-3">{docs.quickstart.note}</p>
              {models.data && models.data.length > 0 ? (
                <div className="mt-4">
                  <p className="text-[13px] text-ink-3">{docs.quickstart.modelsLabel}</p>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {models.data.map((m) => (
                      <Link
                        key={m.model}
                        to="/models/$id"
                        params={{ id: m.model }}
                        className="rounded-md border border-line bg-surface px-2.5 py-1 font-mono text-[13px] text-ink-2 transition-colors hover:border-brand-ink/40 hover:text-brand-ink"
                      >
                        {m.model}
                      </Link>
                    ))}
                  </div>
                </div>
              ) : null}
            </Section>

            <Section id="text" title={docs.sections.text}>
              <p>{docs.text.body}</p>
              <CodeBlock
                tabs={requestTabs(docs.codeTabs, {
                  curl: docs.text.code.curl(baseUrl, model),
                  python: docs.text.code.python(baseUrl, model),
                  node: docs.text.code.node(baseUrl, model),
                })}
              />
              <p className="text-[13px] leading-6 text-ink-3">{docs.text.note}</p>

              <h3 className="mt-6 text-[18px] font-semibold text-ink">{docs.text.streamTitle}</h3>
              <p>{docs.text.streamBody}</p>
              <CodeBlock
                tabs={requestTabs(docs.codeTabs, {
                  curl: docs.text.streamCode.curl(baseUrl, model),
                  python: docs.text.streamCode.python(baseUrl, model),
                  node: docs.text.streamCode.node(baseUrl, model),
                })}
              />
              <p className="text-[13px] leading-6 text-ink-3">{docs.text.streamNote}</p>
            </Section>

            <Section id="images" title={docs.sections.images}>
              <p>{docs.images.body}</p>
              <CodeBlock
                tabs={requestTabs(docs.codeTabs, {
                  curl: docs.images.code.curl(baseUrl),
                  python: docs.images.code.python(baseUrl),
                  node: docs.images.code.node(baseUrl),
                })}
              />
              <p className="text-[13px] leading-6 text-ink-3">{docs.images.note}</p>
            </Section>

            <Section id="embeddings" title={docs.sections.embeddings}>
              <p>{docs.embeddings.body}</p>
              <CodeBlock
                tabs={requestTabs(docs.codeTabs, {
                  curl: docs.embeddings.code.curl(baseUrl),
                  python: docs.embeddings.code.python(baseUrl),
                  node: docs.embeddings.code.node(baseUrl),
                })}
              />
              <p className="text-[13px] leading-6 text-ink-3">{docs.embeddings.note}</p>
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
                      <TableCell className="w-20 whitespace-nowrap font-mono font-medium">
                        {row.code}
                      </TableCell>
                      <TableCell>{row.meaning}</TableCell>
                      <TableCell className="text-ink-2">{row.action}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              <p className="pt-1 text-[13px] leading-6 text-ink-3">{docs.errors.testNote}</p>
              <CodeBlock
                tabs={requestTabs(docs.codeTabs, {
                  curl: docs.errors.code.curl(baseUrl, model),
                  python: docs.errors.code.python(baseUrl, model),
                  node: docs.errors.code.node(baseUrl, model),
                })}
              />
            </Section>

            <Section id="billing" title={docs.sections.billing}>
              <p>{docs.billing.body}</p>

              <div className="rounded-lg border border-brand/25 bg-brand-soft px-4 py-3">
                <div className="text-[13px] font-medium text-brand-ink">
                  {docs.billing.formulaTitle}
                </div>
                <div className="mt-1 break-words font-mono text-[18px] font-semibold leading-7 text-brand-ink">
                  {docs.billing.formula}
                </div>
                <p className="mt-2 text-[13px] leading-6 text-ink-2">
                  {docs.billing.formulaNote}
                </p>
              </div>

              <h3 className="mt-6 text-[18px] font-semibold text-ink">{docs.billing.cacheTitle}</h3>
              <p>{docs.billing.cacheBody}</p>

              <h3 className="mt-6 text-[18px] font-semibold text-ink">{docs.billing.flatTitle}</h3>
              <p>{docs.billing.flatBody}</p>

              <div className="rounded-lg border border-line px-4 py-3">
                <div className="text-[14px] font-medium text-ink">
                  {docs.billing.sellerFeeTitle}
                </div>
                <p className="mt-1">{docs.billing.sellerFee}</p>
              </div>

              <ul className="list-disc space-y-1 pl-5">
                {docs.billing.items.map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
            </Section>

            <Section id="clients" title={docs.sections.clients}>
              <p>{docs.clients.body}</p>
              <div className="space-y-4">
                {docs.clients.items.map((item) => (
                  <div key={item.name} className="rounded-lg border border-line bg-bg p-4">
                    <h3 className="text-[16px] font-semibold text-ink">{item.name}</h3>
                    <p className="mt-1 text-[14px] leading-6 text-ink-2">{item.body}</p>
                    <CodeBlock className="mt-3" code={item.code} label={item.name} />
                  </div>
                ))}
              </div>
              <p className="text-[13px] leading-6 text-ink-3">{docs.clients.note}</p>
            </Section>

            <Section id="seller" title={docs.sections.seller}>
              <p>{docs.seller.body}</p>

              <h3 className="mt-6 text-[18px] font-semibold text-ink">
                {docs.seller.openTitle}
              </h3>
              <ol className="space-y-3">
                {docs.seller.steps.map((step, index) => (
                  <li key={step.title} className="flex gap-3">
                    <span className="mt-0.5 grid h-6 w-6 shrink-0 place-items-center rounded-full bg-brand-soft text-[12px] font-medium tabular-nums text-brand-ink">
                      {index + 1}
                    </span>
                    <div>
                      <div className="text-[15px] font-medium text-ink">{step.title}</div>
                      <p className="mt-0.5 text-[14px] leading-6 text-ink-2">{step.body}</p>
                    </div>
                  </li>
                ))}
              </ol>

              <h3 className="mt-6 text-[18px] font-semibold text-ink">
                {docs.seller.verifyTitle}
              </h3>
              <p>{docs.seller.verifyBody}</p>

              <h3 className="mt-6 text-[18px] font-semibold text-ink">
                {docs.seller.patrolTitle}
              </h3>
              <p>{docs.seller.patrolBody}</p>

              <h3 className="mt-6 text-[18px] font-semibold text-ink">
                {docs.seller.payoutTitle}
              </h3>
              <p>{docs.seller.payoutBody}</p>

              <p className="text-[13px] leading-6 text-ink-3">{docs.seller.note}</p>
            </Section>
          </div>
        </div>

        <aside className="hidden xl:block">
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
