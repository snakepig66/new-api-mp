import type { ReactNode, ThHTMLAttributes, TdHTMLAttributes } from 'react'

import { cn } from '@/lib/cn'

export interface TableProps {
  children: ReactNode
  className?: string
  minWidth?: number
}

/** Dense data table with hairline rules. Scrolls horizontally on narrow screens. */
export function Table({ children, className, minWidth = 720 }: TableProps) {
  return (
    <div className="w-full overflow-x-auto border-t border-line">
      <table
        className={cn('w-full border-collapse text-[13px]', className)}
        style={{ minWidth }}
      >
        {children}
      </table>
    </div>
  )
}

export function TableHead({ children, className }: { children: ReactNode; className?: string }) {
  return <thead className={className}>{children}</thead>
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
        'border-b border-line/70 last:border-b-0',
        interactive && 'transition-colors hover:bg-wash/70',
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
        'whitespace-nowrap border-b border-line bg-wash px-3 py-2 text-left text-[11.5px] font-medium text-muted',
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
  return (
    <td
      className={cn(
        'px-3 py-2.5 align-middle text-[13px] text-ink',
        numeric && 'text-right font-numeric tabular-nums',
        className,
      )}
      {...props}
    >
      {children}
    </td>
  )
}
