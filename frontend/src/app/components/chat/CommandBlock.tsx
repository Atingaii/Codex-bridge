import React from 'react';
import { Check, Clipboard, Terminal } from 'lucide-react';
import type { UIText } from '../../lib/i18n';
import { Button } from '../ui';

export function CommandBlock({
  label,
  value,
  copied,
  onCopy,
  t,
}: {
  label: string;
  value: string;
  copied: boolean;
  onCopy: () => void;
  t: UIText;
}) {
  return (
    <div className="overflow-hidden rounded-md border border-border bg-background/70">
      <div className="flex items-center gap-2 border-b border-border bg-muted/40 px-2 py-1.5">
        <Terminal className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-xs font-medium">{label}</span>
        <Button variant="ghost" size="icon" type="button" className="ml-auto h-6 w-6 rounded-md text-muted-foreground" onClick={onCopy} aria-label={t.copy} title={copied ? t.copied : t.copy}>
          {copied ? <Check className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" /> : <Clipboard className="h-3.5 w-3.5" />}
        </Button>
      </div>
      <pre className="max-h-28 overflow-x-auto whitespace-pre-wrap p-2 font-mono text-[11px] leading-relaxed text-muted-foreground elegant-scrollbar">{value}</pre>
    </div>
  );
}
