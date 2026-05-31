import React, { useMemo, useState } from 'react';
import { AlertCircle, Check, ChevronDown, Clipboard, Terminal, User } from 'lucide-react';
import type { ChatItem, ToolEvent } from '../../lib/types';
import type { UIText } from '../../lib/i18n';
import { Button } from '../ui';
import { MessageContent } from './MessageContent';
import { cn, copyText, formatTime, stringsTrim, stripMachineContractLines } from '../../lib/utils';

export function MessageItem({ msg, t, readOnly = false }: { msg: Extract<ChatItem, { type: 'message' }>, t: UIText, readOnly?: boolean }) {
  const isUser = msg.role === 'user';
  const [copied, setCopied] = useState(false);
  const stripContracts = !isUser;
  const visibleContent = useMemo(
    () => stripContracts ? stripMachineContractLines(msg.content) : msg.content,
    [msg.content, stripContracts]
  );
  const hasVisibleContent = Boolean(stringsTrim(visibleContent));

  const copyMessage = async () => {
    try {
      await copyText(visibleContent);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      setCopied(false);
    }
  };

  if (!isUser && !hasVisibleContent) return null;

  return (
    <div className="flex gap-4 w-full max-w-4xl mx-auto rounded-lg border border-border/70 bg-card/50 px-3 py-3 group">
      <div className="shrink-0 mt-1">
        {isUser ? (
          <div className="h-6 w-6 rounded-md bg-secondary flex items-center justify-center border border-border shadow-sm">
            <User className="h-3.5 w-3.5 text-secondary-foreground" />
          </div>
        ) : (
          <div className={cn("h-6 w-6 rounded-md flex items-center justify-center shadow-sm", msg.role === 'system' ? "bg-secondary border border-border" : "bg-primary")}>
            {msg.role === 'system' ? <AlertCircle className="h-3.5 w-3.5 text-muted-foreground" /> : <Terminal className="h-3.5 w-3.5 text-primary-foreground" />}
          </div>
        )}
      </div>

      <div className="flex flex-col gap-2 min-w-0 flex-1">
        <div className="flex items-center gap-2 mb-0.5 min-h-6">
          <span className="text-xs font-semibold shrink-0">{isUser ? t.user : msg.role === 'system' ? t.system : 'Codex'}</span>
          <span className="text-[10px] text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity">{formatTime(msg.createdAt)}</span>
          <Button
            variant="ghost"
            size="icon"
            type="button"
            className={cn(
              "ml-auto h-6 w-6 rounded-md text-muted-foreground transition-opacity hover:text-foreground",
              readOnly ? "pointer-events-none opacity-0" : copied ? "opacity-100 text-emerald-600 dark:text-emerald-400" : "opacity-100 md:opacity-0 md:group-hover:opacity-100"
            )}
            onClick={copyMessage}
            disabled={readOnly}
            aria-label={t.copyMessage}
            title={copied ? t.copied : t.copy}
          >
            {copied ? <Check className="h-3.5 w-3.5" /> : <Clipboard className="h-3.5 w-3.5" />}
          </Button>
        </div>

        <MessageContent content={visibleContent} stripMachineContracts={stripContracts} />
      </div>
    </div>
  );
}

export function ToolItem({ tool, t }: { tool: ToolEvent, t: UIText }) {
  const [copied, setCopied] = useState(false);
  const content = [tool.command, tool.output, typeof tool.exitCode === 'number' ? `exit: ${tool.exitCode}` : ''].filter(Boolean).join('\n\n');

  const copyToolOutput = async () => {
    try {
      await copyText(content);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div className="w-full max-w-4xl mx-auto mt-2 bg-muted/30 border border-border rounded-lg overflow-hidden text-[13px] group/tool">
      <div className="flex items-center gap-2 px-3 py-1.5 bg-muted/50 border-b border-border">
        <Terminal className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="font-medium text-xs">{t.run}: {tool.name || t.bash}</span>
        <span className="ml-auto text-xs text-muted-foreground font-mono truncate max-w-[260px]">{tool.command || tool.input || tool.status || t.running}</span>
        <Button
          variant="ghost"
          size="icon"
          type="button"
          className={cn(
            "h-6 w-6 rounded-md text-muted-foreground transition-opacity hover:text-foreground",
            copied ? "opacity-100 text-emerald-600 dark:text-emerald-400" : "opacity-100 md:opacity-0 md:group-hover/tool:opacity-100"
          )}
          onClick={copyToolOutput}
          disabled={!content}
          aria-label={t.copyOutput}
          title={copied ? t.copied : t.copy}
        >
          {copied ? <Check className="h-3.5 w-3.5" /> : <Clipboard className="h-3.5 w-3.5" />}
        </Button>
        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground opacity-50" />
      </div>
      <div className="p-3 font-mono text-[11px] whitespace-pre-wrap text-muted-foreground overflow-x-auto max-h-40 bg-background/50 elegant-scrollbar">
        {content}
      </div>
    </div>
  );
}
