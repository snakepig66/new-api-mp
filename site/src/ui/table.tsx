import {
  createContext,
  useContext,
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
      <div className="w-full overflow-x-auto">
        <table
          className={cn('w-full border-collapse text-[14px]', className)}
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
}: {
  children: ReactNode
  className?: string
  interactive?: boolean
}) {
  return (
    <tr
      className={cn(
        'border-b border-line transition-colors last:border-b-0 hover:bg-bg-soft',
        interactive && 'cursor-pointer',
        className,
      )}
    >
      {children}
    </tr>
  )
}

export interface TableHeadProps extends ThHTMLAttributes<HTMLTableCellElement> {
  numeric?: boolean
}

export function TableHeaderCell({ numeric = false, className, children, ...props }: TableHeadProps) {
  return (
    <th
      scope="col"
      className={cn(
        'whitespace-nowrap bg-bg-soft px-4 py-3 text-left text-[13px] font-medium text-ink-3 first:rounded-l-md last:rounded-r-md',
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
        'px-4 align-middle text-[14px] leading-5 text-ink',
        size === 'compact' ? 'py-2.5' : 'py-4',
        numeric && 'text-right tabular-nums',
        className,
      )}
      {...props}
    >
      {children}
    </td>
  )
}
