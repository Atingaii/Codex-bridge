import React, { useEffect, useState } from 'react';
import { Activity, AlertTriangle, Check, ChevronDown, Command, GitBranch, RefreshCw, Terminal, User, X } from 'lucide-react';
import type { Agent, BridgeCLICapability, OrchestrationEvent, OrchestrationTimelineGroup, OrchestrationVisibleEvent } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { Button } from './ui';
import { OrchestrationFileList } from './OrchestrationFiles';
import { MessageContent } from './chat/MessageContent';
import { ApprovalCard } from './chat/ApprovalCard';
import {
  cn,
  commandData,
  formatDuration,
  formatTime,
  orchestrationApprovalMode,
  orchestrationCapability,
  orchestrationEventKey,
  orchestrationTurnLabel,
  stripMachineContractLines,
} from '../lib/utils';

export function CapabilityMatrix({ agent, t }: { agent: Agent | null; t: UIText }) {
  const rows: Array<{ cli: 'claude' | 'codex'; label: string; cap?: BridgeCLICapability }> = [
    { cli: 'claude', label: 'Claude', cap: orchestrationCapability(agent, 'claude') },
    { cli: 'codex', label: 'Codex', cap: orchestrationCapability(agent, 'codex') },
  ];
  const auto = orchestrationApprovalMode(agent) === 'auto-execute';
  return (
    <div className="rounded-md border border-border bg-muted/20 p-2.5">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.capabilityMatrix}</span>
        <span className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">
          {auto ? t.autoExecute : t.reviewRequired}
        </span>
      </div>
      <div className="space-y-1.5">
        {rows.map((row) => {
          const ok = Boolean(row.cap?.available && (auto || row.cap.browserApproval));
          return (
            <div key={row.cli} className="grid grid-cols-[96px_minmax(0,1fr)_auto] items-center gap-2 text-xs">
              <span className="font-medium">{row.label}</span>
              <span className="truncate text-muted-foreground">{row.cap?.execution || t.notAvailable}</span>
              <span className={cn(
                "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px]",
                ok ? "border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" : "border-destructive/20 bg-destructive/10 text-destructive"
              )}>
                {ok ? <Check className="h-3 w-3" /> : <X className="h-3 w-3" />}
                {ok ? (auto ? t.autoExecute : t.browserApproval) : t.notAvailable}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export function OrchestrationEventItem({ item, t }: { item: OrchestrationVisibleEvent, t: UIText }) {
  const isUser = item.kind === 'user.message';
  const isRun = item.kind.startsWith('run.');
  const avatar = orchestrationAvatar(item, t);
  const title = isUser ? t.user : isRun ? t.run : item.type === 'command' ? t.commands : `${item.role || t.agent}${item.cli ? ` · ${avatar.label}` : ''}`;
  const rawContent = item.content || item.error || '';
  const content = isUser ? rawContent : stripMachineContractLines(rawContent);
  const status = isUser ? '' : item.status;

  return (
    <div className="flex gap-4 w-full max-w-4xl mx-auto rounded-lg border border-border/70 bg-card/50 px-3 py-3 group">
      <div className="shrink-0 mt-1">
        <div className={cn(
          "h-6 w-6 rounded-md flex items-center justify-center shadow-sm border",
          avatar.className
        )}>
          {avatar.icon}
        </div>
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 mb-1 min-h-6">
          <span className="text-xs font-semibold capitalize">{title}</span>
          <span className="text-[10px] text-muted-foreground">{item.kind}</span>
          {item.createdAt && <span className="text-[10px] text-muted-foreground">{formatTime(item.createdAt)}</span>}
          {status && <span className="ml-auto rounded-full border border-border px-2 py-0.5 text-[10px] text-muted-foreground">{status}</span>}
        </div>
        {item.type === 'command' ? (
          <CommandEvent event={item.command} t={t} open />
        ) : content ? (
          <MessageContent content={content} stripMachineContracts={!isUser} />
        ) : item.type === 'message' && item.commands.length > 0 ? (
          <p className="text-sm text-muted-foreground">{t.noVisibleAnswer}</p>
        ) : null}
        {item.type === 'message' && item.files?.length ? (
          <OrchestrationFileList files={item.files} label={t.attachedFiles} />
        ) : null}
        {item.type === 'message' && item.commands.length > 0 && (
          <details className="mt-2 rounded-md border border-border bg-muted/10">
            <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 text-[11px] text-muted-foreground hover:text-foreground">
              <Command className="h-3.5 w-3.5" />
              <span>{t.commandDetails}</span>
              <span className="rounded border border-border px-1.5 py-0.5 text-[10px]">{item.commands.length}</span>
            </summary>
            <div className="space-y-2 border-t border-border p-2">
              {item.commands.map((command, index) => (
                <CommandEvent key={orchestrationEventKey(command, index)} event={command} t={t} />
              ))}
            </div>
          </details>
        )}
      </div>
    </div>
  );
}

export function defaultCollapsedTimelineGroups(groups: OrchestrationTimelineGroup[]) {
  const collapsed: Record<string, boolean> = {};
  const shouldCompact = groups.length > 4;
  const latestTurnKey = latestTurnGroupKey(groups);
  groups.forEach((group, index) => {
    if (group.type !== 'turn') return;
    const isLatest = group.key === latestTurnKey || index === groups.length - 1;
    if (shouldCompact && !isLatest && group.complete && !group.active && !group.incomplete && !group.hasError) {
      collapsed[group.key] = true;
    }
  });
  return collapsed;
}

export function reconcileCollapsedTimelineGroups(current: Record<string, boolean>, groups: OrchestrationTimelineGroup[]) {
  const next: Record<string, boolean> = {};
  const known = new Set(groups.map((group) => group.key));
  Object.entries(current).forEach(([key, value]) => {
    if (known.has(key)) next[key] = value;
  });
  const latestTurnKey = latestTurnGroupKey(groups);
  groups.forEach((group, index) => {
    if (group.type !== 'turn' || Object.prototype.hasOwnProperty.call(next, group.key)) return;
    const isLatest = group.key === latestTurnKey || index === groups.length - 1;
    next[group.key] = groups.length > 40 && !isLatest && group.complete && !group.active && !group.incomplete && !group.hasError;
  });
  return next;
}

function latestTurnGroupKey(groups: OrchestrationTimelineGroup[]) {
  for (let index = groups.length - 1; index >= 0; index -= 1) {
    if (groups[index].type === 'turn') return groups[index].key;
  }
  return '';
}

export function OrchestrationTimelineGroupItem({
  group,
  collapsed,
  onToggle,
  onApprovalDecision,
  t,
}: {
  group: OrchestrationTimelineGroup;
  collapsed: boolean;
  onToggle: () => void;
  onApprovalDecision: (requestId: string, decision: 'accept' | 'decline' | 'cancel') => void;
  t: UIText;
}) {
  if (group.type === 'standalone') {
    return (
      <>
        {group.items.map((item) => item.type === 'event'
          ? <OrchestrationEventItem key={item.key} item={item.event} t={t} />
          : <ApprovalCard key={item.key} item={item.approval} t={t} onDecision={onApprovalDecision} />)}
      </>
    );
  }

  const avatar = orchestrationAvatar({ kind: 'turn.delta', cli: group.cli }, t);
  const turnLabel = orchestrationTurnLabel(group.turnInfo || {}, t) || group.turnId || t.thread;
  const countLabel = orchestrationTimelineGroupCountLabel(group, t);
  const stateLabel = group.incomplete ? t.turnMissingEnd : group.active ? t.running : group.hasError ? t.error : group.complete ? t.ready : t.status;

  return (
    <div className="space-y-2">
      <button
        type="button"
        onClick={onToggle}
        className={cn(
          "mx-auto flex w-full max-w-4xl items-center gap-3 rounded-lg border bg-muted/15 px-3 py-2 text-left hover:bg-muted/35 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
          group.incomplete || group.hasError ? "border-destructive/35" : "border-border/70",
        )}
        aria-expanded={!collapsed}
        title={collapsed ? t.expandTurn : t.collapseTurn}
      >
        <div className={cn(
          "flex h-6 w-6 shrink-0 items-center justify-center rounded-md border shadow-sm",
          group.incomplete || group.hasError ? "border-destructive/25 bg-destructive/10 text-destructive" : avatar.className,
        )}>
          {group.incomplete || group.hasError ? <AlertTriangle className="h-3.5 w-3.5" /> : avatar.icon}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-xs font-semibold">{turnLabel}</span>
            <span className="text-[10px] text-muted-foreground">{group.role || t.agent}{group.cli ? ` · ${avatar.label}` : ''}</span>
            {group.createdAt && <span className="hidden text-[10px] text-muted-foreground sm:inline">{formatTime(group.createdAt)}</span>}
          </div>
          <div className="mt-0.5 flex min-w-0 items-center gap-2 text-[10px] text-muted-foreground">
            <span className="truncate">{countLabel}</span>
            {(group.incomplete || group.hasError || group.active) && (
              <span className={cn(
                "shrink-0 rounded border px-1.5 py-0.5",
                group.incomplete || group.hasError ? "border-destructive/25 text-destructive" : "border-border text-muted-foreground",
              )}>
                {stateLabel}
              </span>
            )}
          </div>
        </div>
        <ChevronDown className={cn("h-4 w-4 shrink-0 text-muted-foreground transition-transform", collapsed && "-rotate-90")} />
      </button>
      {!collapsed && (
        <div className="space-y-3">
          {group.items.map((item) => item.type === 'event'
            ? <OrchestrationEventItem key={item.key} item={item.event} t={t} />
            : <ApprovalCard key={item.key} item={item.approval} t={t} onDecision={onApprovalDecision} />)}
          {group.incomplete && (
            <div className="mx-auto flex w-full max-w-4xl items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{t.turnMissingEndDescription}</span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function orchestrationTimelineGroupCountLabel(group: OrchestrationTimelineGroup, t: UIText) {
  const parts: string[] = [];
  if (group.messageCount) parts.push(`${group.messageCount} ${t.messages}`);
  if (group.commandCount) parts.push(`${group.commandCount} ${t.commands}`);
  if (group.approvalCount) parts.push(`${group.approvalCount} ${t.approvals}`);
  if (group.statusCount) parts.push(`${group.statusCount} ${t.status}`);
  return parts.length ? parts.join(' · ') : t.noVisibleAnswer;
}

function orchestrationAvatar(event: Pick<OrchestrationEvent, 'kind' | 'cli'>, t: UIText) {
  const cli = (event.cli || '').toLowerCase();
  if (event.kind === 'user.message') {
    return {
      label: t.user,
      className: 'bg-secondary border-border text-secondary-foreground',
      icon: <User className="h-3.5 w-3.5" />,
    };
  }
  if (event.kind.startsWith('run.')) {
    return {
      label: t.run,
      className: 'bg-secondary border-border text-muted-foreground',
      icon: <Activity className="h-3.5 w-3.5" />,
    };
  }
  if (event.kind.startsWith('command.')) {
    return {
      label: cli === 'claude' ? 'Claude' : cli === 'codex' ? 'GPT' : 'Command',
      className: cli === 'claude'
        ? 'bg-[#d97757]/10 border-[#d97757]/25 text-[#d97757]'
        : cli === 'codex'
          ? 'bg-emerald-500/10 border-emerald-500/20 text-emerald-700 dark:text-emerald-300'
          : 'bg-muted border-border text-muted-foreground',
      icon: cli === 'claude' ? <ClaudeMark /> : cli === 'codex' ? <OpenAIMark /> : <Command className="h-3.5 w-3.5" />,
    };
  }
  if (cli === 'ccb') {
    return {
      label: 'CCB',
      className: 'bg-sky-500/10 border-sky-500/20 text-sky-700 dark:text-sky-300',
      icon: <GitBranch className="h-3.5 w-3.5" />,
    };
  }
  if (cli === 'claude') {
    return {
      label: 'Claude',
      className: 'bg-[#d97757]/10 border-[#d97757]/25 text-[#d97757]',
      icon: <ClaudeMark />,
    };
  }
  if (cli === 'codex' || cli === 'gpt' || cli.startsWith('gpt-')) {
    return {
      label: 'GPT',
      className: 'bg-emerald-500/10 border-emerald-500/20 text-emerald-700 dark:text-emerald-300',
      icon: <OpenAIMark />,
    };
  }
  return {
    label: event.cli || t.agent,
    className: 'bg-primary border-primary text-primary-foreground',
    icon: <Terminal className="h-3.5 w-3.5" />,
  };
}

function ClaudeMark() {
  return (
    <svg viewBox="0 0 24 24" className="h-4 w-4" aria-hidden="true" focusable="false">
      <path
        fill="currentColor"
        d="M11.2 2.5 9.1 9.1 2.5 11.2a.85.85 0 0 0 0 1.6l6.6 2.1 2.1 6.6a.85.85 0 0 0 1.6 0l2.1-6.6 6.6-2.1a.85.85 0 0 0 0-1.6l-6.6-2.1-2.1-6.6a.85.85 0 0 0-1.6 0Z"
      />
      <path
        fill="currentColor"
        opacity=".55"
        d="M5.4 3.6 8 8.1 3.6 5.4a.62.62 0 0 1 .7-1.02l1.1-.78Zm13.2 0 1.1.78a.62.62 0 0 1 .7 1.02L16 8.1l2.6-4.5ZM3.6 18.6 8.1 16l-2.7 4.4a.62.62 0 0 1-1.02-.7l-.78-1.1Zm16.8 0-.78 1.1a.62.62 0 0 1-1.02.7L16 16l4.4 2.6Z"
      />
    </svg>
  );
}

function OpenAIMark() {
  const petals = Array.from({ length: 6 }, (_, index) => index * 60);
  return (
    <svg viewBox="0 0 24 24" className="h-4 w-4" aria-hidden="true" focusable="false">
      <g fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
        {petals.map((angle) => (
          <path
            key={angle}
            d="M12 4.1c2 0 3.6 1.6 3.6 3.6 0 1.4-.8 2.6-2 3.2l-3.6 2.1"
            transform={`rotate(${angle} 12 12)`}
          />
        ))}
      </g>
      <circle cx="12" cy="12" r="1.35" fill="currentColor" />
    </svg>
  );
}

export function CommandEvent({ event, t, open = false }: { event: OrchestrationEvent, t: UIText, open?: boolean }) {
  const [, setClockTick] = useState(0);
  const data = commandData(event);
  const command = data.command || '';
  const output = data.output || '';
  const status = data.status || event.status || '';
  const exitCode = typeof data.exitCode === 'number' ? data.exitCode : undefined;
  const isActive = event.kind === 'command.start' || status === 'running' || status === 'in_progress';
  const startedAt = typeof data.startedAt === 'number' ? data.startedAt : event.createdAt;
  const completedAt = typeof data.completedAt === 'number' ? data.completedAt : undefined;
  const durationMs = typeof data.durationMs === 'number'
    ? data.durationMs
    : startedAt && completedAt
      ? Math.max(0, (completedAt - startedAt) * 1000)
      : isActive && startedAt
        ? Math.max(0, Date.now() - startedAt * 1000)
        : undefined;
  const durationLabel = formatDuration(durationMs);
  useEffect(() => {
    if (!isActive || !startedAt) return undefined;
    const timer = window.setInterval(() => setClockTick((value) => value + 1), 1000);
    return () => window.clearInterval(timer);
  }, [isActive, startedAt]);

  return (
    <details className="rounded-md border border-border bg-background/70 overflow-hidden" open={open || Boolean(output || event.error)}>
      <summary className="flex cursor-pointer items-center gap-2 border-b border-border bg-muted/30 px-3 py-2 text-[11px] marker:content-none">
        {isActive ? <RefreshCw className="h-3.5 w-3.5 animate-spin text-muted-foreground" /> : <Terminal className="h-3.5 w-3.5 text-muted-foreground" />}
        <code className="min-w-0 flex-1 truncate text-foreground">{command || t.commandEvent}</code>
        {startedAt && <span className="shrink-0 text-[10px] text-muted-foreground">{formatTime(startedAt)}</span>}
        {durationLabel && (
          <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">
            {isActive ? t.runningFor : t.duration} {durationLabel}
          </span>
        )}
        {status && <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">{status}</span>}
        {typeof exitCode === 'number' && <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">exit {exitCode}</span>}
        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
      </summary>
      {output && (
        <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words bg-muted/40 p-3 font-mono text-[11px] leading-relaxed text-foreground/80 dark:bg-[#0f172a] dark:text-slate-200 elegant-scrollbar">
          {output}
        </pre>
      )}
    </details>
  );
}
