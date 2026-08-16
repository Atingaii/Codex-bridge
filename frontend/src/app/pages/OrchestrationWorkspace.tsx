import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  AlertCircle,
  ArrowDownToLine,
  ArrowLeft,
  BookOpen,
  Check,
  Command,
  FileUp,
  FolderInput,
  GitBranch,
  History,
  Maximize2,
  Plus,
  RefreshCw,
  Send,
  Server,
  Settings,
  Share2,
  ShieldCheck,
  ShieldQuestion,
  Square,
  BarChart3,
  PieChart,
  Swords,
  Trash2,
  UsersRound,
  Workflow,
} from 'lucide-react';
import { api } from '../lib/api';
import type {
  Agent,
  ApprovalItemState,
  ApprovalRequest,
  CLIConfigPreset,
  Envelope,
  NativeContextCompaction,
  OrchestrationEvent,
  OrchestrationProgress,
  OrchestrationRun,
  ShareInfo,
  UploadAttachment,
  UserAccount,
  WorkerPair,
} from '../lib/types';
import type { Language, UIText } from '../lib/i18n';
import { AgentSelector } from '../components/AgentSelector';
import { OrchestrationFileRow } from '../components/OrchestrationFiles';
import { OrchestrationProgressMap } from '../components/OrchestrationProgressMap';
import { OrchestrationProofProgress } from '../components/OrchestrationProofProgress';
import {
  CapabilityMatrix,
  RunConclusionCard,
  OrchestrationTimelineGroupItem,
  defaultCollapsedTimelineGroups,
  reconcileCollapsedTimelineGroups,
} from '../components/OrchestrationComponents';
import { SettingsModal } from '../components/Settings';
import { Button, Input } from '../components/ui';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../components/ui/dialog';
import {
  activeOrchestrationRunStorageKey,
  activeOrchestrationStatus,
  applyOrchestrationEventToRun,
  approvalStatusFromDecision,
  canCancelOrchestrationStatus,
  compareOrchestrationEvents,
  cn,
  copyText,
  isNearBottom,
  isOrchestrationRun,
  mergeOrchestrationEvents,
  mergeOrchestrationFiles,
  normalizeOrchestrationWorkerPair,
  orchestrationApprovalMode,
  orchestrationCapabilityProblems,
  orchestrationRunFilesFromEvents,
  orchestrationTimelineGroups,
  orchestrationTimelineItems,
  orchestrationTurnInfoFromEvents,
  orchestrationTurnLabel,
  orchestrationWorkerLabel,
  preferredAgentID,
  readUploadAttachment,
  forgetActiveOrchestrationRunForAgent,
  rememberActiveOrchestrationRunForAgent,
  sessionDateLabel,
  formatDuration,
  startWSHeartbeat,
  titleFromPrompt,
  updateApprovalItemStatus,
  upsertApprovalItem,
  upsertOrchestrationRun,
  visibleOrchestrationEvents,
} from '../lib/utils';

const orchestrationEventPageSize = 300;

type OrchestrationEventsPage = {
  events: OrchestrationEvent[];
  hasMoreBefore?: boolean;
};

function selectedWorkerSlotValues(slots: Array<{ slot: string }>, values: Record<string, string>) {
  return Object.fromEntries(slots.flatMap(({ slot }) => {
    const value = values[slot]?.trim();
    return value ? [[slot, value]] : [];
  }));
}

export function OrchestrationWorkspace({
  user,
  onLogout,
  isDarkMode,
  setIsDarkMode,
  language,
  setLanguage,
  t,
  canOpenMain,
  path,
  navigate,
}: {
  user: UserAccount;
  onLogout: () => void;
  isDarkMode: boolean;
  setIsDarkMode: (value: boolean) => void;
  language: Language;
  setLanguage: (value: Language) => void;
  t: UIText;
  canOpenMain: boolean;
  path: string;
  navigate: (path: string, options?: { replace?: boolean }) => void;
}) {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentsLoaded, setAgentsLoaded] = useState(false);
  const [selectedAgentId, setSelectedAgentId] = useState(() => localStorage.getItem('codexBridge.selectedAgentId') || '');
  const [runs, setRuns] = useState<OrchestrationRun[]>([]);
  const [activeRunId, setActiveRunId] = useState('');
  const [events, setEvents] = useState<OrchestrationEvent[]>([]);
  const [approvals, setApprovals] = useState<ApprovalItemState[]>([]);
  const [mode, setMode] = useState<'collaboration' | 'debate'>('collaboration');
  const [workerPair, setWorkerPair] = useState<WorkerPair>('claude-codex');
  const [presets, setPresets] = useState<CLIConfigPreset[]>([]);
  const [selectedWorkerProfiles, setSelectedWorkerProfiles] = useState<Record<string, string>>({});
  const [selectedWorkerEfforts, setSelectedWorkerEfforts] = useState<Record<string, string>>({});
  const [workerProfilesTouched, setWorkerProfilesTouched] = useState(false);
  const [firstCli, setFirstCli] = useState<'claude' | 'codex'>('claude');
  const [profile, setProfile] = useState<'default' | 'formal-proof'>('default');
  const [nativeContextCompaction, setNativeContextCompaction] = useState<NativeContextCompaction>('off');
  const [prompt, setPrompt] = useState('');
  const [cwd, setCwd] = useState('');
  const [maxTurns, setMaxTurns] = useState(2);
  const [files, setFiles] = useState<UploadAttachment[]>([]);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsFocus, setSettingsFocus] = useState<'cli' | ''>('');
  const [error, setError] = useState('');
  const [sharingRunId, setSharingRunId] = useState('');
  const [shareCopiedRunId, setShareCopiedRunId] = useState('');
  const [creating, setCreating] = useState(false);
  const [deletingRunId, setDeletingRunId] = useState('');
  const [planningWorkspaceOpen, setPlanningWorkspaceOpen] = useState(false);
  const [orchestrationProgress, setOrchestrationProgress] = useState<OrchestrationProgress | null>(null);
  const [selectedProgressTaskNumber, setSelectedProgressTaskNumber] = useState<number | null>(null);
  const [selectedProgressGraphId, setSelectedProgressGraphId] = useState('');
  const [taskMapExpanded, setTaskMapExpanded] = useState(false);
  const [agentGraphExpanded, setAgentGraphExpanded] = useState(false);
  const [selectedPromptKey, setSelectedPromptKey] = useState('initial');
  const [connectionStatus, setConnectionStatus] = useState(t.disconnected);
  const [showScrollBottom, setShowScrollBottom] = useState(false);
  const [collapsedTimelineGroups, setCollapsedTimelineGroups] = useState<Record<string, boolean>>({});
  const [refreshingOrchestration, setRefreshingOrchestration] = useState(false);
  const [loadingOlderEvents, setLoadingOlderEvents] = useState(false);
  const [hasOlderEventsByRun, setHasOlderEventsByRun] = useState<Record<string, boolean>>({});
  const [elapsedTick, setElapsedTick] = useState(0);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const activeRunIdRef = useRef('');
  const desiredRunIdRef = useRef('');
  const activeRunAgentIdRef = useRef('');
  const selectedAgentIdRef = useRef(selectedAgentId);
  const stickToBottomRef = useRef(true);
  const timelineOrderRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const taskInputRef = useRef<HTMLTextAreaElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const endRef = useRef<HTMLDivElement | null>(null);
  const collapsedTimelineRunIdRef = useRef('');
  const refreshOrchestrationInFlightRef = useRef<Promise<void> | null>(null);
  const olderEventsLoadInFlightRef = useRef(false);
  const pendingLiveEventsRef = useRef<OrchestrationEvent[]>([]);
  const liveEventFrameRef = useRef<number | null>(null);
  const eventMaxSeqByRunRef = useRef<Record<string, number>>({});
  const eventMinSeqByRunRef = useRef<Record<string, number>>({});
  const orchestrationBootedRef = useRef(false);
  const agentSelectionEpochRef = useRef(0);
  const draftingRunRef = useRef(false);
  const pathRunId = useMemo(() => orchestrationRunIdFromPath(path), [path]);

  const selectedAgent = agents.find((agent) => agent.id === selectedAgentId) || null;
  const activeAgentIds = useMemo(
    () => new Set(runs.filter((run) => activeOrchestrationStatus(run.status)).map((run) => run.agentId)),
    [runs],
  );
  const onlineAgent = selectedAgent?.online ? selectedAgent : agents.find((agent) => agent.online);
  // Keep the global run cache for URL selection, but isolate the sidebar by machine.
  const agentRuns = useMemo(() => {
    if (!selectedAgentId) return [];
    return runs.filter((run) => run.agentId === selectedAgentId);
  }, [runs, selectedAgentId]);
  const activeRun = runs.find((run) => run.id === activeRunId) || null;
  const planWorkspaceEnabled = Boolean(user.features?.includes('orchestration-plan-workspace'));
  const activeRunFiles = useMemo(() => {
    return activeRun ? mergeOrchestrationFiles(activeRun.files, orchestrationRunFilesFromEvents(events, activeRun.id)) : [];
  }, [activeRun, events]);
  const visibleEvents = useMemo(() => activeRun ? visibleOrchestrationEvents(events, activeRunId, activeRun, t).filter((event) => event.kind !== 'run.conclusion') : [], [activeRun, events, activeRunId, t]);
  const finalConclusion = useMemo(() => events.slice().reverse().find((event) => event.runId === activeRunId && Boolean(event.runConclusion) && (event.kind === 'run.conclusion' || event.kind === 'run.end' || event.kind === 'run.error' || event.kind === 'run.cancelled'))?.runConclusion || null, [events, activeRunId]);
  const isRunning = activeOrchestrationStatus(activeRun?.status);
  const currentTurnInfo = useMemo(() => activeRun ? orchestrationTurnInfoFromEvents(events, activeRun.id, activeRun.maxTurns, isRunning) : {}, [activeRun, events, isRunning]);
  const currentTurnLabel = useMemo(() => orchestrationTurnLabel(currentTurnInfo, t), [currentTurnInfo, t]);
  const visibleApprovals = useMemo(() => approvals.filter((item) => item.approval.runId === activeRunId), [approvals, activeRunId]);
  const timelineItems = useMemo(() => orchestrationTimelineItems(visibleEvents, visibleApprovals), [visibleEvents, visibleApprovals]);
  const timelineGroups = useMemo(() => orchestrationTimelineGroups(timelineItems, activeRun, events), [timelineItems, activeRun, events, elapsedTick]);
  const progressTasks = orchestrationProgress?.tasks || [];
  const selectedProgressTask = useMemo(() => {
    if (!progressTasks.length) return null;
    return progressTasks.find((item) => item.taskNumber === selectedProgressTaskNumber) || progressTasks[progressTasks.length - 1];
  }, [progressTasks, selectedProgressTaskNumber]);
  const selectedProgressGraphIDs = useMemo(() => new Set((selectedProgressTask?.graphs || []).map((graph) => graph.id)), [selectedProgressTask]);
  const promptViews = useMemo(() => {
    if (!activeRun) return [];
    const taskNumber = selectedProgressTask?.taskNumber;
    const taskPrompt = selectedProgressTask?.prompt || activeRun.prompt;
    const views = [{ key: `task-${taskNumber || 1}`, label: taskNumber ? `${language === 'zh' ? '任务' : 'Task'} ${taskNumber}` : t.initialPrompt, content: taskPrompt }];
    events.forEach((event) => {
      const content = event.turnStartData?.promptText?.trim();
      if (!content) return;
      if (selectedProgressGraphIDs.size && event.task?.graphId && !selectedProgressGraphIDs.has(event.task.graphId)) return;
      if (selectedProgressGraphIDs.size && !event.task?.graphId && selectedProgressTask && event.createdAt) {
        const taskStart = selectedProgressTask.createdAt || 0;
        const taskEnd = selectedProgressTask.finishedAt || 0;
        if ((taskStart && event.createdAt < taskStart) || (taskEnd && event.createdAt > taskEnd)) return;
      }
      const key = `turn-${event.id || event.seq || views.length}`;
      const round = event.task?.round || event.turnStartData?.round;
      const node = event.task?.name || event.role || event.cli;
      const label = [round ? `${t.turnPrefix} ${round}` : t.roundPrompt, node].filter(Boolean).join(' · ');
      views.push({ key, label, content });
    });
    return views;
  }, [activeRun, events, language, selectedProgressGraphIDs, selectedProgressTask, t.initialPrompt, t.roundPrompt, t.turnPrefix]);
  const selectedPromptView = promptViews.find((item) => item.key === selectedPromptKey) || promptViews[0];
  const projectedPlan = selectedProgressTask?.plan || orchestrationProgress?.plan;
  const projectedPlanItems = projectedPlan?.items?.length ? projectedPlan.items : (selectedProgressTask?.planItems || orchestrationProgress?.planItems || []);
  const selectedAgentGraph = useMemo(() => {
    const graphs = selectedProgressTask?.graphs || [];
    return graphs.find((graph) => graph.id === selectedProgressGraphId) || selectedProgressTask?.graph || graphs[graphs.length - 1] || orchestrationProgress?.graph;
  }, [orchestrationProgress?.graph, selectedProgressGraphId, selectedProgressTask]);
  const currentPlanItem = projectedPlanItems.find((item) => item.id === projectedPlan?.currentFocus);
  const progressRefreshKey = useMemo(() => {
    const relevant = events.filter((event) => event.task && ['run.start', 'turn.end', 'run.end', 'run.error', 'run.cancelled'].includes(event.kind));
    const latest = relevant[relevant.length - 1];
    return latest ? `${latest.seq || latest.id || relevant.length}:${latest.kind}` : '';
  }, [events]);
  const orchestrationStreamStatus = activeRun && isRunning ? connectionStatus : t.idle;
  const continuingRun = Boolean(activeRun && !isRunning);
  const canCancelRun = canCancelOrchestrationStatus(activeRun?.status);
  const hasOlderEvents = Boolean(activeRunId && hasOlderEventsByRun[activeRunId]);
  const capabilityProblems = useMemo(() => orchestrationCapabilityProblems(selectedAgent, t, workerPair), [selectedAgent, t, workerPair]);
  const workingDirs = useMemo(() => {
    return Array.from(new Set((selectedAgent?.workingDirs || []).map((dir) => dir.trim()).filter(Boolean)));
  }, [selectedAgent]);
  const workerProfileSlots = useMemo(() => workerPair === 'codex-codex'
    ? [{ slot: 'codex-a', cli: 'codex' as const, label: 'Codex A' }, { slot: 'codex-b', cli: 'codex' as const, label: 'Codex B' }]
    : workerPair === 'claude-claude'
      ? [{ slot: 'claude-a', cli: 'claude' as const, label: 'Claude A' }, { slot: 'claude-b', cli: 'claude' as const, label: 'Claude B' }]
      : [{ slot: 'claude', cli: 'claude' as const, label: 'Claude' }, { slot: 'codex', cli: 'codex' as const, label: 'Codex' }], [workerPair]);
  const workerProfilesComplete = useMemo(
    () => workerProfileSlots.every(({ slot }) => Boolean(selectedWorkerProfiles[slot])),
    [selectedWorkerProfiles, workerProfileSlots],
  );

  const loadAgents = useCallback(async () => {
    const data = await api<{ agents: Agent[] }>('/api/agents');
    const nextAgents = data.agents || [];
    setAgents(nextAgents);
    setAgentsLoaded(true);
    setSelectedAgentId((current) => {
      const next = activeRunIdRef.current && activeRunAgentIdRef.current === current
        ? current
        : preferredAgentID(nextAgents, current);
      selectedAgentIdRef.current = next;
      if (next) localStorage.setItem('codexBridge.selectedAgentId', next);
      else localStorage.removeItem('codexBridge.selectedAgentId');
      return next;
    });
    return nextAgents;
  }, []);

  const refreshAgentsQuietly = useCallback(async () => {
    const data = await api<{ agents: Agent[] }>('/api/agents');
    const nextAgents = data.agents || [];
    setAgents(nextAgents);
    setAgentsLoaded(true);
    return nextAgents;
  }, []);

  const loadRuns = useCallback(async () => {
    const params = new URLSearchParams();
    params.set('limit', '200');
    const data = await api<{ runs: OrchestrationRun[] }>(`/api/orchestrations?${params.toString()}`);
    const incoming = Array.isArray(data.runs) ? data.runs.filter(isOrchestrationRun) : [];
    setRuns(incoming);
    return incoming;
  }, []);

  const loadRun = useCallback(async (runId: string) => {
    const data = await api<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(runId)}`);
    if (!isOrchestrationRun(data.run)) throw new Error(t.failedLoadOrchestration);
    setRuns((current) => upsertOrchestrationRun(current, data.run));
    return data.run;
  }, [t.failedLoadOrchestration]);

  const loadOrchestrationProgress = useCallback(async (runId: string) => {
    if (!planWorkspaceEnabled || !runId) return null;
    const data = await api<OrchestrationProgress>(`/api/orchestrations/${encodeURIComponent(runId)}/progress`);
    if (activeRunIdRef.current === runId) setOrchestrationProgress(data);
    return data;
  }, [planWorkspaceEnabled]);

  const loadRunEvents = useCallback(async (runId: string, replace = false, mode: 'latest' | 'after' | 'before' = 'latest') => {
    const params = new URLSearchParams();
    if (mode === 'after') {
      const afterSeq = eventMaxSeqByRunRef.current[runId] || 0;
      if (afterSeq > 0) params.set('afterSeq', String(afterSeq));
      params.set('limit', '1000');
    } else if (mode === 'before') {
      const beforeSeq = eventMinSeqByRunRef.current[runId] || 0;
      if (beforeSeq <= 1) return [];
      params.set('beforeSeq', String(beforeSeq));
      params.set('limit', String(orchestrationEventPageSize));
    } else {
      params.set('limit', String(orchestrationEventPageSize));
    }
    const query = params.toString();
    const data = await api<OrchestrationEventsPage>(`/api/orchestrations/${encodeURIComponent(runId)}/events${query ? `?${query}` : ''}`);
    const incoming = data.events || [];
    if (replace) {
      timelineOrderRef.current = 0;
    }
    const nextMaxSeq = maxOrchestrationEventSeq(incoming, replace ? 0 : eventMaxSeqByRunRef.current[runId] || 0);
    if (replace || nextMaxSeq > 0) {
      eventMaxSeqByRunRef.current[runId] = nextMaxSeq;
    }
    const currentMinSeq = replace ? 0 : eventMinSeqByRunRef.current[runId] || 0;
    const nextMinSeq = minOrchestrationEventSeq(incoming, currentMinSeq);
    if (replace) {
      eventMinSeqByRunRef.current[runId] = nextMinSeq;
    } else if (nextMinSeq > 0) {
      eventMinSeqByRunRef.current[runId] = currentMinSeq > 0 ? Math.min(currentMinSeq, nextMinSeq) : nextMinSeq;
    }
    if (replace || mode === 'before') {
      setHasOlderEventsByRun((current) => ({ ...current, [runId]: Boolean(data.hasMoreBefore) }));
    }
    setEvents((current) => {
      if (activeRunIdRef.current !== runId) return current;
      return replace ? incoming.slice().sort(compareOrchestrationEvents) : mergeOrchestrationEvents(current, incoming);
    });
    return incoming;
  }, []);

  const loadOlderEvents = useCallback(async () => {
    const runId = activeRunIdRef.current;
    if (!runId || loadingOlderEvents || olderEventsLoadInFlightRef.current || !hasOlderEventsByRun[runId]) return;
    olderEventsLoadInFlightRef.current = true;
    const container = scrollRef.current;
    const previousScrollHeight = container?.scrollHeight || 0;
    const previousScrollTop = container?.scrollTop || 0;
    setLoadingOlderEvents(true);
    stickToBottomRef.current = false;
    try {
      const incoming = await loadRunEvents(runId, false, 'before');
      // Keep the user's viewport anchored while the older page is inserted.
      window.requestAnimationFrame(() => {
        const nextContainer = scrollRef.current;
        if (!nextContainer || activeRunIdRef.current !== runId) return;
        const delta = nextContainer.scrollHeight - previousScrollHeight;
        nextContainer.scrollTop = previousScrollTop + Math.max(0, delta);
      });
    } finally {
      olderEventsLoadInFlightRef.current = false;
      if (activeRunIdRef.current === runId) setLoadingOlderEvents(false);
    }
  }, [hasOlderEventsByRun, loadRunEvents, loadingOlderEvents]);

  const scrollTimelineToBottom = useCallback((behavior: ScrollBehavior = 'smooth') => {
    const container = scrollRef.current;
    if (!container) return;
    container.scrollTo({ top: container.scrollHeight, behavior });
    stickToBottomRef.current = true;
    setShowScrollBottom(false);
  }, []);

  const updateTimelineScrollState = useCallback(() => {
    const container = scrollRef.current;
    if (!container) {
      setShowScrollBottom(false);
      return;
    }
    const nearBottom = isNearBottom(container);
    stickToBottomRef.current = nearBottom;
    setShowScrollBottom(timelineItems.length > 0 && !nearBottom);
    if (container.scrollTop < 240 && hasOlderEventsByRun[activeRunIdRef.current] && !loadingOlderEvents) {
      void loadOlderEvents().catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration));
    }
  }, [hasOlderEventsByRun, loadOlderEvents, loadingOlderEvents, t.failedLoadOrchestration, timelineItems.length]);

  const clearReconnect = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      window.clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const closeWS = useCallback(() => {
    clearReconnect();
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
  }, [clearReconnect]);

  const clearActiveOrchestration = useCallback(() => {
    closeWS();
    activeRunIdRef.current = '';
    activeRunAgentIdRef.current = '';
    desiredRunIdRef.current = '';
    setActiveRunId('');
    localStorage.removeItem(activeOrchestrationRunStorageKey);
    setEvents([]);
    setApprovals([]);
    timelineOrderRef.current = 0;
    setLoadingOlderEvents(false);
    setConnectionStatus(t.idle);
    setShowScrollBottom(false);
    if (window.location.pathname.startsWith('/orchestrate/runs/')) {
      navigate('/orchestrate', { replace: true });
    }
  }, [closeWS, navigate, t.idle]);

  const applyEvent = useCallback((event: OrchestrationEvent) => {
    const nextEvent = { ...event, timelineOrder: typeof event.timelineOrder === 'number' ? event.timelineOrder : ++timelineOrderRef.current };
    if (typeof nextEvent.seq === 'number' && Number.isFinite(nextEvent.seq)) {
      eventMaxSeqByRunRef.current[nextEvent.runId] = Math.max(eventMaxSeqByRunRef.current[nextEvent.runId] || 0, nextEvent.seq);
    }
    pendingLiveEventsRef.current.push(nextEvent);
    if (liveEventFrameRef.current !== null) return;
    liveEventFrameRef.current = window.requestAnimationFrame(() => {
      liveEventFrameRef.current = null;
      const pending = pendingLiveEventsRef.current.splice(0);
      if (!pending.length) return;
      setEvents((current) => mergeOrchestrationEvents(
        current,
        pending.filter((item) => item.runId === activeRunIdRef.current),
      ));
      setRuns((current) => pending
        .reduce((nextRuns, item) => nextRuns.map((run) => run.id === item.runId ? applyOrchestrationEventToRun(run, item) : run), current)
        .sort((a, b) => (b.updatedAt || b.createdAt || 0) - (a.updatedAt || a.createdAt || 0)));
    });
  }, []);

  const connectRun = useCallback((runId: string) => {
    closeWS();
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${scheme}://${location.host}/ws/orchestrations?runId=${encodeURIComponent(runId)}`);
    wsRef.current = ws;
    setConnectionStatus(t.connecting);
    let stopHeartbeat: (() => void) | null = null;
    ws.onopen = () => {
      if (wsRef.current !== ws) return;
      reconnectAttemptsRef.current = 0;
      setConnectionStatus(t.connected);
      stopHeartbeat = startWSHeartbeat(ws);
      void loadRunEvents(runId, false, 'after').catch(() => undefined);
    };
    ws.onmessage = (message) => {
      if (wsRef.current !== ws) return;
      try {
        const env = JSON.parse(message.data) as Envelope;
        if (env.type === 'orchestration_event') {
          const event = env.payload as OrchestrationEvent;
          if (event.runId === runId) applyEvent(event);
        } else if (env.type === 'approval_request') {
          const approval = env.payload as ApprovalRequest;
          if (approval.requestId && approval.runId === runId) {
            setApprovals((current) => upsertApprovalItem(current, approval, ++timelineOrderRef.current));
          }
        } else if (env.type === 'status') {
          setConnectionStatus(env.payload?.status || t.connected);
        }
      } catch {
        // Ignore malformed frames.
      }
    };
    ws.onerror = () => {
      if (wsRef.current === ws) setConnectionStatus(t.connectionError);
    };
    ws.onclose = () => {
      stopHeartbeat?.();
      if (wsRef.current !== ws) return;
      setConnectionStatus(t.disconnected);
      if (activeRunIdRef.current !== runId) return;
      const delay = Math.min(10000, 1000 * Math.max(1, reconnectAttemptsRef.current + 1));
      reconnectAttemptsRef.current += 1;
      clearReconnect();
      reconnectTimerRef.current = window.setTimeout(() => {
        reconnectTimerRef.current = null;
        if (activeRunIdRef.current !== runId) return;
        void Promise.all([loadRun(runId), loadRunEvents(runId, false, 'after')])
          .then(([run]) => {
            if (activeRunIdRef.current === runId && activeOrchestrationStatus(run.status)) connectRun(runId);
          })
          .catch(() => {
            if (activeRunIdRef.current === runId) connectRun(runId);
          });
      }, delay);
    };
  }, [applyEvent, clearReconnect, closeWS, loadRun, loadRunEvents, startWSHeartbeat, t.connected, t.connecting, t.connectionError, t.disconnected]);

  const activateRun = useCallback(async (run: OrchestrationRun, options: { syncURL?: boolean; replaceURL?: boolean } = {}) => {
    if (desiredRunIdRef.current && desiredRunIdRef.current !== run.id) return;
    const urlRunID = orchestrationRunIdFromPath(window.location.pathname);
    if (urlRunID && urlRunID !== run.id) return;
    draftingRunRef.current = false;
    const runAgentId = run.agentId || selectedAgentIdRef.current;
    timelineOrderRef.current = 0;
    activeRunIdRef.current = run.id;
    activeRunAgentIdRef.current = runAgentId;
    setActiveRunId(run.id);
    setLoadingOlderEvents(false);
    setRuns((current) => upsertOrchestrationRun(current, run));
    localStorage.setItem(activeOrchestrationRunStorageKey, run.id);
    if (runAgentId) {
      selectedAgentIdRef.current = runAgentId;
      setSelectedAgentId(runAgentId);
      localStorage.setItem('codexBridge.selectedAgentId', runAgentId);
      rememberActiveOrchestrationRunForAgent(runAgentId, run.id);
    }
    setEvents((current) => current.filter((event) => event.runId === run.id));
    setApprovals((current) => current.filter((item) => item.approval.runId === run.id));
    setMode(run.mode === 'debate' ? 'debate' : 'collaboration');
    const nextWorkerPair = normalizeOrchestrationWorkerPair(run.workerPair);
    setWorkerPair(nextWorkerPair);
    setFirstCli(nextWorkerPair === 'codex-codex' || run.firstCli === 'codex' ? 'codex' : 'claude');
    setProfile(run.profile === 'formal-proof' ? 'formal-proof' : 'default');
    setNativeContextCompaction(run.nativeContextCompaction === 'after-turn' ? 'after-turn' : 'off');
    setCwd(run.cwd || '');
    setMaxTurns(run.maxTurns || 4);
    stickToBottomRef.current = true;
    setShowScrollBottom(false);
    if (options.syncURL !== false) {
      navigate(orchestrationRunPath(run.id), { replace: options.replaceURL });
    }
    await loadRunEvents(run.id, true, 'latest');
    if (activeRunIdRef.current !== run.id || desiredRunIdRef.current !== run.id) return;
    if (activeOrchestrationStatus(run.status)) {
      connectRun(run.id);
    } else {
      closeWS();
      setConnectionStatus(t.idle);
    }
  }, [closeWS, connectRun, loadRunEvents, navigate, t.idle]);

  const selectRun = useCallback(async (runId: string, options: { syncURL?: boolean; replaceURL?: boolean } = {}) => {
    draftingRunRef.current = false;
    desiredRunIdRef.current = runId;
    if (options.syncURL !== false && orchestrationRunIdFromPath(window.location.pathname) !== runId) {
      navigate(orchestrationRunPath(runId), { replace: options.replaceURL });
    }
    timelineOrderRef.current = 0;
    activeRunIdRef.current = runId;
    setActiveRunId(runId);
    setEvents((current) => current.filter((event) => event.runId === runId));
    setApprovals((current) => current.filter((item) => item.approval.runId === runId));
    const run = await loadRun(runId);
    if (activeRunIdRef.current !== runId || desiredRunIdRef.current !== runId) return;
    await activateRun(run, options);
  }, [activateRun, loadRun, navigate]);

  const respondOrchestrationApproval = useCallback((requestId: string, decision: 'accept' | 'decline' | 'cancel') => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN || !activeRunIdRef.current) return;
    wsRef.current.send(JSON.stringify({
      type: 'approval_response',
      payload: { requestId, decision },
    }));
    setApprovals((current) => updateApprovalItemStatus(current, requestId, approvalStatusFromDecision(decision)));
  }, []);

  const refreshOrchestration = useCallback(async () => {
    if (refreshOrchestrationInFlightRef.current) return refreshOrchestrationInFlightRef.current;
    const task = (async () => {
      const refreshEpoch = agentSelectionEpochRef.current;
      const isCurrentRefresh = () => refreshEpoch === agentSelectionEpochRef.current;
      setRefreshingOrchestration(true);
      try {
        const requestedRunId = orchestrationRunIdFromPath(window.location.pathname);
        const loadedAgents = await loadAgents();
        if (!isCurrentRefresh()) return;
        let directRun: OrchestrationRun | null = null;
        if (requestedRunId) {
          directRun = await loadRun(requestedRunId);
          if (!isCurrentRefresh()) return;
          if (orchestrationRunIdFromPath(window.location.pathname) !== requestedRunId) {
            directRun = null;
          }
        }
        const savedAgentId = directRun?.agentId || localStorage.getItem('codexBridge.selectedAgentId') || selectedAgentIdRef.current;
        const agentId = directRun?.agentId || preferredAgentID(loadedAgents, savedAgentId);
        selectedAgentIdRef.current = agentId;
        setSelectedAgentId(agentId);
        if (agentId) localStorage.setItem('codexBridge.selectedAgentId', agentId);
        else localStorage.removeItem('codexBridge.selectedAgentId');
        await loadRuns();
        if (!isCurrentRefresh() || selectedAgentIdRef.current !== agentId) return;
        if (directRun) {
          if (activeRunIdRef.current === directRun.id) {
            return;
          }
          desiredRunIdRef.current = directRun.id;
          await activateRun(directRun, { syncURL: false });
          return;
        }
        if (draftingRunRef.current) return;
        if (activeRunIdRef.current && activeRunAgentIdRef.current === agentId) {
          return;
        }
        clearActiveOrchestration();
      } finally {
        refreshOrchestrationInFlightRef.current = null;
        setRefreshingOrchestration(false);
        orchestrationBootedRef.current = true;
      }
    })();
    refreshOrchestrationInFlightRef.current = task;
    return task;
  }, [activateRun, clearActiveOrchestration, loadAgents, loadRun, loadRuns]);

  const refreshVisibleOrchestrationData = useCallback(async () => {
    const runId = orchestrationRunIdFromPath(window.location.pathname) || activeRunIdRef.current;
    setRefreshingOrchestration(true);
    try {
      const [nextAgents, nextRuns, refreshedRun] = await Promise.all([
        api<{ agents: Agent[] }>('/api/agents'),
        api<{ runs: OrchestrationRun[] }>('/api/orchestrations?limit=200'),
        runId ? api<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(runId)}`) : Promise.resolve(null),
        runId ? loadRunEvents(runId, false, 'after') : Promise.resolve([]),
      ]);
      setAgents(nextAgents.agents || []);
      setAgentsLoaded(true);
      const incomingRuns = Array.isArray(nextRuns.runs) ? nextRuns.runs.filter(isOrchestrationRun) : [];
      const currentRun = refreshedRun && isOrchestrationRun(refreshedRun.run) ? refreshedRun.run : null;
      setRuns(currentRun ? upsertOrchestrationRun(incomingRuns, currentRun) : incomingRuns);
    } finally {
      setRefreshingOrchestration(false);
    }
  }, [loadRunEvents]);

  useEffect(() => {
    let stopped = false;
    let retryTimer: number | null = null;
    const retry = () => {
      if (stopped || document.visibilityState !== 'visible' || !navigator.onLine) return;
      if (retryTimer !== null) {
        window.clearTimeout(retryTimer);
        retryTimer = null;
      }
      void refreshOrchestration()
        .then(() => {
          if (!stopped) setError('');
        })
        .catch((err) => {
          if (stopped) return;
          setError(err instanceof Error ? err.message : t.failedLoadOrchestration);
          retryTimer = window.setTimeout(retry, 3000);
        });
    };
    const recover = () => {
      if (document.visibilityState !== 'visible' || !navigator.onLine) return;
      if (!orchestrationBootedRef.current) {
        retry();
        return;
      }
      void refreshVisibleOrchestrationData().catch(() => undefined);
    };
    retry();
    window.addEventListener('online', recover);
    document.addEventListener('visibilitychange', recover);
    return () => {
      stopped = true;
      if (retryTimer !== null) window.clearTimeout(retryTimer);
      window.removeEventListener('online', recover);
      document.removeEventListener('visibilitychange', recover);
      closeWS();
    };
  }, []);

  useEffect(() => {
    if (!orchestrationBootedRef.current || !pathRunId || pathRunId === activeRunIdRef.current) return;
    selectRun(pathRunId, { syncURL: false }).catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration));
  }, [pathRunId, selectRun, t.failedLoadOrchestration]);

  useEffect(() => {
    let stopped = false;
    const syncAgents = () => {
      if (stopped || document.visibilityState !== 'visible') return;
      refreshAgentsQuietly().catch(() => undefined);
    };
    const interval = window.setInterval(syncAgents, 5000);
    document.addEventListener('visibilitychange', syncAgents);
    return () => {
      stopped = true;
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', syncAgents);
    };
  }, [refreshAgentsQuietly]);

  useEffect(() => {
    selectedAgentIdRef.current = selectedAgentId;
  }, [selectedAgentId]);

	const refreshWorkerPresets = useCallback(async () => {
		const agentId = selectedAgentIdRef.current;
		if (!agentId) {
			setPresets([]);
			return;
		}
		const data = await api<{ presets: CLIConfigPreset[] }>(`/api/agents/${encodeURIComponent(agentId)}/cli-config/presets`);
		if (selectedAgentIdRef.current === agentId) {
			setPresets(Array.isArray(data.presets) ? data.presets : []);
		}
	}, []);

  useEffect(() => {
    let cancelled = false;
    setPresets([]);
    setSelectedWorkerProfiles({});
    setSelectedWorkerEfforts({});
    setWorkerProfilesTouched(false);
    if (!selectedAgentId) return () => { cancelled = true; };
		void refreshWorkerPresets()
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, [refreshWorkerPresets, selectedAgentId]);

  useEffect(() => {
    setSelectedPromptKey('initial');
    setOrchestrationProgress(null);
    setSelectedProgressTaskNumber(null);
    setSelectedProgressGraphId('');
    setAgentGraphExpanded(false);
    setPlanningWorkspaceOpen(false);
  }, [activeRunId]);

  useEffect(() => {
    if (!progressTasks.length) return;
    setSelectedProgressTaskNumber((current) => progressTasks.some((item) => item.taskNumber === current) ? current : progressTasks[progressTasks.length - 1].taskNumber);
  }, [progressTasks]);

  useEffect(() => {
    setSelectedPromptKey(`task-${selectedProgressTask?.taskNumber || 1}`);
    setSelectedProgressGraphId(selectedProgressTask?.graph?.id || selectedProgressTask?.graphs?.[selectedProgressTask.graphs.length - 1]?.id || '');
  }, [selectedProgressTask?.taskNumber]);

  useEffect(() => {
    if (!activeRunId || !planWorkspaceEnabled) return;
    void loadOrchestrationProgress(activeRunId).catch(() => undefined);
  }, [activeRunId, loadOrchestrationProgress, planWorkspaceEnabled, progressRefreshKey]);

  useEffect(() => {
    setCollapsedTimelineGroups((current) => {
      if (collapsedTimelineRunIdRef.current !== activeRunId) {
        if (!timelineGroups.length) {
          collapsedTimelineRunIdRef.current = '';
          return {};
        }
        collapsedTimelineRunIdRef.current = activeRunId;
        return defaultCollapsedTimelineGroups(timelineGroups);
      }
      return reconcileCollapsedTimelineGroups(current, timelineGroups);
    });
  }, [activeRunId, timelineGroups]);

  useEffect(() => {
    // Before the first /api/agents response lands, selectedAgent is always
    // empty; clearing then would wipe the remembered run and strip a deep-link
    // URL on every page load.
    if (!agentsLoaded) return;
    // A deep-linked run is an explicit selection. Agent synchronization can
    // update endpoint metadata, but must never replace the URL-selected run
    // with the browser's remembered run for another endpoint.
    if (pathRunId) {
      if (activeRunIdRef.current !== pathRunId) {
        selectRun(pathRunId, { syncURL: false }).catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration));
      }
      return;
    }
    if (draftingRunRef.current) return;
    if (!selectedAgent?.id) {
      if (activeRunIdRef.current && activeRunAgentIdRef.current === selectedAgentIdRef.current) return;
      clearActiveOrchestration();
      return;
    }
    const currentRun = runs.find((run) => run.id === activeRunIdRef.current);
    if (currentRun?.agentId === selectedAgent.id) return;
    clearActiveOrchestration();
  }, [agentsLoaded, clearActiveOrchestration, pathRunId, runs, selectRun, selectedAgent?.id, t.failedLoadOrchestration]);

  useEffect(() => {
    if (!activeRunId || !activeOrchestrationStatus(activeRun?.status)) return;
    let stopped = false;
    const syncActiveRun = async () => {
      try {
        await Promise.all([loadRun(activeRunId), loadRunEvents(activeRunId, false, 'after')]);
      } catch {
        // The websocket remains the primary live path; polling is a quiet fallback.
      }
    };
    const interval = window.setInterval(() => {
      if (!stopped) void syncActiveRun();
    }, 3000);
    const handleVisibility = () => {
      if (document.visibilityState === 'visible' && !stopped) void syncActiveRun();
    };
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      stopped = true;
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [activeRunId, activeRun?.status, loadRun, loadRunEvents]);

  useEffect(() => {
    if (!activeRunId || activeOrchestrationStatus(activeRun?.status)) return;
    closeWS();
    setConnectionStatus(t.idle);
  }, [activeRunId, activeRun?.status, closeWS, t.idle]);

  useEffect(() => {
    if (!isRunning) return;
    const interval = window.setInterval(() => setElapsedTick((current) => current + 1), 1000);
    return () => window.clearInterval(interval);
  }, [isRunning]);

  useEffect(() => {
    const id = window.requestAnimationFrame(() => {
      const container = scrollRef.current;
      if (!container) return;
      if (stickToBottomRef.current) {
        scrollTimelineToBottom('auto');
        return;
      }
      setShowScrollBottom(timelineItems.length > 0 && !isNearBottom(container));
    });
    return () => window.cancelAnimationFrame(id);
  }, [activeRunId, scrollTimelineToBottom, timelineItems]);

  useEffect(() => {
    if (!workingDirs.length) {
      if (cwd) setCwd('');
      return;
    }
    if (!cwd || !workingDirs.includes(cwd)) {
      setCwd(workingDirs[0]);
    }
  }, [cwd, workingDirs]);

  const addFiles = async (inputFiles: FileList | null) => {
    if (!inputFiles?.length) return;
    const next = await Promise.all(Array.from(inputFiles).map(readUploadAttachment));
    setFiles((current) => [...current, ...next].slice(0, 12));
    if (fileInputRef.current) fileInputRef.current.value = '';
  };

  const removeFile = (id: string) => {
    setFiles((current) => current.filter((file) => file.id !== id));
  };

  const startRun = async () => {
    const task = prompt.trim();
    if (!task || creating || isRunning) return;
    if (!activeRun && !workerProfilesComplete) {
      setError(t.workerPresetRequired);
      return;
    }
    if (capabilityProblems.length > 0) {
      setError(capabilityProblems.join(' '));
      return;
    }
    setCreating(true);
    setError('');
    try {
      const endpoint = activeRun ? `/api/orchestrations/${encodeURIComponent(activeRun.id)}/prompts` : '/api/orchestrations';
      const selectedProfiles = selectedWorkerSlotValues(workerProfileSlots, selectedWorkerProfiles);
      const body: Record<string, unknown> = {
        mode,
        workerPair,
        firstCli: workerPair === 'codex-codex' ? 'codex' : workerPair === 'claude-claude' ? 'claude' : firstCli,
        profile,
        nativeContextCompaction,
        prompt: task,
        title: titleFromPrompt(task, t),
        cwd: cwd.trim(),
        maxTurns,
        agentId: selectedAgent?.id || '',
        files: files.map(({ name, mimeType, size, data }) => ({ name, mimeType, size, data })),
      };
      if (!activeRun || workerProfilesTouched) {
        body.workerProfilePresetIds = selectedProfiles;
        body.workerProfileEfforts = selectedWorkerSlotValues(workerProfileSlots, selectedWorkerEfforts);
      }
      const data = await api<{ run: OrchestrationRun }>(endpoint, {
        method: 'POST',
        body: JSON.stringify(body),
      });
      setRuns((current) => [data.run, ...current.filter((run) => run.id !== data.run.id)]);
      setPrompt('');
      setFiles([]);
      localStorage.setItem(activeOrchestrationRunStorageKey, data.run.id);
      rememberActiveOrchestrationRunForAgent(data.run.agentId, data.run.id);
      await selectRun(data.run.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : t.failedStartOrchestration);
    } finally {
      setCreating(false);
    }
  };

  const cancelRun = async () => {
    if (!activeRun || !canCancelOrchestrationStatus(activeRun.status)) return;
    setRuns((current) => current.map((run) => run.id === activeRun.id ? { ...run, status: 'canceling' } : run));
    const data = await api<{ status?: string }>(`/api/orchestrations/${encodeURIComponent(activeRun.id)}/cancel`, { method: 'POST', body: '{}' });
    if (data.status && data.status !== 'canceling') {
      setRuns((current) => current.map((run) => run.id === activeRun.id ? { ...run, status: data.status || run.status } : run));
    }
  };

  const deleteRun = async (run: OrchestrationRun) => {
    const confirmMessage = activeOrchestrationStatus(run.status) ? t.deleteActiveRunConfirm : t.deleteRunConfirm;
    if (deletingRunId || !window.confirm(confirmMessage)) return;
    setDeletingRunId(run.id);
    setError('');
    try {
      await api(`/api/orchestrations/${encodeURIComponent(run.id)}`, { method: 'DELETE' });
      setRuns((current) => current.filter((item) => item.id !== run.id));
      forgetActiveOrchestrationRunForAgent(run.agentId, run.id);
      if (activeRunIdRef.current === run.id) clearActiveOrchestration();
    } catch (err) {
      setError(err instanceof Error ? `${t.failedDeleteRun}: ${err.message}` : t.failedDeleteRun);
    } finally {
      setDeletingRunId('');
    }
  };

  const shareRun = async (run: OrchestrationRun | null) => {
    if (!run || sharingRunId) return;
    setSharingRunId(run.id);
    setError('');
    try {
      const data = await api<{ share: ShareInfo }>(`/api/orchestrations/${encodeURIComponent(run.id)}/share`, { method: 'POST', body: '{}' });
      const url = data.share.url || `${window.location.origin}/share/${data.share.id}`;
      await copyText(url);
      setShareCopiedRunId(run.id);
      window.setTimeout(() => setShareCopiedRunId(''), 1400);
    } catch (err) {
      setError(err instanceof Error ? `${t.failedCreateShare}: ${err.message}` : t.failedCreateShare);
    } finally {
      setSharingRunId('');
    }
  };

  const logout = async () => {
    closeWS();
    await api('/api/logout', { method: 'POST', body: '{}' });
    onLogout();
  };

  const selectAgent = (agentId: string) => {
    agentSelectionEpochRef.current += 1;
    selectedAgentIdRef.current = agentId;
    setSelectedAgentId(agentId);
    if (agentId) localStorage.setItem('codexBridge.selectedAgentId', agentId);
    else localStorage.removeItem('codexBridge.selectedAgentId');
    if (draftingRunRef.current) return;
    clearActiveOrchestration();
  };

  const openSettings = (focus: 'cli' | '' = '') => {
    setSettingsFocus(focus);
    setSettingsOpen(true);
  };

  const selectWorkerPair = (nextWorkerPair: WorkerPair) => {
    setWorkerPair(nextWorkerPair);
    setSelectedWorkerProfiles({});
    setSelectedWorkerEfforts({});
    setWorkerProfilesTouched(false);
    if (nextWorkerPair === 'codex-codex') {
      setFirstCli('codex');
	} else if (nextWorkerPair === 'claude-claude') {
	  setFirstCli('claude');
    }
  };

  const renderWorkerProfileSelectors = () => {
    const disabled = creating || isRunning;
    return (
      <div className="space-y-2">
        <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.workerModelProfiles}</label>
        <div className="grid gap-2 sm:grid-cols-2">
          {workerProfileSlots.map(({ slot, cli, label }) => {
            const options = presets.filter((preset) => preset.cli === cli);
            const selectedPreset = options.find((preset) => preset.id === selectedWorkerProfiles[slot]);
            const reasoningLevels = selectedPreset?.reasoningLevels || [];
            const defaultEffort = selectedPreset?.reasoningEffort || selectedPreset?.reasoningDefault || '';
            return (
              <div key={slot} className="block min-w-0 space-y-1.5">
                <span className="text-xs text-muted-foreground">{label}</span>
                <select
                  value={selectedWorkerProfiles[slot] || ''}
                  onChange={(event) => {
                    const value = event.target.value;
                    const nextPreset = options.find((preset) => preset.id === value);
                    const nextLevels = nextPreset?.reasoningLevels || [];
                    const nextDefault = nextPreset?.reasoningEffort || nextPreset?.reasoningDefault || '';
                    setSelectedWorkerProfiles((current) => ({ ...current, [slot]: value }));
                    setSelectedWorkerEfforts((current) => ({ ...current, [slot]: nextLevels.includes(nextDefault) ? nextDefault : '' }));
                    setWorkerProfilesTouched(true);
                  }}
                  disabled={disabled || !options.length}
                  className="flex h-9 w-full min-w-0 rounded-md border border-input bg-transparent px-2 py-1 text-xs shadow-sm disabled:cursor-not-allowed disabled:opacity-50"
                  aria-label={`${label} ${t.workerModelProfiles}`}
                >
                  <option value="" disabled>{options.length ? t.selectPreset : t.noProviderPresets}</option>
                  {options.map((preset) => <option key={preset.id} value={preset.id}>{preset.name} · {preset.model}</option>)}
                </select>
                <select
                  value={selectedWorkerEfforts[slot] || ''}
                  onChange={(event) => {
                    setSelectedWorkerEfforts((current) => ({ ...current, [slot]: event.target.value }));
                    setWorkerProfilesTouched(true);
                  }}
                  disabled={disabled || !selectedPreset || !reasoningLevels.length}
                  className="flex h-8 w-full min-w-0 rounded-md border border-input bg-transparent px-2 py-1 text-[11px] shadow-sm disabled:cursor-not-allowed disabled:opacity-50"
                  aria-label={`${label} ${t.reasoningEffort}`}
                >
                  <option value="">{selectedPreset ? (reasoningLevels.length ? t.defaultReasoningEffort : t.modelCatalogUnavailable) : t.selectPreset}</option>
                  {reasoningLevels.map((level) => <option key={level} value={level}>{level}{level === selectedPreset?.reasoningDefault ? ` (${t.defaultReasoningEffort})` : ''}</option>)}
                </select>
              </div>
            );
          })}
        </div>
      </div>
    );
  };

  const renderWorkerPairSelector = (placement: 'toolbar' | 'panel') => {
    const isToolbar = placement === 'toolbar';
    const disabled = creating || isRunning;
    return (
      <div className={isToolbar ? "flex shrink-0 items-center gap-2" : "space-y-2"}>
        {isToolbar ? (
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.workerPair}</span>
        ) : (
          <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.workerPair}</label>
        )}
        <div className={cn("grid grid-cols-3 gap-1 rounded-lg border border-border bg-muted p-1", isToolbar ? "w-[22rem]" : "w-full")}>
          <button
            className={cn("h-8 rounded-md px-2 text-xs font-medium", workerPair === 'claude-codex' ? "bg-background shadow-sm" : "text-muted-foreground")}
            onClick={() => selectWorkerPair('claude-codex')}
            disabled={disabled}
            aria-pressed={workerPair === 'claude-codex'}
          >
            {t.workerPairClaudeCodex}
          </button>
          <button
            className={cn("h-8 rounded-md px-2 text-xs font-medium", workerPair === 'codex-codex' ? "bg-background shadow-sm" : "text-muted-foreground")}
            onClick={() => selectWorkerPair('codex-codex')}
            disabled={disabled}
            aria-pressed={workerPair === 'codex-codex'}
          >
            {t.workerPairCodexCodex}
          </button>
		  <button
			className={cn("h-8 rounded-md px-2 text-xs font-medium", workerPair === 'claude-claude' ? "bg-background shadow-sm" : "text-muted-foreground")}
			onClick={() => selectWorkerPair('claude-claude')}
			disabled={disabled}
			aria-pressed={workerPair === 'claude-claude'}
		  >
			{t.workerPairClaudeClaude}
		  </button>
        </div>
      </div>
    );
  };

  const startDraftRun = () => {
    agentSelectionEpochRef.current += 1;
    draftingRunRef.current = true;
    clearActiveOrchestration();
    setPrompt(t.reviewCurrentRepository);
    setFiles([]);
    setError('');
    window.setTimeout(() => taskInputRef.current?.focus(), 0);
  };

  const toggleTimelineGroup = (key: string) => {
    setCollapsedTimelineGroups((current) => ({ ...current, [key]: !current[key] }));
  };

  const jumpToTimelineItem = (groupKey: string, targetID: string) => {
    setCollapsedTimelineGroups((current) => ({ ...current, [groupKey]: false }));
    stickToBottomRef.current = false;
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => {
        const container = scrollRef.current;
        const target = document.getElementById(targetID);
        if (!container || !target) return;
        const containerTop = container.getBoundingClientRect().top;
        const targetTop = target.getBoundingClientRect().top;
        container.scrollTo({ top: container.scrollTop + targetTop - containerTop - 12, behavior: 'smooth' });
      });
    });
  };

  return (
    <div className="h-screen w-full flex bg-background text-foreground overflow-hidden font-sans">
      <aside className="hidden md:flex w-[280px] flex-col border-r border-sidebar-border bg-sidebar">
        <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
          <div className="flex items-center gap-2 font-medium">
            <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
              <GitBranch className="h-3.5 w-3.5" />
            </div>
            <span className="text-sm">{t.orchestration}</span>
          </div>
        </div>
        <div className="p-3 space-y-2">
          {canOpenMain && (
            <Button variant="ghost" className="w-full justify-start gap-2 h-9 rounded-lg" onClick={() => navigate('/')}>
              <ArrowLeft className="h-4 w-4" />
              {t.codexBridge}
            </Button>
          )}
          <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm" onClick={startDraftRun}>
            <Plus className="h-4 w-4" />
            {t.newRun}
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto px-3 py-2 space-y-1 elegant-scrollbar">
          {agentRuns.length === 0 ? (
            <div className="px-2 py-1.5 text-xs text-muted-foreground">{t.noOrchestrationRuns}</div>
          ) : agentRuns.map((run) => (
            <div
              key={run.id}
              className={cn(
                "w-full rounded-md px-2 py-2 text-sm transition-colors",
                activeRunId === run.id ? "bg-sidebar-accent text-sidebar-accent-foreground" : "text-sidebar-foreground hover:bg-sidebar-accent/50",
                activeOrchestrationStatus(run.status) && "ring-1 ring-inset ring-emerald-500/40",
              )}
            >
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  className="flex min-w-0 flex-1 items-center gap-2 text-left"
                  onClick={() => selectRun(run.id).catch((err) => setError(err.message))}
                >
                  {activeOrchestrationStatus(run.status) ? <span className="h-2 w-2 shrink-0 rounded-full bg-emerald-500 shadow-[0_0_0_3px_rgba(16,185,129,0.12)]" /> : run.mode === 'debate' ? <Swords className="h-3.5 w-3.5 shrink-0 opacity-70" /> : <UsersRound className="h-3.5 w-3.5 shrink-0 opacity-70" />}
                  <span className="truncate font-medium">{run.title}</span>
                </button>
                <button
                  type="button"
                  className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded border border-sidebar-border bg-sidebar text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-60"
                  onClick={() => void deleteRun(run)}
                  title={deletingRunId === run.id ? t.deletingRun : t.deleteRun}
                  disabled={Boolean(deletingRunId)}
                >
                  {deletingRunId === run.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : <Trash2 className="h-3 w-3" />}
                </button>
                <button
                  type="button"
                  className={cn(
                    "inline-flex h-6 shrink-0 items-center gap-1 rounded border border-sidebar-border bg-sidebar px-1.5 text-[10px] font-medium text-muted-foreground hover:bg-sidebar-border",
                    shareCopiedRunId === run.id && "text-emerald-600 dark:text-emerald-400",
                  )}
                  onClick={() => shareRun(run).catch((err) => setError(err.message))}
                  title={shareCopiedRunId === run.id ? t.copied : t.shareRun}
                >
                  {sharingRunId === run.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : shareCopiedRunId === run.id ? <Check className="h-3 w-3" /> : <Share2 className="h-3 w-3" />}
                  <span>{shareCopiedRunId === run.id ? t.copied : t.shareRun}</span>
                </button>
              </div>
              <button
                type="button"
                className="mt-1 flex w-full items-center justify-between text-[10px] text-muted-foreground"
                onClick={() => selectRun(run.id).catch((err) => setError(err.message))}
              >
                <span className="truncate">{agents.find((agent) => agent.id === run.agentId)?.name || sessionDateLabel(run.updatedAt || run.createdAt, t)}</span>
                <span>{run.status}</span>
              </button>
            </div>
          ))}
        </div>
        <div className="p-3 border-t border-sidebar-border shrink-0">
          <a href="/help" className="mb-1 flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-foreground">
            <BookOpen className="h-3.5 w-3.5" />
            <span>{t.help}</span>
          </a>
          <a href="/updates" className="mb-1 flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-foreground">
            <History className="h-3.5 w-3.5" />
            <span>{t.updates}</span>
          </a>
          <button onClick={() => openSettings()} className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-sm hover:bg-sidebar-accent transition-colors">
            <Settings className="h-3.5 w-3.5" />
            <span className="flex-1 text-left">{t.settings}</span>
            <div className={cn("h-1.5 w-1.5 rounded-full", onlineAgent ? "bg-emerald-500" : "bg-muted-foreground")} />
          </button>
        </div>
      </aside>

      <main className="relative flex-1 flex flex-col min-w-0 h-full">
        <header className="h-14 shrink-0 border-b border-border flex items-center justify-between px-3 md:px-4 bg-background z-10">
          <div className="flex items-center gap-2 min-w-0">
            {canOpenMain && (
              <Button variant="ghost" size="icon" className="md:hidden text-muted-foreground" onClick={() => navigate('/')}>
                <ArrowLeft className="h-5 w-5" />
              </Button>
            )}
            <GitBranch className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium truncate">{activeRun?.title || t.cliOrchestration}</span>
          </div>
          <div className="flex items-center gap-2">
            <AgentSelector
              agents={agents}
              selectedAgentId={selectedAgentId}
              onSelect={selectAgent}
              t={t}
              className="hidden sm:inline-flex"
              disabled={creating}
              activeAgentIds={activeAgentIds}
            />
            <Button variant="secondary" size="sm" className="hidden sm:inline-flex h-8 gap-1.5 rounded-lg" onClick={() => openSettings('cli')}>
              <Plus className="h-3.5 w-3.5" />
              {t.addCliEndpoint}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className={cn(
                "h-8 gap-1.5 rounded-lg",
                shareCopiedRunId === activeRun?.id && "text-emerald-600 dark:text-emerald-400"
              )}
              onClick={() => shareRun(activeRun).catch((err) => setError(err.message))}
              disabled={!activeRun || sharingRunId === activeRun.id}
              aria-label={t.shareRun}
              title={shareCopiedRunId === activeRun?.id ? t.copied : t.shareRun}
            >
              {sharingRunId === activeRun?.id ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : shareCopiedRunId === activeRun?.id ? <Check className="h-3.5 w-3.5" /> : <Share2 className="h-3.5 w-3.5" />}
              <span>{shareCopiedRunId === activeRun?.id ? t.copied : t.shareRun}</span>
            </Button>
            <Button variant="secondary" size="icon" className="h-8 w-8 rounded-lg" onClick={() => navigate(activeRun ? `/orchestrate/stats?run=${encodeURIComponent(activeRun.id)}` : '/orchestrate/stats')} aria-label={t.runStatistics} title={t.runStatistics}>
              <BarChart3 className="h-3.5 w-3.5" />
            </Button>
            <Button variant="secondary" size="icon" className="h-8 w-8 rounded-lg" onClick={() => navigate('/orchestrate/usage')} aria-label={t.usageOverview} title={t.usageOverview}>
              <PieChart className="h-3.5 w-3.5" />
            </Button>
            {user.isAdmin && <Button variant="secondary" size="icon" className="h-8 w-8 rounded-lg" onClick={() => navigate('/admin/usage')} aria-label={t.adminDashboard} title={t.adminDashboard}><ShieldCheck className="h-3.5 w-3.5" /></Button>}
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground rounded-full h-8 w-8"
              onClick={() => refreshVisibleOrchestrationData().catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration))}
              disabled={refreshingOrchestration}
              aria-label={t.refresh}
              title={t.refresh}
            >
              <RefreshCw className={cn("h-4 w-4", refreshingOrchestration && "animate-spin")} />
            </Button>
          </div>
        </header>

        <div className="bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          {renderWorkerPairSelector('toolbar')}
          <AgentSelector
            agents={agents}
            selectedAgentId={selectedAgentId}
            onSelect={selectAgent}
            t={t}
            className="sm:hidden min-w-[220px] shrink-0"
            disabled={creating}
            activeAgentIds={activeAgentIds}
          />
          <div className="flex items-center gap-1.5">
            <Server className="h-3.5 w-3.5" />
            <span>{t.workers}: {orchestrationWorkerLabel(selectedAgent, t)} · {selectedAgent?.online ? t.online : t.offline}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <ShieldQuestion className="h-3.5 w-3.5" />
            <span>{t.browserApproval}: {orchestrationApprovalMode(selectedAgent) === 'auto-execute' ? t.autoExecute : orchestrationApprovalMode(selectedAgent) === 'strict-workspace' ? t.strictWorkspace : capabilityProblems.length ? t.notAvailable : t.available}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            <span>{t.status}: {activeRun?.status || t.idle}</span>
          </div>
          {currentTurnLabel && (
            <div className="flex items-center gap-1.5">
              <GitBranch className="h-3.5 w-3.5" />
              <span>{isRunning ? t.currentTurn : t.lastTurn}: {currentTurnLabel}</span>
            </div>
          )}
          <div className="flex items-center gap-1.5">
            <Command className="h-3.5 w-3.5" />
            <span>{t.stream}: {orchestrationStreamStatus}</span>
          </div>
        </div>

        {planWorkspaceEnabled && activeRun && (
          <section className="shrink-0 border-b border-border bg-background">
            <button
              type="button"
              className="flex min-h-11 w-full items-center gap-3 px-4 py-2 text-left transition-colors hover:bg-muted/30 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring"
              onClick={() => setPlanningWorkspaceOpen((current) => !current)}
              aria-expanded={planningWorkspaceOpen}
              aria-label={planningWorkspaceOpen ? t.closePlanningWorkspace : t.openPlanningWorkspace}
            >
              <Workflow className="h-4 w-4 shrink-0 text-primary" />
              <span className="shrink-0 text-xs font-semibold">{t.planningWorkspace}</span>
              <div className="h-1.5 min-w-[5rem] max-w-40 flex-1 overflow-hidden rounded-full bg-muted">
                <div className="h-full bg-emerald-500 transition-[width] duration-300" style={{ width: `${Math.max(0, Math.min(100, projectedPlan?.percent || 0))}%` }} />
              </div>
              <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">
                {projectedPlanItems.length ? `${projectedPlan?.completed || 0}/${projectedPlan?.total || projectedPlanItems.length} · ${projectedPlan?.percent || 0}%` : t.planWaiting}
              </span>
              {currentPlanItem && <span className="hidden min-w-0 max-w-[28rem] truncate text-xs text-muted-foreground lg:block">{language === 'zh' ? '当前' : 'Current'}: {currentPlanItem.title}</span>}
              <Maximize2 className="h-4 w-4 shrink-0 text-muted-foreground" />
            </button>
            <Dialog open={planningWorkspaceOpen} onOpenChange={setPlanningWorkspaceOpen}>
              <DialogContent className="flex h-[calc(100vh-2rem)] w-[calc(100vw-2rem)] max-w-none flex-col gap-3 overflow-hidden p-4 sm:max-w-none md:p-5">
                <DialogHeader className="shrink-0 pr-10">
                  <DialogTitle className="flex items-center gap-2 text-base"><Workflow className="h-4 w-4 text-primary" />{t.planningWorkspace}</DialogTitle>
                  <DialogDescription>{language === 'zh' ? '在不改变实时对话布局的独立工作区中查看任务提示词、计划清单、分支地图和 Agent 执行链。' : 'Inspect task prompts, plan checklist, branch map, and Agent execution chain without changing the live transcript layout.'}</DialogDescription>
                </DialogHeader>
                <div className="min-h-0 flex-1 overflow-y-auto rounded-md border border-border bg-muted/10 p-3 elegant-scrollbar md:p-4">
                {progressTasks.length > 0 && (
                  <div className="mb-3 flex min-w-0 items-center gap-2 overflow-x-auto rounded-md border border-border bg-background p-1.5 elegant-scrollbar">
                    <span className="shrink-0 px-1.5 text-[10px] font-medium uppercase text-muted-foreground">{language === 'zh' ? '任务' : 'Tasks'}</span>
                    {progressTasks.map((task) => {
                      const active = task.taskNumber === selectedProgressTask?.taskNumber;
                      const end = task.finishedAt || (activeOrchestrationStatus(task.status) ? Math.floor(Date.now() / 1000) : task.updatedAt);
                      const duration = task.createdAt && end && end >= task.createdAt ? formatDuration((end - task.createdAt) * 1000) : '';
                      return (
                        <button
                          key={task.taskNumber}
                          type="button"
                          onClick={() => setSelectedProgressTaskNumber(task.taskNumber)}
                          className={cn('flex h-8 shrink-0 items-center gap-2 rounded px-2.5 text-xs transition-colors', active ? 'bg-primary text-primary-foreground shadow-sm' : 'text-muted-foreground hover:bg-muted hover:text-foreground')}
                          aria-pressed={active}
                        >
                          <span>{language === 'zh' ? `任务 ${task.taskNumber}` : `Task ${task.taskNumber}`}</span>
                          <span className={cn('font-mono text-[10px] tabular-nums', active ? 'text-primary-foreground/75' : 'text-muted-foreground')}>{task.graphs.length} {language === 'zh' ? '轮' : task.graphs.length === 1 ? 'round' : 'rounds'}{duration ? ` · ${duration}` : ''}</span>
                        </button>
                      );
                    })}
                  </div>
                )}
                <div className="grid gap-3 xl:grid-cols-[minmax(15rem,0.42fr)_minmax(38rem,1.58fr)]">
                  <aside className="min-w-0 overflow-hidden rounded-md border border-border bg-background">
                    <div className="flex items-center gap-2 border-b border-border px-3 py-2.5 text-xs font-semibold text-muted-foreground"><BookOpen className="h-3.5 w-3.5" />{t.promptNavigator}</div>
                    <div className="flex max-h-28 gap-1 overflow-auto border-b border-border p-2 elegant-scrollbar xl:flex-col">
                      {promptViews.map((view) => (
                        <button key={view.key} type="button" onClick={() => setSelectedPromptKey(view.key)} className={cn('shrink-0 truncate rounded-md px-2.5 py-2 text-left text-xs transition-colors xl:w-full', selectedPromptView?.key === view.key ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground')} title={view.label}>{view.label}</button>
                      ))}
                    </div>
                    <div className="max-h-56 overflow-y-auto p-3 elegant-scrollbar"><pre className="whitespace-pre-wrap break-words font-sans text-xs leading-5 text-foreground">{selectedPromptView?.content || t.noRoundPrompts}</pre></div>
                  </aside>

                  <div className="min-w-0 space-y-3">
                    <section className="min-w-0 rounded-md border border-border bg-background p-3">
                      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                        <div><div className="flex items-center gap-2 text-sm font-semibold"><Workflow className="h-4 w-4 text-primary" />{language === 'zh' ? '任务分支地图' : 'Task branch map'}</div><p className="mt-1 text-[11px] text-muted-foreground">{language === 'zh' ? '整体路线、局部进度、依赖与推荐顺序。' : 'Overall route, local progress, dependencies, and recommended order.'}</p></div>
                        <div className="flex min-w-0 items-center gap-2">
                          {currentPlanItem && <span className="max-w-[20rem] truncate rounded border border-sky-500/20 bg-sky-500/[0.06] px-2 py-1 text-[10px] text-sky-700 dark:text-sky-300">{language === 'zh' ? '当前' : 'Focus'} · {currentPlanItem.title}</span>}
                          <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0 rounded" onClick={() => setTaskMapExpanded(true)} disabled={!projectedPlanItems.length} aria-label={language === 'zh' ? '展开任务分支地图' : 'Expand task branch map'} title={language === 'zh' ? '展开任务分支地图' : 'Expand task branch map'}><Maximize2 className="h-3.5 w-3.5" /></Button>
                        </div>
                      </div>
                      <OrchestrationProgressMap
                        tasks={projectedPlanItems.map((item, index) => ({ id: item.id, name: item.title, role: item.branch || item.kind, status: item.status, position: item.priority || index + 1, dependencies: item.dependsOn || [], detail: item.evidence || item.rationale }))}
                        activeTaskId={projectedPlan?.currentFocus}
                        height={Math.max(320, Math.min(520, Math.ceil(Math.max(projectedPlanItems.length, 3) / 3) * 128))}
                        ariaLabel={language === 'zh' ? '任务分支地图' : 'Task branch map'}
                        emptyLabel={isRunning ? (language === 'zh' ? '正在生成整个任务的中文计划…' : 'Generating the structured whole-task plan...') : (language === 'zh' ? '本次任务没有可用的结构化计划。' : 'No structured task plan is available for this run.')}
                        statusLabels={orchestrationStatusLabels(language, t)}
                        inferSequentialDependencies={false}
                        interactive
                      />
                    </section>

                    <div className="grid min-w-0 gap-3 2xl:grid-cols-[minmax(24rem,1.25fr)_minmax(26rem,1fr)]">
                      <OrchestrationProofProgress plan={projectedPlan} planItems={projectedPlanItems} labels={language === 'zh' ? { empty: isRunning ? '正在生成整个任务的中文计划…' : '本次任务没有可用的结构化计划。' } : { title: 'Whole-task plan', completed: 'Completed', active: 'Active', pending: 'Pending', blocked: 'Blocked', ready: 'Ready', empty: isRunning ? 'Generating the structured whole-task plan...' : 'No structured task plan is available for this run.', evidence: 'Evidence', rationale: 'Rationale', dependency: 'Blocked by', showCompleted: 'Show completed', hideCompleted: 'Hide completed' }} />
                      <section className="min-w-0 rounded-md border border-border bg-background p-3">
                        <div className="mb-2 flex items-start justify-between gap-3">
                          <div><div className="flex items-center gap-2 text-xs font-semibold"><Activity className="h-3.5 w-3.5 text-muted-foreground" />{language === 'zh' ? 'Agent 执行链（次级）' : 'Agent execution chain (secondary)'}</div><p className="mt-1 text-[10px] text-muted-foreground">{language === 'zh' ? '仅用于诊断运行阶段，不作为任务计划。' : 'Runtime diagnostics only; not the task plan.'}</p></div>
                          <div className="flex shrink-0 items-center gap-1">
                            {selectedAgentGraph && <span className="px-1 text-[10px] text-muted-foreground">{language === 'zh' ? `第 ${Math.max(1, (selectedProgressTask?.graphs || []).findIndex((graph) => graph.id === selectedAgentGraph.id) + 1)}/${Math.max(1, selectedProgressTask?.graphs?.length || 1)} 轮` : `Round ${Math.max(1, (selectedProgressTask?.graphs || []).findIndex((graph) => graph.id === selectedAgentGraph.id) + 1)}/${Math.max(1, selectedProgressTask?.graphs?.length || 1)}`}</span>}
                            <Button variant="ghost" size="icon" className="h-7 w-7 rounded" onClick={() => setAgentGraphExpanded(true)} disabled={!selectedAgentGraph} aria-label={language === 'zh' ? '放大执行链' : 'Expand execution chain'} title={language === 'zh' ? '放大执行链' : 'Expand execution chain'}><Maximize2 className="h-3.5 w-3.5" /></Button>
                          </div>
                        </div>
                        {(selectedProgressTask?.graphs?.length || 0) > 1 && (
                          <div className="mb-2 flex max-w-full gap-1 overflow-x-auto rounded border border-border bg-muted/20 p-1 elegant-scrollbar">
                            {selectedProgressTask?.graphs.map((graph, index) => <button key={graph.id} type="button" onClick={() => setSelectedProgressGraphId(graph.id)} className={cn('h-6 shrink-0 rounded px-2 text-[10px] transition-colors', selectedAgentGraph?.id === graph.id ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>{language === 'zh' ? `第 ${index + 1} 轮` : `Round ${index + 1}`}</button>)}
                          </div>
                        )}
                        <OrchestrationProgressMap
                          tasks={(selectedAgentGraph?.tasks || []).map((task) => ({ ...task, name: orchestrationTaskDisplayName(task.name, language), dependencies: task.dependencies || [] }))}
                          height={Math.max(320, Math.min(520, Math.ceil(Math.max(selectedAgentGraph?.tasks?.length || 0, 3) / 3) * 128))}
                          ariaLabel={language === 'zh' ? 'Agent 执行链' : 'Agent execution chain'}
                          emptyLabel={language === 'zh' ? '暂无执行节点。' : 'No execution nodes yet.'}
                          statusLabels={orchestrationStatusLabels(language, t)}
						  interactive
                        />
                      </section>
                    </div>
                  </div>
                </div>
                </div>
              </DialogContent>
            </Dialog>
          </section>
        )}

        <div className="grid flex-1 min-h-0 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_360px] lg:overflow-hidden">
          <div className="relative min-h-0">
            <div
              ref={scrollRef}
              onScroll={updateTimelineScrollState}
              className="h-full overflow-y-auto p-4 md:p-6 space-y-3 elegant-scrollbar"
            >
              {!timelineItems.length ? (
                <div className="h-full flex flex-col items-center justify-center text-center max-w-md mx-auto space-y-4">
                  <div className="h-12 w-12 rounded-2xl bg-primary/5 border border-border flex items-center justify-center">
                    <GitBranch className="h-6 w-6 text-primary" />
                  </div>
                  <h2 className="text-lg font-medium">{t.coordinateClaudeCodex}</h2>
                  <div className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-muted/40 px-2 py-1 text-xs text-muted-foreground">
                    <Server className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate">{selectedAgent?.name || t.noBridgeConnected}</span>
                    {!agentRuns.length && <span className="shrink-0">· {t.noOrchestrationRuns}</span>}
                  </div>
                  <p className="text-sm text-muted-foreground">{t.startCollaborationHint}</p>
                </div>
              ) : (
                <>
                  {hasOlderEvents && (
                    <div className="flex justify-center pb-1">
                      <Button
                        variant="secondary"
                        size="sm"
                        type="button"
                        className="gap-1.5 rounded-lg border border-border bg-background/80 text-muted-foreground shadow-sm hover:text-foreground"
                        onClick={() => loadOlderEvents().catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration))}
                        disabled={loadingOlderEvents}
                      >
                        <RefreshCw className={cn("h-3.5 w-3.5", loadingOlderEvents && "animate-spin")} />
                        {loadingOlderEvents ? t.loadingEarlierEvents : t.loadEarlierEvents}
                      </Button>
                    </div>
                  )}
                  {timelineGroups.map((group) => (
                    <OrchestrationTimelineGroupItem
                      key={group.key}
                      group={group}
                      collapsed={Boolean(collapsedTimelineGroups[group.key])}
                      onToggle={() => toggleTimelineGroup(group.key)}
                      onJumpToFirstMessage={(targetID) => jumpToTimelineItem(group.key, targetID)}
                      onApprovalDecision={respondOrchestrationApproval}
                      t={t}
                    />
                  ))}
                  {finalConclusion && !isRunning && <RunConclusionCard conclusion={finalConclusion} status={activeRun?.status} t={t} />}
                </>
              )}
              <div ref={endRef} className="h-4" />
            </div>

            {showScrollBottom && (
              <Button
                variant="secondary"
                size="icon"
                type="button"
                className="absolute bottom-4 left-1/2 z-20 h-9 w-9 -translate-x-1/2 rounded-full border border-border bg-card/95 text-muted-foreground shadow-lg backdrop-blur hover:text-foreground"
                onClick={() => scrollTimelineToBottom()}
                aria-label={t.jumpToLatestMessage}
                title={t.jumpToBottom}
              >
                <ArrowDownToLine className="h-4 w-4" />
              </Button>
            )}
          </div>

          <aside className="min-h-0 border-t border-border bg-background/95 p-4 overflow-y-auto elegant-scrollbar lg:border-l lg:border-t-0">
            <div className="flex min-h-full flex-col gap-3">
              <div className="space-y-3">
                <div className="space-y-2">
                  <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.mode}</label>
                  <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                    <button className={cn("h-8 rounded-md text-xs font-medium", mode === 'collaboration' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setMode('collaboration')} disabled={creating || isRunning}>
                      {t.collaborate}
                    </button>
                    <button className={cn("h-8 rounded-md text-xs font-medium", mode === 'debate' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setMode('debate')} disabled={creating || isRunning}>
                      {t.debate}
                    </button>
                  </div>
                </div>

                {renderWorkerPairSelector('panel')}
                {renderWorkerProfileSelectors()}

                <div className="space-y-2">
                  <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.firstCli}</label>
                  <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                    <button className={cn("h-8 rounded-md text-xs font-medium", workerPair !== 'codex-codex' && firstCli === 'claude' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setFirstCli('claude')} disabled={creating || isRunning || workerPair === 'codex-codex'}>
                      Claude
                    </button>
                    <button className={cn("h-8 rounded-md text-xs font-medium", (workerPair === 'codex-codex' || firstCli === 'codex') ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setFirstCli('codex')} disabled={creating || isRunning || workerPair === 'claude-claude'}>
                      Codex
                    </button>
                  </div>
                </div>

                <div className="space-y-2">
                  <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.profile}</label>
                  <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                    <button className={cn("h-8 rounded-md text-xs font-medium", profile === 'default' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setProfile('default')} disabled={creating || isRunning}>
                      {t.defaultProfile}
                    </button>
                    <button className={cn("h-8 rounded-md text-xs font-medium", profile === 'formal-proof' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setProfile('formal-proof')} disabled={creating || isRunning}>
                      {t.formalProofProfile}
                    </button>
                  </div>
                </div>

                <label className="flex flex-col gap-2">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.task}</span>
                  <textarea
                    ref={taskInputRef}
                    className="h-24 w-full resize-none overflow-y-auto rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring elegant-scrollbar"
                    placeholder={t.taskPlaceholder}
                    value={prompt}
                    onChange={(event) => setPrompt(event.target.value)}
                    disabled={creating || isRunning}
                  />
                </label>

                <label className="block shrink-0 space-y-2 sm:hidden">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.cliEndpoint}</span>
                  <AgentSelector
                    agents={agents}
                    selectedAgentId={selectedAgentId}
                    onSelect={selectAgent}
                    t={t}
                    className="w-full sm:hidden"
                    disabled={creating}
                    activeAgentIds={activeAgentIds}
                  />
                </label>

                <CapabilityMatrix agent={selectedAgent} t={t} />
                {capabilityProblems.length > 0 && (
                  <div className="flex items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                    <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span>{capabilityProblems.join(' ')}</span>
                  </div>
                )}

                <label className="block space-y-2">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.workingDirectory}</span>
                  <div className="relative">
                    <FolderInput className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                    <select
                      value={cwd}
                      onChange={(event) => setCwd(event.target.value)}
                      className={cn(
                        "flex h-9 w-full rounded-md border border-input bg-transparent py-1 pl-9 pr-8 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
                        !cwd && "text-muted-foreground"
                      )}
                      disabled={creating || isRunning || !workingDirs.length}
                      aria-label={t.workingDirectory}
                    >
                      {workingDirs.length ? workingDirs.map((dir) => (
                        <option key={dir} value={dir}>{dir}</option>
                      )) : (
                        <option value="">{t.noWorkingDirs}</option>
                      )}
                    </select>
                  </div>
                </label>

                <div className="grid gap-3 sm:grid-cols-[minmax(7rem,0.45fr)_minmax(10rem,0.55fr)]">
                  <label className="block space-y-2">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.turns}</span>
                    <select value={maxTurns} onChange={(event) => setMaxTurns(Number(event.target.value))} disabled={creating || isRunning} className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm">
                      {[1, 2, 3, 4, 6, 8, 12].map((turns) => <option key={turns} value={turns}>{turns} {turns === 1 ? 'round' : 'rounds'}</option>)}
                    </select>
                  </label>
                  <div className="space-y-2">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.nativeContextCompaction}</span>
                    <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                      <button className={cn("h-8 rounded-md text-xs font-medium", nativeContextCompaction === 'off' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setNativeContextCompaction('off')} disabled={creating || isRunning}>
                        {t.nativeContextCompactionOff}
                      </button>
                      <button className={cn("h-8 rounded-md text-xs font-medium", nativeContextCompaction === 'after-turn' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setNativeContextCompaction('after-turn')} disabled={creating || isRunning}>
                        {t.nativeContextCompactionAfterTurn}
                      </button>
                    </div>
                  </div>
                </div>
              </div>

              <div className="flex shrink-0 flex-col gap-2">
                <div className="flex shrink-0 items-center justify-between">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.files}</span>
                  <Button variant="ghost" size="sm" className="h-7 gap-1.5" onClick={() => fileInputRef.current?.click()} disabled={creating || isRunning}>
                    <FileUp className="h-3.5 w-3.5" />
                    {t.add}
                  </Button>
                </div>
                <input ref={fileInputRef} type="file" multiple className="hidden" onChange={(event) => addFiles(event.target.files).catch((err) => setError(err.message))} />
                <div className="space-y-2">
                  <section className="rounded-md border border-border/70 bg-background/40">
                    <div className="flex h-7 items-center justify-between border-b border-border/70 px-2">
                      <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{t.currentRunFiles}</span>
                      {activeRunFiles.length > 0 && <span className="text-[10px] text-muted-foreground">{activeRunFiles.length}</span>}
                    </div>
                    <div className="max-h-32 min-h-[3rem] space-y-1.5 overflow-y-auto p-1.5 elegant-scrollbar">
                      {activeRunFiles.length > 0 ? (
                        activeRunFiles.map((file, index) => (
                          <OrchestrationFileRow key={`${file.name}-${file.size}-${index}`} file={file} status={t.uploadedFileStatus} />
                        ))
                      ) : (
                        <div className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">{t.uploadProofFiles}</div>
                      )}
                    </div>
                  </section>
                  {files.length > 0 && (
                    <section className="rounded-md border border-border/70 bg-background/40">
                      <div className="flex h-7 items-center justify-between border-b border-border/70 px-2">
                        <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{t.pendingFiles}</span>
                        <span className="text-[10px] text-muted-foreground">{files.length}</span>
                      </div>
                      <div className="max-h-36 space-y-1.5 overflow-y-auto p-1.5 elegant-scrollbar">
                        {files.map((file) => (
                          <OrchestrationFileRow
                            key={file.id}
                            file={file}
                            status={t.pendingFileStatus}
                            onRemove={() => removeFile(file.id)}
                            removeLabel={`${t.removeFile} ${file.name}`}
                          />
                        ))}
                      </div>
                    </section>
                  )}
                </div>
              </div>

              {error && (
                <div className="flex shrink-0 items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{error}</span>
                </div>
              )}

              <div className="mt-auto flex shrink-0 items-center gap-2 pt-1">
                {isRunning ? (
                  <Button variant="secondary" className="w-full gap-2" onClick={() => cancelRun().catch((err) => setError(err.message))} disabled={!canCancelRun}>
                    {canCancelRun ? <Square className="h-3.5 w-3.5 fill-current" /> : <RefreshCw className="h-4 w-4 animate-spin" />}
                    {canCancelRun ? t.stopRun : t.stopping}
                  </Button>
                ) : (
                  <Button className="w-full gap-2" onClick={() => startRun()} disabled={!prompt.trim() || creating || !selectedAgent || capabilityProblems.length > 0 || (!activeRun && !workerProfilesComplete)}>
                    {creating ? <RefreshCw className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
                    {continuingRun ? t.continueRun : t.start}
                  </Button>
                )}
              </div>
            </div>
          </aside>
        </div>
      </main>

      <Dialog open={agentGraphExpanded} onOpenChange={setAgentGraphExpanded}>
        <DialogContent className="flex h-[calc(100vh-2rem)] w-[calc(100vw-2rem)] max-w-none flex-col gap-3 overflow-hidden p-4 sm:max-w-none md:p-5">
          <DialogHeader className="shrink-0 pr-10">
            <DialogTitle className="text-base">{language === 'zh' ? `任务 ${selectedProgressTask?.taskNumber || 1} · Agent 执行链` : `Task ${selectedProgressTask?.taskNumber || 1} · Agent execution chain`}</DialogTitle>
            <DialogDescription>{language === 'zh' ? `第 ${Math.max(1, (selectedProgressTask?.graphs || []).findIndex((graph) => graph.id === selectedAgentGraph?.id) + 1)}/${Math.max(1, selectedProgressTask?.graphs?.length || 1)} 轮 · 拖动画布，滚轮或双指缩放。` : `Round ${Math.max(1, (selectedProgressTask?.graphs || []).findIndex((graph) => graph.id === selectedAgentGraph?.id) + 1)}/${Math.max(1, selectedProgressTask?.graphs?.length || 1)} · Drag to pan and scroll or pinch to zoom.`}</DialogDescription>
          </DialogHeader>
          {(selectedProgressTask?.graphs?.length || 0) > 1 && <div className="flex shrink-0 gap-1 overflow-x-auto rounded-md border border-border bg-muted/20 p-1 elegant-scrollbar">{selectedProgressTask?.graphs.map((graph, index) => <button key={graph.id} type="button" onClick={() => setSelectedProgressGraphId(graph.id)} className={cn('h-7 shrink-0 rounded px-2.5 text-xs transition-colors', selectedAgentGraph?.id === graph.id ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>{language === 'zh' ? `第 ${index + 1} 轮` : `Round ${index + 1}`}</button>)}</div>}
          <div className="min-h-0 flex-1 overflow-hidden rounded-md border border-border">
            <OrchestrationProgressMap tasks={(selectedAgentGraph?.tasks || []).map((task) => ({ ...task, name: orchestrationTaskDisplayName(task.name, language), dependencies: task.dependencies || [] }))} height="100%" interactive ariaLabel={language === 'zh' ? '可缩放的 Agent 执行链' : 'Interactive Agent execution chain'} emptyLabel={language === 'zh' ? '暂无执行节点。' : 'No execution nodes yet.'} statusLabels={orchestrationStatusLabels(language, t)} />
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={taskMapExpanded} onOpenChange={setTaskMapExpanded}>
        <DialogContent className="flex h-[min(44rem,calc(100vh-5rem))] w-[min(68rem,calc(100vw-2rem))] max-w-none flex-col gap-3 overflow-hidden p-4 md:p-5">
          <DialogHeader className="shrink-0 pr-10">
            <DialogTitle className="flex items-center gap-2 text-base"><Workflow className="h-4 w-4 text-primary" />{language === 'zh' ? `任务 ${selectedProgressTask?.taskNumber || 1} · 任务分支地图` : `Task ${selectedProgressTask?.taskNumber || 1} · Task branch map`}</DialogTitle>
            <DialogDescription>{language === 'zh' ? '拖动画布，滚轮或双指缩放；使用右下角控件调整视图。' : 'Drag to pan, scroll or pinch to zoom, and use the lower-right controls to adjust the view.'}</DialogDescription>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-hidden rounded-md border border-border">
            <OrchestrationProgressMap
              tasks={projectedPlanItems.map((item, index) => ({ id: item.id, name: item.title, role: item.branch || item.kind, status: item.status, position: item.priority || index + 1, dependencies: item.dependsOn || [], detail: item.evidence || item.rationale }))}
              activeTaskId={projectedPlan?.currentFocus}
              height="100%"
              interactive
              ariaLabel={language === 'zh' ? '可缩放的任务分支地图' : 'Interactive task branch map'}
              emptyLabel={language === 'zh' ? '本次任务没有可用的结构化计划。' : 'No structured task plan is available for this run.'}
              statusLabels={orchestrationStatusLabels(language, t)}
              inferSequentialDependencies={false}
            />
          </div>
        </DialogContent>
      </Dialog>

      {settingsOpen && (
        <SettingsModal
          user={user}
          agents={agents}
          selectedAgentId={selectedAgentId}
          onSelectAgent={selectAgent}
          onAgentsChanged={async () => { await loadAgents(); }}
		  onPresetsChanged={refreshWorkerPresets}
          onLogout={logout}
          isDarkMode={isDarkMode}
          setIsDarkMode={setIsDarkMode}
          language={language}
          setLanguage={setLanguage}
          t={t}
          initialFocus={settingsFocus}
          close={() => setSettingsOpen(false)}
        />
      )}
    </div>
  );
}

function orchestrationRunPath(runId: string) {
  return `/orchestrate/runs/${encodeURIComponent(runId)}`;
}

function orchestrationRunIdFromPath(path: string) {
  const match = path.match(/^\/orchestrate\/runs\/([^/?#]+)/);
  return match ? decodeURIComponent(match[1]) : '';
}

function orchestrationTaskDisplayName(name: string, language: Language) {
  if (language !== 'zh') return name;
  const labels: Record<string, string> = {
    plan: '规划任务',
    'plan-review': '审核计划',
    'candidate-a': '候选方案 A',
    'candidate-b': '候选方案 B',
    integrate: '整合实现',
    review: '独立复核',
  };
  return labels[name] || name;
}

function orchestrationStatusLabels(language: Language, t: UIText) {
  if (language !== 'zh') {
    return {
      pending: 'Pending',
      ready: t.ready,
      dispatching: 'Starting',
      running: 'Running',
      in_progress: 'In progress',
      succeeded: 'Completed',
      completed: 'Completed',
      failed: 'Failed',
      blocked: 'Blocked',
      canceling: 'Canceling',
      canceled: 'Canceled',
      unknown: 'Unknown',
    };
  }
  return {
    pending: '待处理',
    ready: '可开始',
    dispatching: '启动中',
    running: '进行中',
    in_progress: '进行中',
    succeeded: '已完成',
    completed: '已完成',
    failed: '失败',
    blocked: '受阻',
    canceling: '取消中',
    canceled: '已取消',
    unknown: '状态未知',
  };
}

function maxOrchestrationEventSeq(events: OrchestrationEvent[], initial = 0) {
  return events.reduce((max, event) => {
    if (typeof event.seq !== 'number' || !Number.isFinite(event.seq)) return max;
    return Math.max(max, event.seq);
  }, initial);
}

function minOrchestrationEventSeq(events: OrchestrationEvent[], initial = 0) {
  return events.reduce((min, event) => {
    if (typeof event.seq !== 'number' || !Number.isFinite(event.seq)) return min;
    if (min <= 0) return event.seq;
    return Math.min(min, event.seq);
  }, initial);
}
