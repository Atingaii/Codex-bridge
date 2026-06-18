import React, { useMemo } from 'react';
import { renderMarkdown, stripMachineContractLines } from '../../lib/utils';

export function MessageContent({ content, stripMachineContracts = false }: { content: string; stripMachineContracts?: boolean }) {
  const visibleContent = useMemo(
    () => stripMachineContracts ? stripMachineContractLines(content) : String(content || ''),
    [content, stripMachineContracts]
  );
  const html = useMemo(() => {
    return renderMarkdown(visibleContent);
  }, [visibleContent]);

  return <div className="text-[14px] leading-relaxed text-foreground" dangerouslySetInnerHTML={{ __html: html }} />;
}
