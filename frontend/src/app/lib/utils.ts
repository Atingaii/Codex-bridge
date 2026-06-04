import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import { uiText, type UIText } from './i18n';
import type {
  Agent,
  ApprovalItemState,
  ApprovalRequest,
  ApprovalStatus,
  ChatItem,
  CommandData,
  ImageAttachment,
  OrchestrationEvent,
  OrchestrationFile,
  OrchestrationRun,
  OrchestrationTimelineGroup,
  OrchestrationTimelineItem,
  OrchestrationTurnInfo,
  OrchestrationVisibleEvent,
  PublicOrchestrationRun,
  Session,
  UploadAttachment,
} from './types';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function newID(prefix: string) {
  if (!window.crypto?.getRandomValues) {
    return `${prefix}_${Date.now().toString(16)}${Math.random().toString(16).slice(2)}`;
  }
  const random = window.crypto.getRandomValues(new Uint32Array(4));
  return `${prefix}_${Array.from(random, (part) => part.toString(16).padStart(8, '0')).join('')}`;
}

export function displaySessionTitle(session: Session | null | undefined, t: UIText = uiText.en) {
  if (!session?.title || session.title === 'New chat') return t.newSession;
  return session.title;
}

export function titleFromPrompt(prompt: string, t: UIText = uiText.en) {
  const compact = prompt.replace(/\s+/g, ' ').trim();
  if (!compact) return t.newSession;
  return compact.length > 48 ? `${compact.slice(0, 48)}...` : compact;
}

export function formatTime(timestamp?: number) {
  if (!timestamp) return '';
  const date = new Date(timestamp * 1000);
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function formatDuration(ms?: number) {
  if (!Number.isFinite(ms || NaN) || !ms || ms < 0) return '';
  const totalSeconds = Math.max(1, Math.floor(ms / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}h ${minutes}m ${seconds}s`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

export function sessionDateLabel(timestamp: number, t: UIText = uiText.en) {
  const date = new Date(timestamp * 1000);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const target = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const diffDays = Math.round((today.getTime() - target.getTime()) / 86400000);
  const calendarDate = date.toLocaleDateString([], { year: 'numeric', month: '2-digit', day: '2-digit' });
  if (diffDays <= 0) return `${t.today} · ${calendarDate}`;
  if (diffDays === 1) return `${t.yesterday} · ${calendarDate}`;
  return calendarDate;
}

export function initials(username: string) {
  return (username || 'CB')
    .split(/\s+/)
    .slice(0, 2)
    .map((part) => part[0])
    .join('')
    .toUpperCase();
}

export function activeStatus(status?: string) {
  return status === 'queued' || status === 'running' || status === 'canceling';
}

export function activeOrchestrationStatus(status?: string) {
  return status === 'queued' || status === 'running' || status === 'canceling';
}

export function terminalOrchestrationStatus(status?: string) {
  return status === 'completed' || status === 'failed' || status === 'canceled';
}

export const activeOrchestrationRunStorageKey = 'codexBridge.activeOrchestrationRunId';
export const activeOrchestrationRunByAgentStorageKey = 'codexBridge.activeOrchestrationRunByAgent';

export function readActiveOrchestrationRunByAgent(): Record<string, string> {
  try {
    const raw = localStorage.getItem(activeOrchestrationRunByAgentStorageKey);
    const parsed = raw ? JSON.parse(raw) : {};
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, string> : {};
  } catch {
    return {};
  }
}

export function rememberActiveOrchestrationRunForAgent(agentId: string, runId: string) {
  if (!agentId || !runId) return;
  const current = readActiveOrchestrationRunByAgent();
  current[agentId] = runId;
  localStorage.setItem(activeOrchestrationRunByAgentStorageKey, JSON.stringify(current));
}

export function forgetActiveOrchestrationRunForAgent(agentId: string, runId?: string) {
  if (!agentId) return;
  const current = readActiveOrchestrationRunByAgent();
  if (!runId || current[agentId] === runId) {
    delete current[agentId];
    localStorage.setItem(activeOrchestrationRunByAgentStorageKey, JSON.stringify(current));
  }
}

export function canCancelOrchestrationStatus(status?: string) {
  return status === 'queued' || status === 'running';
}

export function orchestrationRunStatusFromEvent(event: OrchestrationEvent) {
  switch (event.kind) {
    case 'run.start':
      return 'running';
    case 'run.end':
      return 'completed';
    case 'run.error':
      return 'failed';
    case 'run.cancelled':
      return 'canceled';
    case 'run.canceling':
      return 'canceling';
    default:
      return '';
  }
}

export function orchestrationEventKey(event: OrchestrationEvent, index = 0) {
  if (event.id) return `id:${event.id}`;
  if (event.seq && event.runId) return `seq:${event.runId}:${event.seq}`;
  return `fallback:${event.runId}:${event.kind}:${event.turnId || ''}:${event.role || ''}:${event.cli || ''}:${event.createdAt || ''}:${index}`;
}

export function compareOrchestrationEvents(a: OrchestrationEvent, b: OrchestrationEvent) {
  if (a.runId !== b.runId) return a.runId.localeCompare(b.runId);
  if (a.seq && b.seq && a.seq !== b.seq) return a.seq - b.seq;
  if (a.createdAt && b.createdAt && a.createdAt !== b.createdAt) return a.createdAt - b.createdAt;
  return orchestrationEventKey(a).localeCompare(orchestrationEventKey(b));
}

export function orchestrationEventSource(event: OrchestrationEvent) {
  if (event.source === 'cli' || event.source === 'bridge' || event.source === 'user') return event.source;
  if (event.kind === 'user.message') return 'user';
  if (event.kind === 'run.start' || event.kind === 'run.end' || event.kind === 'run.error' || event.kind === 'run.cancelled' || event.kind === 'run.canceling' || event.kind === 'run.conclusion' || event.kind === 'turn.start') return 'bridge';
  return 'cli';
}

export function isBridgeRelayNotice(event: Pick<OrchestrationEvent, 'kind' | 'source' | 'severity'>) {
  return orchestrationEventSource(event as OrchestrationEvent) === 'bridge' && (event.kind === 'turn.delta' || event.kind === 'run.conclusion' || Boolean(event.severity));
}

export function commandData(event: OrchestrationEvent): CommandData {
  return event.commandData || {};
}

export function bridgeConclusionContent(event: OrchestrationEvent) {
  return stringsTrim(event.runConclusion?.summary || event.content || event.error);
}

export function mergeOrchestrationEvents(current: OrchestrationEvent[], incoming: OrchestrationEvent[]) {
  const merged = new Map<string, OrchestrationEvent>();
  current.forEach((event, index) => merged.set(orchestrationEventKey(event, index), event));
  incoming.forEach((event, index) => {
    const key = orchestrationEventKey(event, current.length + index);
    const previous = merged.get(key);
    merged.set(key, previous ? {
      ...previous,
      ...event,
      commandData: event.commandData || previous.commandData,
      runStartData: event.runStartData || previous.runStartData,
      turnStartData: event.turnStartData || previous.turnStartData,
      runEndData: event.runEndData || previous.runEndData,
      bridgeNoteData: event.bridgeNoteData || previous.bridgeNoteData,
      runConclusion: event.runConclusion || previous.runConclusion,
      data: event.data || previous.data,
      timelineOrder: firstNumber(previous.timelineOrder, event.timelineOrder),
    } : event);
  });
  return Array.from(merged.values()).sort(compareOrchestrationEvents);
}

export function upsertOrchestrationRun(current: OrchestrationRun[], next: OrchestrationRun) {
  const found = current.some((run) => run.id === next.id);
  const runs = found ? current.map((run) => run.id === next.id ? { ...run, ...next } : run) : [next, ...current];
  return runs.slice().sort((a, b) => (b.updatedAt || b.createdAt || 0) - (a.updatedAt || a.createdAt || 0));
}

export function upsertApprovalItem(current: ApprovalItemState[], approval: ApprovalRequest, timelineOrder?: number): ApprovalItemState[] {
  const semanticKey = approvalSemanticKey(approval);
  const createdAt = Math.floor(Date.now() / 1000);
  const next: ApprovalItemState = { id: approval.requestId, approval, status: 'pending', timelineOrder, createdAt };
  let replaced = false;
  const updated = current.map((item) => {
    if (item.approval.requestId === approval.requestId) {
      replaced = true;
      return {
        ...item,
        approval,
        timelineOrder: firstNumber(item.timelineOrder, timelineOrder),
        createdAt: item.createdAt || createdAt,
      };
    }
    if (approvalSemanticKey(item.approval) === semanticKey) {
      replaced = true;
      return {
        ...next,
        status: item.status || next.status,
        timelineOrder: firstNumber(item.timelineOrder, timelineOrder),
        createdAt: item.createdAt || createdAt,
      };
    }
    return item;
  });
  return replaced ? updated : [...current, next];
}

export function updateApprovalItemStatus(current: ApprovalItemState[], requestId: string, status: ApprovalStatus): ApprovalItemState[] {
  return current.map((item) => item.approval.requestId === requestId ? { ...item, status } : item);
}

export function approvalSemanticKey(approval: ApprovalRequest) {
  const command = stringsTrim(approval.command).replace(/\s+/g, ' ');
  const cwd = stringsTrim(approval.cwd);
  const reason = stringsTrim(approval.reason).replace(/\s+/g, ' ');
  return [approval.runId || '', approval.turnId || approval.promptId || '', approval.kind || '', command, cwd, reason].join('\u001f');
}

export function approvalStatusFromDecision(decision: 'accept' | 'decline' | 'cancel'): ApprovalStatus {
  return decision === 'accept' ? 'accepted' : decision === 'decline' ? 'declined' : 'canceled';
}

function hasNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

function firstNumber(...values: Array<number | undefined>) {
  return values.find(hasNumber);
}

function minNumber(current?: number, next?: number) {
  if (!hasNumber(next)) return current;
  return hasNumber(current) ? Math.min(current, next) : next;
}

export function visibleEventTimelineOrder(event: OrchestrationVisibleEvent) {
  if (hasNumber(event.timelineOrder)) return event.timelineOrder;
  if (event.type === 'command') return event.command.timelineOrder;
  if (event.type === 'message' && event.commands.length > 0) {
    return event.commands.reduce<number | undefined>((best, command) => {
      return minNumber(best, command.timelineOrder);
    }, undefined);
  }
  return undefined;
}

export function compareOrchestrationTimelineItems(a: OrchestrationTimelineItem, b: OrchestrationTimelineItem) {
  if (hasNumber(a.timelineOrder) && hasNumber(b.timelineOrder) && a.timelineOrder !== b.timelineOrder) return a.timelineOrder - b.timelineOrder;
  if (hasNumber(a.timelineOrder) && !hasNumber(b.timelineOrder)) return 1;
  if (!hasNumber(a.timelineOrder) && hasNumber(b.timelineOrder)) return -1;
  if (hasNumber(a.createdAt) && hasNumber(b.createdAt) && a.createdAt !== b.createdAt) return a.createdAt - b.createdAt;
  if (a.sortIndex !== b.sortIndex) return a.sortIndex - b.sortIndex;
  return a.key.localeCompare(b.key);
}

export function orchestrationTimelineItems(events: OrchestrationVisibleEvent[], approvals: ApprovalItemState[]): OrchestrationTimelineItem[] {
  return [
    ...events.map((event, index) => ({
      type: 'event' as const,
      key: `event:${event.key}`,
      event,
      sortIndex: index,
      timelineOrder: visibleEventTimelineOrder(event),
      createdAt: event.createdAt,
    })),
    ...approvals.map((approval, index) => ({
      type: 'approval' as const,
      key: `approval:${approval.id}`,
      approval,
      sortIndex: events.length + index,
      timelineOrder: approval.timelineOrder,
      createdAt: approval.createdAt,
    })),
  ].sort(compareOrchestrationTimelineItems);
}

export function orchestrationTimelineGroups(
  items: OrchestrationTimelineItem[],
  run?: Pick<OrchestrationRun | PublicOrchestrationRun, 'id' | 'status' | 'maxTurns'> | null,
  events: OrchestrationEvent[] = [],
): OrchestrationTimelineGroup[] {
  const groups: OrchestrationTimelineGroup[] = [];
  const turnGroups = new Map<string, OrchestrationTimelineGroup>();
  const terminalRun = terminalOrchestrationStatus(run?.status);
  const activeRun = activeOrchestrationStatus(run?.status);
  const completeTurnKeys = completedOrchestrationTurnGroupKeys(events, run?.id);
  const terminalClosedTurnKeys = terminalClosedOrchestrationTurnGroupKeys(events, run?.id);

  const ensureTurnGroup = (item: OrchestrationTimelineItem, meta: { runId?: string; turnId?: string; role?: string; cli?: string }) => {
    const key = orchestrationTimelineTurnGroupKey(meta);
    let group = turnGroups.get(key);
    if (!group) {
      const turnInfo = parseOrchestrationTurnInfo(meta.turnId);
      group = {
        type: 'turn',
        key,
        runId: meta.runId,
        turnId: meta.turnId,
        role: meta.role,
        cli: meta.cli,
        turnInfo: typeof turnInfo.ordinal === 'number' && run?.maxTurns ? { ...turnInfo, total: run.maxTurns } : turnInfo,
        items: [],
        messageCount: 0,
        commandCount: 0,
        approvalCount: 0,
        statusCount: 0,
        createdAt: item.createdAt,
        timelineOrder: item.timelineOrder,
        complete: false,
        active: false,
        incomplete: false,
        hasError: false,
      };
      turnGroups.set(key, group);
      groups.push(group);
    } else {
      group.role = group.role || meta.role;
      group.cli = group.cli || meta.cli;
    }
    return group;
  };

  items.forEach((item) => {
    const meta = orchestrationTimelineItemTurnMeta(item);
    if (!meta.turnId) {
      groups.push(standaloneTimelineGroup(item));
      return;
    }
    const group = ensureTurnGroup(item, meta);
    addTimelineItemToGroup(group, item);
  });

  groups.forEach((group) => {
    if (group.type !== 'turn') return;
    group.items.sort(compareOrchestrationTimelineItems);
    const lastItem = group.items[group.items.length - 1];
    const lastEvent = lastItem?.type === 'event' ? lastItem.event : undefined;
    const activeCommand = group.items.some((item) => item.type === 'event' && timelineEventIsActiveCommand(item.event));
    const closedByTerminalRun = terminalClosedTurnKeys.has(group.key);
    group.complete = group.complete || completeTurnKeys.has(group.key) || closedByTerminalRun;
    if (closedByTerminalRun) group.hasError = true;
    group.active = activeCommand || (activeRun && Boolean(lastEvent && !group.complete && lastEvent.kind !== 'turn.end'));
    group.incomplete = terminalRun && !group.complete && group.items.length > 0;
    group.createdAt = group.items.reduce<number | undefined>((best, item) => {
      return minNumber(best, item.createdAt);
    }, group.createdAt);
    group.timelineOrder = group.items.reduce<number | undefined>((best, item) => {
      return minNumber(best, item.timelineOrder);
    }, group.timelineOrder);
  });

  return groups.sort(compareOrchestrationTimelineGroups);
}

function orchestrationTimelineTurnGroupKey(meta: { runId?: string; turnId?: string }) {
  return `turn:${meta.runId || ''}:${meta.turnId || ''}`;
}

function completedOrchestrationTurnGroupKeys(events: OrchestrationEvent[], runId?: string) {
  const keys = new Set<string>();
  events.forEach((event) => {
    if (event.kind !== 'turn.end' || !event.turnId) return;
    if (runId && event.runId !== runId) return;
    keys.add(orchestrationTimelineTurnGroupKey({ runId: event.runId, turnId: event.turnId }));
  });
  return keys;
}

function terminalClosedOrchestrationTurnGroupKeys(events: OrchestrationEvent[], runId?: string) {
  const keys = new Set<string>();
  const completed = completedOrchestrationTurnGroupKeys(events, runId);
  let latestTurn: { runId?: string; turnId?: string } | null = null;
  events
    .filter((event) => !runId || event.runId === runId)
    .slice()
    .sort(compareOrchestrationEvents)
    .forEach((event) => {
      if (event.turnId) {
        latestTurn = { runId: event.runId, turnId: event.turnId };
      }
      if ((event.kind !== 'run.error' && event.kind !== 'run.cancelled') || !latestTurn?.turnId) return;
      const key = orchestrationTimelineTurnGroupKey(latestTurn);
      if (!completed.has(key)) keys.add(key);
    });
  return keys;
}

function orchestrationTimelineItemTurnMeta(item: OrchestrationTimelineItem) {
  if (item.type === 'event') {
    return {
      runId: item.event.runId,
      turnId: item.event.turnId,
      role: item.event.role,
      cli: item.event.cli,
    };
  }
  return {
    runId: item.approval.approval.runId,
    turnId: item.approval.approval.turnId || item.approval.approval.promptId,
  };
}

function standaloneTimelineGroup(item: OrchestrationTimelineItem): OrchestrationTimelineGroup {
  const hasError = item.type === 'event' ? timelineEventHasError(item.event) : false;
  return {
    type: 'standalone',
    key: `standalone:${item.key}`,
    runId: item.type === 'event' ? item.event.runId : item.approval.approval.runId,
    items: [item],
    messageCount: item.type === 'event' && item.event.type === 'message' ? 1 : 0,
    commandCount: item.type === 'event' && item.event.type === 'command' ? 1 : 0,
    approvalCount: item.type === 'approval' ? 1 : 0,
    statusCount: item.type === 'event' && item.event.type === 'status' ? 1 : 0,
    createdAt: item.createdAt,
    timelineOrder: item.timelineOrder,
    complete: true,
    active: false,
    incomplete: false,
    hasError,
  };
}

function addTimelineItemToGroup(group: OrchestrationTimelineGroup, item: OrchestrationTimelineItem) {
  group.items.push(item);
  group.createdAt = minNumber(group.createdAt, item.createdAt);
  group.timelineOrder = minNumber(group.timelineOrder, item.timelineOrder);
  if (item.type === 'approval') {
    group.approvalCount += 1;
    if (!item.approval.status || item.approval.status === 'pending') group.active = true;
    return;
  }
  const event = item.event;
  if (event.type === 'message') group.messageCount += 1;
  if (event.type === 'command') group.commandCount += 1;
  if (event.type === 'status') group.statusCount += 1;
  if (event.kind === 'turn.end') group.complete = true;
  if (timelineEventHasError(event)) group.hasError = true;
  if (timelineEventIsActiveCommand(event)) group.active = true;
}

function timelineEventHasError(event: OrchestrationVisibleEvent) {
  return Boolean(event.error) || event.status === 'error' || event.status === 'failed' || (event.type === 'command' && commandEventFailed(event.command));
}

function timelineEventIsActiveCommand(event: OrchestrationVisibleEvent) {
  if (event.type !== 'command') return false;
  const data = commandData(event.command);
  const status = String(data.status || event.status || '').toLowerCase();
  return event.kind === 'command.start' || status === 'running' || status === 'in_progress';
}

function compareOrchestrationTimelineGroups(a: OrchestrationTimelineGroup, b: OrchestrationTimelineGroup) {
  if (hasNumber(a.timelineOrder) && hasNumber(b.timelineOrder) && a.timelineOrder !== b.timelineOrder) return a.timelineOrder - b.timelineOrder;
  if (hasNumber(a.timelineOrder) && !hasNumber(b.timelineOrder)) return 1;
  if (!hasNumber(a.timelineOrder) && hasNumber(b.timelineOrder)) return -1;
  if (hasNumber(a.createdAt) && hasNumber(b.createdAt) && a.createdAt !== b.createdAt) return a.createdAt - b.createdAt;
  return a.key.localeCompare(b.key);
}

export function orchestrationToolID(event: OrchestrationEvent) {
  return commandData(event).id || '';
}

export function mergeOrchestrationToolEvents(events: OrchestrationEvent[]): OrchestrationEvent[] {
  const merged: OrchestrationEvent[] = [];
  const toolIndexes = new Map<string, number>();
  events.forEach((event) => {
    const toolID = event.kind.startsWith('command.') ? orchestrationToolID(event) : '';
    if (!toolID) {
      merged.push(event);
      return;
    }
    const key = `${event.runId}:${event.turnId || ''}:${event.cli || ''}:${toolID}`;
    const index = toolIndexes.get(key);
    if (typeof index !== 'number') {
      toolIndexes.set(key, merged.length);
      merged.push(event);
      return;
    }
    const previous = merged[index];
    merged[index] = {
      ...previous,
      ...event,
      commandData: mergeCommandData(previous.commandData, event.commandData),
      data: event.data || previous.data,
      content: event.content || previous.content,
      error: event.error || previous.error,
      createdAt: event.createdAt || previous.createdAt,
      seq: event.seq || previous.seq,
      timelineOrder: firstNumber(previous.timelineOrder, event.timelineOrder),
    };
  });
  return merged;
}

export function mergeCommandData(previous?: CommandData, next?: CommandData): CommandData | undefined {
  if (!previous && !next) return undefined;
  const merged = { ...(previous || {}), ...(next || {}) };
  for (const field of ['command', 'input', 'name'] as const) {
    if (typeof merged[field] === 'string' && !merged[field]?.trim() && typeof previous?.[field] === 'string' && previous[field]?.trim()) {
      merged[field] = previous[field];
    }
  }
  return merged;
}

export function orchestrationTurnKey(event: OrchestrationEvent) {
  return `${event.runId}:${event.turnId || ''}:${event.role || ''}:${event.cli || ''}`;
}

export function parseOrchestrationTurnInfo(turnId?: string): OrchestrationTurnInfo {
  const value = String(turnId || '');
  if (!value) return {};
  if (/(?:^|-)verifier$/.test(value)) return { verifier: true };
  const match = value.match(/-(\d{2,})$/);
  if (!match) return {};
  const ordinal = Number(match[1]);
  return Number.isFinite(ordinal) && ordinal > 0 ? { ordinal } : {};
}

export function orchestrationTurnInfoFromEvents(events: OrchestrationEvent[], runId: string, maxTurns?: number, includeTotal = true): OrchestrationTurnInfo {
  let latest: OrchestrationEvent | null = null;
  for (const event of events) {
    if (event.runId !== runId || !event.turnId) continue;
    if (!latest || compareOrchestrationEvents(latest, event) <= 0) latest = event;
  }
  if (!latest) return {};
  const info = parseOrchestrationTurnInfo(latest.turnId);
  const turn = latest.turnStartData?.turn;
  const total = latest.turnStartData?.maxTurns || maxTurns;
  const ordinal = typeof turn === 'number' && turn > 0 ? turn : info.ordinal;
  if (typeof ordinal !== 'number') return info;
  if (includeTotal && total) {
    return { ...info, ordinal, total };
  }
  return { ...info, ordinal };
}

export function orchestrationTurnLabel(info: OrchestrationTurnInfo, t: UIText) {
  if (info.verifier) return t.verifierTurn;
  if (typeof info.ordinal !== 'number') return '';
  const suffix = info.total ? `/${info.total}` : '';
  if (t.turnPrefix === '第') return `${t.turnPrefix}${info.ordinal}${suffix}${t.turnSuffix}`;
  return `${t.turnPrefix} ${info.ordinal}${suffix}`;
}

export function visibleOrchestrationEvents(events: OrchestrationEvent[], runId: string, run?: OrchestrationRun | PublicOrchestrationRun | null, t?: UIText): OrchestrationVisibleEvent[] {
  const terminalRun = terminalOrchestrationStatus(run?.status);
  const ordered = mergeOrchestrationDeltaEvents(
    mergeOrchestrationToolEvents(events.filter((event) => event.runId === runId).slice().sort(compareOrchestrationEvents))
      .filter((event) => !isEmptyPagesReadFailureEvent(event))
      .map((event) => terminalRun ? finalizeTerminalCommandEvent(event, run?.status) : event)
  );
  const turnDeltaContent = orchestrationTurnDeltaContentByKey(ordered);
  const visible: OrchestrationVisibleEvent[] = [];
  let segmentVisibleStart = 0;

  ordered.forEach((event, index) => {
    if (event.kind === 'user.message') {
      const content = stringsTrim(event.content);
      if (!content) return;
      visible.push({
        type: 'message',
        key: orchestrationEventKey(event, index),
        runId: event.runId,
        kind: event.kind,
        role: event.role,
        cli: event.cli,
        turnId: event.turnId,
        content,
        status: event.status,
        error: event.error,
        createdAt: event.createdAt,
        timelineOrder: event.timelineOrder,
        files: orchestrationEventFiles(event),
        commands: [],
      });
      return;
    }

    if (event.kind === 'turn.delta') {
      const content = cleanOrchestrationDisplayContent(event.content);
      if (!content) return;
      visible.push({
        type: 'message',
        key: orchestrationEventKey(event, index),
        runId: event.runId,
        kind: event.kind,
        role: event.role,
        cli: event.cli,
        turnId: event.turnId,
        content,
        status: event.status,
        error: event.error,
        createdAt: event.createdAt,
        timelineOrder: event.timelineOrder,
        commands: [],
      });
      return;
    }

    if (event.kind.startsWith('command.')) {
      const content = orchestrationCommandSummary(event);
      visible.push({
        type: 'command',
        key: orchestrationEventKey(event, index),
        runId: event.runId,
        kind: event.kind,
        role: event.role,
        cli: event.cli,
        turnId: event.turnId,
        content,
        status: event.status,
        error: event.error,
        createdAt: event.createdAt,
        timelineOrder: event.timelineOrder,
        command: event,
      });
      if (event.status === 'error' || event.error) {
        visible.push(statusVisibleEvent(event, index, ':status'));
      }
      return;
    }

    if (event.kind === 'turn.end') {
      const content = turnEndDisplayContent(
        cleanOrchestrationDisplayContent(event.content),
        turnDeltaContent.get(orchestrationTurnKey(event)) || ''
      );
      if (content) {
        visible.push({
          type: 'message',
          key: orchestrationEventKey(event, index),
          runId: event.runId,
          kind: event.kind,
          role: event.role,
          cli: event.cli,
          turnId: event.turnId,
          content,
          status: event.status,
          error: event.error,
          createdAt: event.createdAt,
          timelineOrder: event.timelineOrder,
          commands: [],
        });
        if (shouldShowOrchestrationStatus(event)) {
          visible.push(statusVisibleEvent(event, index, ':status'));
        }
        return;
      }
      if (event.error) {
        visible.push(statusVisibleEvent(event, index));
      }
      return;
    }

    if (event.kind === 'run.conclusion') {
      const content = cleanOrchestrationDisplayContent(bridgeConclusionContent(event));
      if (content) {
        visible.push({
          type: 'message',
          key: orchestrationEventKey(event, index),
          runId: event.runId,
          kind: event.kind,
          role: 'summary',
          cli: event.cli,
          turnId: event.turnId,
          content,
          status: event.status,
          error: event.error,
          createdAt: event.createdAt,
          timelineOrder: event.timelineOrder,
          commands: [],
        });
      }
      return;
    }

    if (event.kind === 'run.end' || event.kind === 'run.error' || event.kind === 'run.cancelled') {
      if (!ordered.some((candidate) => candidate.kind === 'run.conclusion' && candidate.runId === event.runId)) {
        const content = cleanOrchestrationDisplayContent(orchestrationStatusContent(event));
        const hasEquivalentVisibleConclusion = visible
          .slice(segmentVisibleStart)
          .some((item) => item.type === 'message' && stringsTrim(item.content) === stringsTrim(content));
        if (content && !hasEquivalentVisibleConclusion) {
          visible.push({
            type: 'message',
            key: orchestrationEventKey(event, index),
            runId: event.runId,
            kind: event.kind,
            role: 'summary',
            cli: event.cli,
            turnId: event.turnId,
            content,
            status: event.status,
            error: event.error,
            createdAt: event.createdAt,
            timelineOrder: event.timelineOrder,
            commands: [],
          });
        } else if (shouldShowOrchestrationStatus(event)) {
          visible.push(statusVisibleEvent(event, index));
        }
      } else if (shouldShowOrchestrationStatus(event) && event.kind !== 'run.end') {
        visible.push(statusVisibleEvent(event, index));
      }
      segmentVisibleStart = visible.length;
      return;
    }

    if (shouldShowOrchestrationStatus(event)) {
      visible.push(statusVisibleEvent(event, index));
    }

  });
  return visible;
}

export function finalizeTerminalCommandEvent(event: OrchestrationEvent, runStatus?: string): OrchestrationEvent {
  if (!event.kind.startsWith('command.')) return event;
  const data = commandData(event);
  const status = data.status || event.status || '';
  const active = event.kind === 'command.start' || status === 'running' || status === 'in_progress';
  if (!active || typeof data.completedAt === 'number') return event;
  const terminalStatus = runStatus === 'canceled' ? 'canceled' : 'interrupted';
  return {
    ...event,
    kind: 'command.end',
    status: terminalStatus,
    commandData: {
      ...event.commandData,
      status: terminalStatus,
      completedAt: event.createdAt || Math.floor(Date.now() / 1000),
    },
  };
}

export function commandEventFailed(event: OrchestrationEvent) {
  const data = commandData(event);
  const status = String(data.status || event.status || '').toLowerCase();
  const exitCode = data.exitCode;
  return Boolean(event.error) || status === 'failed' || status === 'error' || (typeof exitCode === 'number' && exitCode !== 0);
}

export function mergeOrchestrationDeltaEvents(events: OrchestrationEvent[]): OrchestrationEvent[] {
  const merged: OrchestrationEvent[] = [];
  events.forEach((event) => {
    if (event.kind !== 'turn.delta') {
      merged.push(event);
      return;
    }
    const content = decodeEscapedLineBreaks(String(event.content || ''));
    if (!stringsTrim(content)) return;
    if (isBridgeRelayNotice(event)) {
      merged.push({ ...event, content });
      return;
    }
    const previous = merged[merged.length - 1];
    if (!canMergeAdjacentOrchestrationDelta(previous, event)) {
      merged.push({ ...event, content });
      return;
    }
    merged[merged.length - 1] = {
      ...previous,
      content: mergeDeltaContent(previous.content || '', content),
      status: event.status || previous.status,
      error: event.error || previous.error,
      createdAt: previous.createdAt || event.createdAt,
      seq: previous.seq || event.seq,
      timelineOrder: firstNumber(previous.timelineOrder, event.timelineOrder),
    };
  });
  return merged;
}

export function canMergeAdjacentOrchestrationDelta(previous: OrchestrationEvent | undefined, event: OrchestrationEvent) {
  if (!previous || previous.kind !== 'turn.delta') return false;
  return orchestrationTurnKey(previous) === orchestrationTurnKey(event)
    && isBridgeRelayNotice(previous) === isBridgeRelayNotice(event);
}

export function mergeDeltaContent(previous: string, next: string) {
  if (!previous) return next;
  if (!next) return previous;
  if (next.startsWith(previous)) return next;
  if (previous.endsWith(next)) return previous;
  return previous + next;
}

export function orchestrationTurnDeltaContentByKey(events: OrchestrationEvent[]) {
  const out = new Map<string, string>();
  events.forEach((event) => {
    if (event.kind !== 'turn.delta' || isBridgeRelayNotice(event)) return;
    const content = cleanOrchestrationDisplayContent(event.content);
    if (!content) return;
    const key = orchestrationTurnKey(event);
    out.set(key, mergeDeltaContent(out.get(key) || '', content));
  });
  return out;
}

export function turnEndDisplayContent(content: string, deltaContent: string) {
  const value = stringsTrim(content);
  const deltas = stringsTrim(deltaContent);
  if (!value || !deltas) return value;
  return '';
}

export function cleanOrchestrationDisplayContent(content?: string) {
  const value = stringsTrim(stripMachineContractLines(content));
  if (!value) return '';
  const index = conclusionDisplayTrimIndex(value);
  return index > 0 && shouldTrimConclusionDisplayPrefix(value.slice(0, index)) ? value.slice(index).trim() : value;
}

export function conclusionDisplayTrimIndex(value: string) {
  const lower = value.toLowerCase();
  const markerGroups = [
    ['最终结论', '最终总结', 'final conclusion', 'final summary'],
    ['审查结论', '本轮结论', '结论：', '结论:', 'conclusion:', 'summary:'],
  ];
  for (const markers of markerGroups) {
    let best = -1;
    markers.forEach((marker) => {
      const index = lower.lastIndexOf(marker.toLowerCase());
      if (index > best) best = index;
    });
    if (best >= 0) return best;
  }
  return -1;
}

export function shouldTrimConclusionDisplayPrefix(prefix: string) {
  const value = stringsTrim(prefix).toLowerCase();
  if (!value) return false;
  const signals = ['我会', '我先', '我将', '接下来', '正在', '不展开新的', 'i will', "i'll", 'i am going to', 'next i'];
  const count = signals.reduce((sum, signal) => sum + value.split(signal).length - 1, 0);
  return count >= 2 || value.startsWith('我会') || value.startsWith('我先') || Array.from(value).length > 240;
}

export function stringsTrim(value?: string) {
  return decodeEscapedLineBreaks(String(value || '')).trim();
}

export function stripMachineContractLines(content?: string) {
  const value = decodeEscapedLineBreaks(String(content || ''));
  if (!value) return '';
  let changed = false;
  const lines = value.split(/\r?\n/).filter((line) => {
    const remove = isMachineContractLine(line);
    if (remove) changed = true;
    return !remove;
  });
  return changed ? lines.join('\n').trim() : value;
}

export function isMachineContractLine(line: string) {
  const value = line.trim();
  return /^Msg:\s*to=[^;]+;\s*intent=[^;]+;\s*need=/i.test(value)
    || /^Handoff:\s*status=(needs_next|blocked|resolved)\b/i.test(value);
}

export function orchestrationEventFiles(event: OrchestrationEvent): OrchestrationFile[] {
  const raw = event.data?.files;
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item) => {
    if (!item || typeof item !== 'object') return [];
    const record = item as Record<string, unknown>;
    const name = typeof record.name === 'string' ? record.name.trim() : '';
    if (!name) return [];
    const mimeType = typeof record.mimeType === 'string' ? record.mimeType.trim() : '';
    const parsedSize = Number(record.size);
    const size = Number.isFinite(parsedSize) && parsedSize > 0 ? parsedSize : 0;
    return [{ name, mimeType, size }];
  });
}

export function mergeOrchestrationFiles(...groups: Array<OrchestrationFile[] | undefined>): OrchestrationFile[] {
  const seen = new Set<string>();
  const out: OrchestrationFile[] = [];
  groups.forEach((group) => {
    (group || []).forEach((file) => {
      const name = stringsTrim(file.name);
      if (!name) return;
      const mimeType = stringsTrim(file.mimeType);
      const size = Number.isFinite(Number(file.size)) && Number(file.size) > 0 ? Number(file.size) : 0;
      const key = `${name}\u001f${mimeType}\u001f${size}`;
      if (seen.has(key)) return;
      seen.add(key);
      out.push({ name, mimeType, size });
    });
  });
  return out;
}

export function orchestrationRunFilesFromEvents(events: OrchestrationEvent[], runId: string): OrchestrationFile[] {
  return mergeOrchestrationFiles(
    ...events
      .filter((event) => event.runId === runId && event.kind === 'user.message')
      .map(orchestrationEventFiles)
  );
}

export function decodeEscapedLineBreaks(value: string) {
  if (/[\r\n]/.test(value)) return value;
  const escapedBreaks = value.match(/\\r\\n|\\n|\\r/g);
  if (!escapedBreaks || escapedBreaks.length < 2) return value;
  return value
    .replace(/\\r\\n/g, '\n')
    .replace(/\\n/g, '\n')
    .replace(/\\r/g, '\n')
    .replace(/\\t/g, '\t');
}

export function orchestrationCommandSummary(event: OrchestrationEvent) {
  const data = commandData(event);
  const command = stringsTrim(data.command);
  const output = stringsTrim(data.output);
  const fallback = stringsTrim(event.error || event.content || data.status || event.status || event.kind);
  if (command && output) return `${command}\n\n${output}`;
  return command || output || fallback;
}

export function isEmptyPagesReadFailureEvent(event: OrchestrationEvent) {
  if (!event.kind.startsWith('command.')) return false;
  const data = commandData(event);
  const command = stringsTrim(data.command);
  const output = data.output || '';
  const status = data.status || event.status || '';
  return (
    status.toLowerCase() === 'failed' &&
    command.startsWith('Read ') &&
    output.includes('Invalid pages parameter: ""') &&
    output.includes('Pages are 1-indexed')
  );
}

export function shouldShowOrchestrationStatus(event: OrchestrationEvent) {
  if (event.kind === 'run.start' || event.kind === 'turn.start') return true;
  if (event.kind === 'run.conclusion') return false;
  if (event.kind === 'run.end') {
    const content = stringsTrim(event.content);
    return Boolean(event.error || (content && content !== 'Orchestration completed.'));
  }
  return event.kind === 'run.error' || event.kind === 'run.cancelled' || event.kind === 'run.canceling' || Boolean(event.error);
}

export function statusVisibleEvent(event: OrchestrationEvent, index: number, keySuffix = ''): OrchestrationVisibleEvent {
  return {
    type: 'status',
    key: `${orchestrationEventKey(event, index)}${keySuffix}`,
    runId: event.runId,
    kind: event.kind,
    role: event.role,
    cli: event.cli,
    turnId: event.turnId,
    content: orchestrationStatusContent(event),
    status: event.status,
    error: event.error,
    createdAt: event.createdAt,
    timelineOrder: event.timelineOrder,
  };
}

export function orchestrationStatusContent(event: OrchestrationEvent) {
  if (event.kind === 'run.conclusion') return bridgeConclusionContent(event);
  const content = stringsTrim(event.content);
  const error = stringsTrim(event.error);
  if ((event.kind === 'run.end' || event.kind === 'run.error') && content) return content;
  return error || content || stringsTrim(event.status) || event.kind;
}

export function applyOrchestrationEventToRun(run: OrchestrationRun, event: OrchestrationEvent): OrchestrationRun {
  const status = orchestrationRunStatusFromEvent(event);
  const updatedAt = Math.max(run.updatedAt || 0, event.createdAt || Math.floor(Date.now() / 1000));
  const next: OrchestrationRun = {
    ...run,
    updatedAt,
    error: event.error || run.error,
  };
  if (status) {
    next.status = status;
    if (!next.finishedAt && !activeOrchestrationStatus(status)) {
      next.finishedAt = event.createdAt || updatedAt;
    }
  }
  return next;
}

export function isNearBottom(element: HTMLElement, threshold = 120) {
  return element.scrollHeight - element.scrollTop - element.clientHeight <= threshold;
}

export async function copyText(value: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return true;
    } catch {
      // Fall back for WebViews or browsers that expose clipboard but deny it.
    }
  }
  const textarea = document.createElement('textarea');
  textarea.value = value;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.top = '-9999px';
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand('copy');
  document.body.removeChild(textarea);
  return copied;
}

export function waitForOpen(ws: WebSocket, timeout = 3000) {
  if (ws.readyState === WebSocket.OPEN) return Promise.resolve();
  if (ws.readyState === WebSocket.CLOSING || ws.readyState === WebSocket.CLOSED) {
    return Promise.reject(new Error('WebSocket is disconnected'));
  }
  return new Promise<void>((resolve, reject) => {
    const timer = window.setTimeout(() => {
      cleanup();
      reject(new Error('WebSocket connection timed out'));
    }, timeout);
    const cleanup = () => {
      window.clearTimeout(timer);
      ws.removeEventListener('open', handleOpen);
      ws.removeEventListener('error', handleError);
      ws.removeEventListener('close', handleClose);
    };
    const handleOpen = () => {
      cleanup();
      resolve();
    };
    const handleError = () => {
      cleanup();
      reject(new Error('WebSocket connection failed'));
    };
    const handleClose = () => {
      cleanup();
      reject(new Error('WebSocket is disconnected'));
    };
    ws.addEventListener('open', handleOpen);
    ws.addEventListener('error', handleError);
    ws.addEventListener('close', handleClose);
  });
}

export function startWSHeartbeat(ws: WebSocket, sid?: string) {
  const send = () => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'heartbeat', sid, payload: { ts: Date.now() } }));
    }
  };
  send();
  const id = window.setInterval(send, 15000);
  return () => window.clearInterval(id);
}

export function defaultAgentID(agents: Agent[]) {
  return (agents.find((agent) => agent.online) || agents[0])?.id || '';
}

export function preferredAgentID(agents: Agent[], current: string) {
  const selected = agents.find((agent) => agent.id === current);
  if (selected?.online) return selected.id;
  return defaultAgentID(agents) || selected?.id || '';
}

export function orchestrationApprovalMode(agent?: Agent | null) {
  const caps = agent?.capabilities;
  if (!caps) return agent?.online ? 'unknown' : 'offline';
  if (caps.approvalPolicy === 'never' && caps.sandbox === 'danger-full-access') return 'auto-execute';
  if (caps.metadata?.approvalMode === 'auto-execute') return 'auto-execute';
  return 'review-required';
}

export function orchestrationWorkerLabel(agent: Agent | null | undefined, t: UIText) {
  return t.manualOrchestration;
}

export function orchestrationCapability(agent: Agent | null | undefined, cli: 'claude' | 'codex') {
  return agent?.capabilities?.orchestration?.[cli];
}

export function orchestrationCapabilityProblems(agent: Agent | null | undefined, t: UIText) {
  if (!agent) return [t.noBridgeConnected];
  if (!agent.online) return [t.agentOffline];
  if (orchestrationApprovalMode(agent) !== 'review-required') return [];
  const problems: string[] = [];
  if (!orchestrationCapability(agent, 'claude')?.browserApproval) problems.push(t.claudeOrchestrationApprovalMissing);
  if (!orchestrationCapability(agent, 'codex')?.browserApproval) problems.push(t.codexOrchestrationApprovalMissing);
  return problems;
}

export const activeSessionByAgentStorageKey = 'codexBridge.activeSessionByAgent';

export function readActiveSessionByAgent(): Record<string, string> {
  try {
    const raw = localStorage.getItem(activeSessionByAgentStorageKey);
    const parsed = raw ? JSON.parse(raw) : {};
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, string> : {};
  } catch {
    return {};
  }
}

export function rememberActiveSessionForAgent(agentId: string, sessionId: string) {
  if (!agentId || !sessionId) return;
  const current = readActiveSessionByAgent();
  current[agentId] = sessionId;
  localStorage.setItem(activeSessionByAgentStorageKey, JSON.stringify(current));
}

export function forgetActiveSessionForAgent(agentId: string, sessionId?: string) {
  if (!agentId) return;
  const current = readActiveSessionByAgent();
  if (!sessionId || current[agentId] === sessionId) {
    delete current[agentId];
    localStorage.setItem(activeSessionByAgentStorageKey, JSON.stringify(current));
  }
}

export function agentStatusClass(agent?: Agent) {
  return cn("h-2 w-2 rounded-full", agent?.online ? "bg-emerald-500" : "bg-muted-foreground");
}

export function escapeBasic(value: string) {
  return value.replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  })[ch] || ch);
}

export function renderInlineMarkdown(text: string) {
  return escapeBasic(text)
    .replace(/!\[([^\]]*)\]\((blob:[^)]+|data:image\/[^)]+|https?:\/\/[^)]+)\)/g, '<img alt="$1" src="$2" class="mt-2 max-h-64 rounded-lg border border-border object-contain" />')
    .replace(/`([^`]+)`/g, '<code class="px-1 py-0.5 rounded bg-muted font-mono text-[0.92em]">$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
}

export function readImageAttachment(file: File): Promise<ImageAttachment> {
  return new Promise((resolve, reject) => {
    if (!file.type.startsWith('image/')) {
      reject(new Error('Only image files can be uploaded'));
      return;
    }
    if (file.size > 8 * 1024 * 1024) {
      reject(new Error('Image must be 8 MB or smaller'));
      return;
    }
    const reader = new FileReader();
    reader.onerror = () => reject(new Error('Failed to read image'));
    reader.onload = () => {
      const value = String(reader.result || '');
      const comma = value.indexOf(',');
      resolve({
        id: newID('att'),
        name: file.name,
        mimeType: file.type,
        size: file.size,
        data: comma === -1 ? value : value.slice(comma + 1),
        previewUrl: URL.createObjectURL(file),
      });
    };
    reader.readAsDataURL(file);
  });
}

export function readUploadAttachment(file: File): Promise<UploadAttachment> {
  return new Promise((resolve, reject) => {
    if (file.size > 8 * 1024 * 1024) {
      reject(new Error('Each file must be 8 MB or smaller'));
      return;
    }
    const reader = new FileReader();
    reader.onerror = () => reject(new Error('Failed to read file'));
    reader.onload = () => {
      const value = String(reader.result || '');
      const comma = value.indexOf(',');
      resolve({
        id: newID('file'),
        name: file.name,
        mimeType: inferredUploadMimeType(file),
        size: file.size,
        data: comma === -1 ? value : value.slice(comma + 1),
      });
    };
    reader.readAsDataURL(file);
  });
}

export function formatBytes(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

export const orchestrationArchiveMimeTypes = new Set([
  'application/zip',
  'application/x-zip-compressed',
  'application/x-tar',
  'application/gzip',
  'application/x-gzip',
  'application/x-bzip2',
  'application/x-xz',
  'application/x-7z-compressed',
  'application/vnd.rar',
  'application/x-rar-compressed',
]);

export const orchestrationArchiveExtensions = [
  '.zip',
  '.tar',
  '.tar.gz',
  '.tgz',
  '.gz',
  '.bz2',
  '.xz',
  '.7z',
  '.rar',
];

export function inferredUploadMimeType(file: File) {
  const browserType = stringsTrim(file.type);
  if (browserType && browserType !== 'application/octet-stream') return browserType;
  const name = file.name.toLowerCase();
  if (name.endsWith('.tar.gz') || name.endsWith('.tgz')) return 'application/gzip';
  if (name.endsWith('.zip')) return 'application/zip';
  if (name.endsWith('.tar')) return 'application/x-tar';
  if (name.endsWith('.gz')) return 'application/gzip';
  if (name.endsWith('.bz2')) return 'application/x-bzip2';
  if (name.endsWith('.xz')) return 'application/x-xz';
  if (name.endsWith('.7z')) return 'application/x-7z-compressed';
  if (name.endsWith('.rar')) return 'application/vnd.rar';
  return browserType || 'application/octet-stream';
}

export function isArchiveUpload(file: Pick<OrchestrationFile, 'name' | 'mimeType'>) {
  const mimeType = stringsTrim(file.mimeType).toLowerCase();
  const name = file.name.toLowerCase();
  return orchestrationArchiveMimeTypes.has(mimeType) || orchestrationArchiveExtensions.some((ext) => name.endsWith(ext));
}
