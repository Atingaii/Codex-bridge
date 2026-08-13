import React from 'react';
import { Server } from 'lucide-react';
import type { Agent } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { cn } from '../lib/utils';

export function AgentSelector({
  agents,
  selectedAgentId,
  onSelect,
  t,
  className,
  disabled,
  activeAgentIds,
}: {
  agents: Agent[];
  selectedAgentId: string;
  onSelect: (agentId: string) => void;
  t: UIText;
  className?: string;
  disabled?: boolean;
  activeAgentIds?: ReadonlySet<string>;
}) {
  const selected = agents.find((agent) => agent.id === selectedAgentId) || null;
  const value = selected ? selected.id : '';
  const selectedTitle = selected
    ? [
        selected.name || selected.hostname || selected.machineId,
        selected.online ? t.online : t.offline,
        selected.version,
        selected.connectedAt ? new Date(selected.connectedAt * 1000).toLocaleString() : '',
      ].filter(Boolean).join(' · ')
    : t.noBridgeConnected;
  return (
    <label className={cn("relative inline-flex min-w-[180px] items-center", className)}>
      <Server className={cn("absolute left-2.5 h-3.5 w-3.5 pointer-events-none", selected && activeAgentIds?.has(selected.id) ? "text-emerald-500" : "text-muted-foreground")} />
      <select
        value={value}
        onChange={(event) => onSelect(event.target.value)}
        disabled={disabled || agents.length === 0}
        className="h-8 w-full rounded-lg border border-border bg-secondary/50 py-1 pl-8 pr-7 text-xs text-foreground shadow-sm outline-none focus:ring-1 focus:ring-ring disabled:opacity-60"
        aria-label={t.selectEndpoint}
        title={selectedTitle}
      >
        {!selected && agents.length > 0 && <option value="" disabled>{t.selectEndpoint}</option>}
        {agents.length ? (
          agents.map((agent) => (
            <option key={agent.id} value={agent.id}>
              {activeAgentIds?.has(agent.id) ? '● ' : agent.online ? '◉ ' : '○ '}{agent.name || agent.hostname || agent.machineId}{activeAgentIds?.has(agent.id) ? ` · ${t.running}` : agent.online && agent.version ? ` · ${agent.version}` : ''}
            </option>
          ))
        ) : (
          <option value="">{t.noBridgeConnected}</option>
        )}
      </select>
    </label>
  );
}
