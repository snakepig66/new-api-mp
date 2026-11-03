import '@tanstack/react-table'

declare module '@tanstack/react-table' {
  interface ColumnMeta<_TData, _TValue> {
    label?: string
    description?: string
    className?: string
    pinned?: 'left' | 'right'
    mobileTitle?: boolean
    mobileBadge?: boolean
    mobileHidden?: boolean
    mobileOrder?: number
  }
}
