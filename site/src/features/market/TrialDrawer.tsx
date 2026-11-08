import { useState } from 'react'

import { copy } from '@/copy'
import { ApiError } from '@/lib/api'
import { Button, Drawer } from '@/ui'

import { postTrial, type TrialMessage } from './trial'

const page = copy.pages.modelDetail.trial

export interface TrialDrawerProps {
  model: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * Lightweight playground for one model: a chat transcript plus an input box,
 * calling /pg/chat/completions with the signed-in user's session. Cost accrues
 * to the user's balance at official prices; the entry point is only rendered
 * for models with official supply.
 */
export function TrialDrawer({ model, open, onOpenChange }: TrialDrawerProps) {
  const [messages, setMessages] = useState<TrialMessage[]>([])
  const [input, setInput] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const send = async () => {
    const text = input.trim()
    if (!text || sending) return
    const next: TrialMessage[] = [...messages, { role: 'user', content: text }]
    setMessages(next)
    setInput('')
    setSending(true)
    setError(null)
    try {
      const reply = await postTrial(model, next)
      setMessages((prev) => [...prev, { role: 'assistant', content: reply }])
    } catch (err) {
      setError(err instanceof ApiError ? err.message || page.failed : page.failed)
    } finally {
      setSending(false)
    }
  }

  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title={page.title(model)}
      description={page.hint}
      footer={
        <div className="flex items-end gap-2">
          <textarea
            aria-label={page.send}
            rows={2}
            value={input}
            placeholder={page.empty}
            onChange={(event) => setInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault()
                void send()
              }
            }}
            className="min-h-[44px] flex-1 resize-none rounded-md border border-white/70 bg-white/55 px-3 py-2 text-body leading-6 text-ink backdrop-blur-md placeholder:text-ink-3 focus:border-brand/60 focus:bg-white/85 focus:outline-none focus:ring-[3px] focus:ring-brand/20"
          />
          <Button loading={sending} disabled={!input.trim()} onClick={() => void send()}>
            {sending ? page.sending : page.send}
          </Button>
        </div>
      }
    >
      <div className="space-y-3">
        {messages.length === 0 ? (
          <p className="rounded-lg border border-line bg-surface/70 px-3 py-2.5 text-meta leading-6 text-ink-3">
            {page.officialOnly}
          </p>
        ) : (
          messages.map((message, index) => (
            <div
              key={index}
              className={
                message.role === 'user'
                  ? 'rounded-lg bg-brand-soft/60 px-3 py-2.5'
                  : 'rounded-lg border border-line bg-surface px-3 py-2.5'
              }
            >
              <div className="text-caption text-ink-3">
                {message.role === 'user' ? page.you : model}
              </div>
              <div className="mt-1 whitespace-pre-wrap break-words text-body leading-6 text-ink">
                {message.content}
              </div>
            </div>
          ))
        )}
        {sending ? <p className="text-meta text-ink-3">{page.sending}</p> : null}
        {error ? (
          <p role="alert" className="rounded-md bg-danger/10 px-3 py-2 text-meta leading-5 text-danger">
            {error}
          </p>
        ) : null}
      </div>
    </Drawer>
  )
}
