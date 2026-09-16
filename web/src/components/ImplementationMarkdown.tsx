import { Markdown } from '@/components/Markdown'
import { implementationMarkdownBlocks } from '@/lib/implementationMarkdown'

type ImplementationMarkdownProps = {
  source: string
  className?: string
}

/** Renders implementation-template starters as visible muted guidance until their section is completed. */
export function ImplementationMarkdown({ source, className }: ImplementationMarkdownProps) {
  const blocks = implementationMarkdownBlocks(source)

  if (blocks.length === 1 && !blocks[0]?.header) return <Markdown source={blocks[0]?.source ?? ''} className={className} />

  return (
    <div className={className}>
      {blocks.map((block, index) => {
        if (!block.header) return <Markdown key={index} source={block.source} />

        if (block.placeholder) {
          return (
            <div key={index}>
              <Markdown source={block.header} />
              <Markdown source={block.body ?? ''} className="text-slate-400" />
            </div>
          )
        }

        return <Markdown key={index} source={block.source} />
      })}
    </div>
  )
}
