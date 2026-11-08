import type { HTMLAttributes, ReactNode } from 'react'

type CodeBlockProps = HTMLAttributes<HTMLDivElement> & {
  code: string
  language: string
  title?: ReactNode
  showLineNumbers?: boolean
  maxExpandedLines?: number
}

export function CodeBlock({ code, title, className, ...props }: CodeBlockProps) {
  const { language: _language, showLineNumbers: _n, maxExpandedLines: _m, ...rest } = props
  return (
    <div className={className} {...rest}>
      {title ? <div className='mb-1 text-xs font-medium'>{title}</div> : null}
      <pre className='bg-muted overflow-auto rounded-md p-2 text-xs'>
        <code>{code}</code>
      </pre>
    </div>
  )
}

export function CodeBlockCopyButton() {
  return null
}
