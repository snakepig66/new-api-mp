import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { copy } from '@/copy'
import {
  Button,
  Dialog,
  Empty,
  PageHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
  useToast,
} from '@/ui'

import {
  adminHolidaysQuery,
  importAdminHolidays,
  type AdminHoliday,
} from '@/features/admin/api'
import {
  ErrorBlock,
  Field,
  Notice,
  PageSkeleton,
  Select,
} from '@/features/admin/components'
import { adminErrorMessage } from '@/features/admin/errors'
import { parseHolidayInput, shanghaiYear } from '@/features/admin/schemas'
import { adminHolidayStatus } from '@/features/admin/status'

export const Route = createFileRoute('/console/ops/tools/')({
  component: HolidaysContent,
})

const page = copy.pages.adminHolidays
const common = copy.pages.adminCommon

function HolidaysContent() {
  const holidays = useQuery(adminHolidaysQuery())
  const [year, setYear] = useState(() => shanghaiYear())
  const [dialogOpen, setDialogOpen] = useState(false)

  const data = holidays.data
  const rows = (data?.holidays ?? []).filter((item) => item.date.startsWith(`${year}-`))

  const years = (() => {
    const set = new Set<number>([shanghaiYear(), shanghaiYear() + 1, year])
    if (data) {
      set.add(data.next_year)
      for (const item of data.holidays) {
        const parsed = Number(item.date.slice(0, 4))
        if (Number.isInteger(parsed) && parsed > 2000) set.add(parsed)
      }
    }
    return [...set].sort((a, b) => a - b)
  })()

  return (
    <>
      <PageHeader
        title={page.title}
        lead={page.lead}
        actions={
          <Button size="sm" onClick={() => setDialogOpen(true)}>
            {page.import}
          </Button>
        }
      />

      {data && !data.next_year_imported ? (
        <Notice
          tone="warning"
          title={page.warning(data.next_year)}
          className="mt-5"
        >
          {page.warningHint}
        </Notice>
      ) : null}

      <div className="mt-5 flex flex-wrap items-center gap-3">
        <label htmlFor="holiday-year" className="text-[14px] text-ink-2">
          {page.year}
        </label>
        <Select
          id="holiday-year"
          className="w-32"
          value={String(year)}
          onChange={(event) => setYear(Number(event.target.value))}
        >
          {years.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </Select>
      </div>

      <div className="mt-5">
        {holidays.isPending ? (
          <PageSkeleton rows={8} />
        ) : holidays.isError ? (
          <ErrorBlock error={holidays.error} onRetry={() => void holidays.refetch()} />
        ) : rows.length === 0 ? (
          <Empty
            title={page.emptyTitle(year)}
            reason={page.emptyReason}
            next={page.emptyNext}
            action={
              <Button variant="secondary" size="sm" onClick={() => setDialogOpen(true)}>
                {page.import}
              </Button>
            }
          />
        ) : (
          <div className="rounded-md border border-line bg-bg p-6">
          <Table minWidth={480}>
            <TableHead>
              <TableRow>
                <TableHeaderCell>{page.columns.date}</TableHeaderCell>
                <TableHeaderCell>{page.columns.kind}</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rows.map((item) => {
                const view = adminHolidayStatus(item.kind)
                return (
                  <TableRow key={item.date}>
                    <TableCell className="tabular-nums text-[13px]">{item.date}</TableCell>
                    <TableCell>
                      <Tag tone={view.tone} dot>
                        {view.label}
                      </Tag>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          </div>
        )}
      </div>

      <ImportDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        onImported={(entries) => {
          const first = entries[0]?.date.slice(0, 4)
          if (first) setYear(Number(first))
        }}
      />
    </>
  )
}

function ImportDialog({
  open,
  onClose,
  onImported,
}: {
  open: boolean
  onClose: () => void
  onImported: (entries: AdminHoliday[]) => void
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [text, setText] = useState('')
  const [error, setError] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: importAdminHolidays,
    onSuccess: (_data, entries) => {
      void queryClient.invalidateQueries({ queryKey: ['marketplace', 'admin', 'holidays'] })
      toast.show({
        title: page.success(entries.length),
        description: page.successHint,
        tone: 'success',
      })
      onImported(entries)
      setText('')
      setError(null)
      onClose()
    },
    onError: (err) => setError(adminErrorMessage(err)),
  })

  function submit() {
    const parsed = parseHolidayInput(text)
    if (!parsed.ok) {
      setError(parsed.error)
      return
    }
    setError(null)
    mutation.mutate(parsed.entries)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          onClose()
          setError(null)
          mutation.reset()
        }
      }}
      title={page.dialog.title}
      description={page.dialog.lead}
      className="max-w-xl"
      footer={
        <>
          <Button
            variant="secondary"
            disabled={mutation.isPending}
            onClick={() => {
              onClose()
              setError(null)
              mutation.reset()
            }}
          >
            {common.cancel}
          </Button>
          <Button loading={mutation.isPending} onClick={submit}>
            {mutation.isPending ? page.dialog.submitting : page.dialog.submit}
          </Button>
        </>
      }
    >
      <Field
        label={page.dialog.input}
        htmlFor="holiday-import"
        hint={page.dialog.hint}
        error={error}
      >
        <textarea
          id="holiday-import"
          rows={9}
          spellCheck={false}
          value={text}
          placeholder={page.dialog.placeholder}
          onChange={(event) => {
            setText(event.target.value)
            setError(null)
          }}
          className="w-full resize-y rounded-md border border-transparent bg-bg-soft px-4 py-3 text-[15px] leading-6 text-ink transition-colors placeholder:text-ink-3 focus:border-brand focus:bg-bg focus:outline-none focus:ring-[3px] focus:ring-brand-soft"
        />
      </Field>
    </Dialog>
  )
}
