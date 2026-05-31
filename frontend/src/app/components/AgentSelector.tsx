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
}: {
  agents: Agent[];
  selectedAgentId: string;
  onSelect: (agentId: string) => void;
  t: UIText;
  className?: string;
  disabled?: boolean;
}) {
  const selected = agents.find((agent) => agent.id === selectedAgentId) || null;
  const value = selected ? selected.id : '';
  return (
    <label className={cn("relative inline-flex min-w-[180px] items-center", className)}>
      <Server className="absolute left-2.5 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
      <select
        value={value}
        onChange={(event) => onSelect(event.target.value)}
        disabled={disabled || agents.length === 0}
        className="h-8 w-full rounded-lg border border-border bg-secondary/50 py-1 pl-8 pr-7 text-xs text-foreground shadow-sm outline-none focus:ring-1 focus:ring-ring disabled:opacity-60"
        aria-label={t.selectEndpoint}
        title={selected?.name || t.noBridgeConnected}
      >
        {!selected && agents.length > 0 && <option value="" disabled>{t.selectEndpoint}</option>}
        {agents.length ? (
          agents.map((agent) => (
            <option key={agent.id} value={agent.id}>
              {agent.online ? '● ' : '○ '}{agent.name || agent.hostname || agent.machineId}
            </option>
          ))
        ) : (
          <option value="">{t.noBridgeConnected}</option>
        )}
      </select>
    </label>
  );
}
