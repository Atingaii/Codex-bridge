import React, { useMemo } from 'react';
import { escapeBasic, renderInlineMarkdown, stripMachineContractLines } from '../../lib/utils';

export function MessageContent({ content, stripMachineContracts = false }: { content: string; stripMachineContracts?: boolean }) {
  const visibleContent = useMemo(
    () => stripMachineContracts ? stripMachineContractLines(content) : String(content || ''),
    [content, stripMachineContracts]
  );
  const html = useMemo(() => {
    const chunks = String(visibleContent || '').split(/```([\s\S]*?)```/g);
    return chunks.map((chunk, index) => {
      if (index % 2 === 1) {
        return `<pre class="my-3 overflow-x-auto rounded-lg border border-border bg-muted/70 p-3 text-xs leading-relaxed text-foreground dark:bg-[#0f172a] dark:text-slate-200"><code>${escapeBasic(chunk.replace(/^\w+\n/, ''))}</code></pre>`;
      }
      return renderInlineMarkdown(chunk).replace(/\n/g, '<br />');
    }).join('');
  }, [visibleContent]);

  return <div className="text-[14px] leading-relaxed text-foreground" dangerouslySetInnerHTML={{ __html: html }} />;
}
