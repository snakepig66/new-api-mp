import * as ToastPrimitive from '@radix-ui/react-toast'
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'

import { copy } from '@/copy'
import { cn } from '@/lib/cn'

export type ToastTone = 'default' | 'success' | 'error'

export interface ToastOptions {
  title: string
  description?: string
  tone?: ToastTone
}

interface ToastItem extends ToastOptions {
  id: number
}

interface ToastContextValue {
  show: (options: ToastOptions) => void
}

const ToastContext = createContext<ToastContextValue | null>(null)

/* Success is not a separate colour: only errors get the red bar. */
const accent: Record<ToastTone, string> = {
  default: 'border-l-brand',
  success: 'border-l-brand',
  error: 'border-l-danger',
}

export interface ToastProviderProps {
  children: ReactNode
  duration?: number
}

export function ToastProvider({ children, duration = 4000 }: ToastProviderProps) {
  const [items, setItems] = useState<ToastItem[]>([])
  const nextId = useRef(0)

  const show = useCallback((options: ToastOptions) => {
    nextId.current += 1
    const item = { ...options, id: nextId.current }
    setItems((current) => [...current, item])
  }, [])

  const remove = useCallback((id: number) => {
    setItems((current) => current.filter((item) => item.id !== id))
  }, [])

  const context = useMemo(() => ({ show }), [show])

  return (
    <ToastContext.Provider value={context}>
      <ToastPrimitive.Provider swipeDirection="right" duration={duration}>
        {children}
        {items.map((item) => (
          <ToastPrimitive.Root
            key={item.id}
            open
            duration={duration}
            onOpenChange={(open) => {
              if (!open) remove(item.id)
            }}
            className={cn(
              'toast-root glass-strong flex items-start gap-3 rounded-md border-l-4 px-4 py-3',
              accent[item.tone ?? 'default'],
            )}
          >
            <div className="min-w-0 flex-1">
              <ToastPrimitive.Title className="text-[14px] font-medium leading-5 text-ink">
                {item.title}
              </ToastPrimitive.Title>
              {item.description ? (
                <ToastPrimitive.Description className="mt-1 text-[13px] leading-5 text-ink-3">
                  {item.description}
                </ToastPrimitive.Description>
              ) : null}
            </div>
            <ToastPrimitive.Close
              aria-label={copy.actions.close}
              className="mt-0.5 grid h-6 w-6 shrink-0 place-items-center rounded-full text-ink-3 transition-colors hover:bg-white/70 hover:text-ink"
            >
              <svg width="11" height="11" viewBox="0 0 12 12" fill="none" aria-hidden="true">
                <path d="M1.5 1.5l9 9M10.5 1.5l-9 9" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
              </svg>
            </ToastPrimitive.Close>
          </ToastPrimitive.Root>
        ))}
        <ToastPrimitive.Viewport className="fixed bottom-4 right-4 z-[60] flex w-[min(360px,calc(100vw-2rem))] flex-col gap-2 outline-none" />
      </ToastPrimitive.Provider>
    </ToastContext.Provider>
  )
}

export function useToast(): ToastContextValue {
  const context = useContext(ToastContext)
  if (!context) throw new Error('useToast must be used inside ToastProvider')
  return context
}
