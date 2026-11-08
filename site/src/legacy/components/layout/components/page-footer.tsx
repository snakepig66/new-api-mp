import { createContext, useContext, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

const PageFooterContext = createContext<HTMLDivElement | null>(null)

export function PageFooterProvider(props: {
  container: HTMLDivElement | null
  children: ReactNode
}) {
  return (
    <PageFooterContext.Provider value={props.container}>
      {props.children}
    </PageFooterContext.Provider>
  )
}

export function PageFooterPortal(props: { children: ReactNode }) {
  const container = useContext(PageFooterContext)
  if (!container) return null
  return createPortal(props.children, container)
}
