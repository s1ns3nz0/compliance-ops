import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { cn } from '@/lib/utils'

/** Renders Markdown (GFM) to React elements. No raw HTML is rendered (`skipHtml`), links open in a new tab. */
const components: Components = {
  a: ({ href, children }) => (
    <a href={href} target="_blank" rel="noopener noreferrer" className="text-sky-700 underline underline-offset-2 hover:text-sky-900">
      {children}
    </a>
  ),
  p: ({ children }) => <p className="my-1.5 leading-relaxed first:mt-0 last:mb-0">{children}</p>,
  h1: ({ children }) => <h1 className="mt-3 mb-1.5 text-base font-semibold first:mt-0">{children}</h1>,
  h2: ({ children }) => <h2 className="mt-3 mb-1.5 text-sm font-semibold first:mt-0">{children}</h2>,
  h3: ({ children }) => <h3 className="mt-2 mb-1 text-sm font-semibold first:mt-0">{children}</h3>,
  ul: ({ children }) => <ul className="my-1.5 list-disc space-y-0.5 pl-5">{children}</ul>,
  ol: ({ children }) => <ol className="my-1.5 list-decimal space-y-0.5 pl-5">{children}</ol>,
  li: ({ children, className }) => <li className={cn(className === 'task-list-item' && 'list-none -ml-5 flex gap-1.5')}>{children}</li>,
  input: ({ checked }) => <input type="checkbox" checked={!!checked} readOnly className="mt-1 size-3.5 accent-slate-700" aria-label={checked ? 'Done' : 'To do'} />,
  blockquote: ({ children }) => <blockquote className="my-1.5 border-l-2 border-slate-300 pl-3 text-slate-600">{children}</blockquote>,
  code: ({ children, className }) =>
    className ? (
      <code className={cn('font-mono text-[0.85em]', className)}>{children}</code>
    ) : (
      <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-[0.85em] text-slate-800">{children}</code>
    ),
  pre: ({ children }) => <pre className="my-2 overflow-x-auto rounded-md bg-slate-900 p-3 text-xs text-slate-100">{children}</pre>,
  hr: () => <hr className="my-3 border-slate-200" />,
  table: ({ children }) => (
    <div className="my-2 overflow-x-auto">
      <table className="w-full border-collapse text-xs">{children}</table>
    </div>
  ),
  th: ({ children }) => <th className="border border-slate-200 bg-slate-50 px-2 py-1 text-left font-semibold">{children}</th>,
  td: ({ children }) => <td className="border border-slate-200 px-2 py-1 align-top">{children}</td>,
  img: ({ alt }) => <span className="text-xs text-slate-400">[image: {alt || 'omitted'}]</span>,
}

export function Markdown({ source, className }: { source: string; className?: string }) {
  return (
    <div className={cn('text-sm text-slate-800', className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components} skipHtml>
        {source}
      </ReactMarkdown>
    </div>
  )
}
