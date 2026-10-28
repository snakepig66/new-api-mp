import {
  createContext,
  useContext,
  type HTMLAttributes,
  type ReactNode,
  type ThHTMLAttributes,
  type TdHTMLAttributes,
} from 'react'

import { cn } from '@/lib/cn'

export type TableSize = 'default' | 'compact'

const TableSizeContext = createContext<TableSize>('default')

export interface TableProps {
  children: ReactNode
  className?: string
  minWidth?: number
  /** `compact` tightens body rows to 40px; default rows are ~52px. */
  size?: TableSize
}

/**
 * Borderless data table. The header is a soft grey band; body rows are
 * separated by one hairline. Scrolls horizontally on narrow screens.
 */
export function Table({ children, className, minWidth = 720, size = 'default' }: TableProps) {
  return (
    <TableSizeContext.Provider value={size}>
      {/* `relative` anchors absolutely positioned descendants (the sr-only
          labels inside cells); without it they escape the scroll container and
          inflate the document's scrollWidth on 390px screens. */}
      <div className="glass-inset relative w-full overflow-x-auto p-1">
        <table
          className={cn('w-full border-collapse text-body', className)}
          style={{ minWidth }}
        >
          {children}
        </table>
      </div>
    </TableSizeContext.Provider>
  )
}

export function TableHead({ children, className }: { children: ReactNode; className?: string }) {
  /* Header cells carry their own soft band; the row must not add a hairline. */
  return (
    <thead className={cn('[&_tr]:border-0 [&_tr:hover]:bg-transparent', className)}>
      {children}
    </thead>
  )
}

export function TableBody({ children, className }: { children: ReactNode; className?: string }) {
  return <tbody className={className}>{children}</tbody>
}

export function TableRow({
  children,
  className,
  interactive = false,
  ...props
}: HTMLAttributes<HTMLTableRowElement> & {
  children: ReactNode
  interactive?: boolean
}) {
  return (
    <tr
      className={cn(
        'border-b border-line transition-colors last:border-b-0 hover:bg-brand/[0.05]',
        interactive && 'cursor-pointer',
        className,
      )}
      {...props}
    >
      {children}
    </tr>
  )
}

export interface TableHeadProps extends ThHTMLAttributes<HTMLTableCellElement> {
  numeric?: boolean
}

export function TableHeaderCell({ numeric = false, className, children, ...props }: TableHeadProps) {
  const size = useContext(TableSizeContext)

  return (
    <th
      scope="col"
      className={cn(
        'whitespace-nowrap bg-brand-soft/60 px-4 text-left text-caption font-medium text-ink-3 first:rounded-l-md last:rounded-r-md',
        size === 'compact' ? 'py-2.5' : 'py-3.5',
        numeric && 'text-right',
        className,
      )}
      {...props}
    >
      {children}
    </th>
  )
}

export interface TableCellProps extends TdHTMLAttributes<HTMLTableCellElement> {
  numeric?: boolean
}

export function TableCell({ numeric = false, className, children, ...props }: TableCellProps) {
  const size = useContext(TableSizeContext)

  return (
    <td
      className={cn(
        'px-4 align-middle text-body leading-5 text-ink',
        size === 'compact' ? 'py-2.5' : 'py-3.5',
        numeric && 'text-right tabular-nums',
        className,
      )}
      {...props}
    >
      {children}
    </td>
  )
}
