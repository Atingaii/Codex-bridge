import React from 'react';
import { TerminalSquare, Info } from 'lucide-react';
import type { UIText } from '../../lib/i18n';
import { CommandBlock } from './CommandBlock';

// TakeoverHint renders the local CLI takeover command for an ACP-backed chat
// session (target B). It only shows a real command when the Bridge actually
// resolved one; otherwise it shows a neutral "unavailable" note. It never
// fabricates a command (honesty rule carried from PR-1).
export function TakeoverHint({
  command,
  nativeId,
  available,
  copied,
  onCopy,
  t,
}: {
  command?: string;
  nativeId?: string;
  available: boolean;
  copied: boolean;
  onCopy: () => void;
  t: UIText;
}) {
  const hasCommand = available && !!command;
  return (
    <div className="rounded-md border border-border bg-muted/20 p-2.5 space-y-2">
      <div className="flex items-center gap-1.5 text-xs font-medium">
        <TerminalSquare className="h-3.5 w-3.5 text-muted-foreground" />
        <span>{t.takeoverTitle}</span>
      </div>
      {hasCommand ? (
        <>
          <p className="text-[11px] text-muted-foreground leading-relaxed">{t.takeoverHint}</p>
          <CommandBlock
            label={nativeId ? `${t.takeoverTitle} · ${nativeId}` : t.takeoverTitle}
            value={command as string}
            copied={copied}
            onCopy={onCopy}
            t={t}
          />
        </>
      ) : (
        <div className="flex items-start gap-1.5 text-[11px] text-muted-foreground leading-relaxed">
          <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{t.takeoverUnavailable}</span>
        </div>
      )}
    </div>
  );
}
