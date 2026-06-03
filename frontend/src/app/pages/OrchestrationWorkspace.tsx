import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  AlertCircle,
  ArrowDownToLine,
  ArrowLeft,
  Check,
  Command,
  FileUp,
  FolderInput,
  GitBranch,
  Plus,
  RefreshCw,
  Send,
  Server,
  Settings,
  Share2,
  ShieldQuestion,
  Square,
  Swords,
  UsersRound,
} from 'lucide-react';
import { api } from '../lib/api';
import type {
  Agent,
  ApprovalItemState,
  ApprovalRequest,
  Envelope,
  NativeContextCompaction,
  OrchestrationEvent,
  OrchestrationRun,
  ShareInfo,
  UploadAttachment,
  UserAccount,
} from '../lib/types';
import type { Language, UIText } from '../lib/i18n';
import { AgentSelector } from '../components/AgentSelector';
import { OrchestrationFileRow } from '../components/OrchestrationFiles';
import {
  CapabilityMatrix,
  OrchestrationTimelineGroupItem,
  defaultCollapsedTimelineGroups,
  reconcileCollapsedTimelineGroups,
} from '../components/OrchestrationComponents';
import { SettingsModal } from '../components/Settings';
import { Button, Input } from '../components/ui';
import {
  activeOrchestrationRunStorageKey,
  activeOrchestrationStatus,
  applyOrchestrationEventToRun,
  approvalStatusFromDecision,
  canCancelOrchestrationStatus,
  compareOrchestrationEvents,
  cn,
  copyText,
  forgetActiveOrchestrationRunForAgent,
  isNearBottom,
  mergeOrchestrationEvents,
  mergeOrchestrationFiles,
  orchestrationApprovalMode,
  orchestrationCapabilityProblems,
  orchestrationRunFilesFromEvents,
  orchestrationTimelineGroups,
  orchestrationTimelineItems,
  orchestrationTurnInfoFromEvents,
  orchestrationTurnLabel,
  orchestrationWorkerLabel,
  preferredAgentID,
  readActiveOrchestrationRunByAgent,
  readUploadAttachment,
  rememberActiveOrchestrationRunForAgent,
  sessionDateLabel,
  startWSHeartbeat,
  titleFromPrompt,
  updateApprovalItemStatus,
  upsertApprovalItem,
  upsertOrchestrationRun,
  visibleOrchestrationEvents,
} from '../lib/utils';

export function OrchestrationWorkspace({
  user,
  onLogout,
  isDarkMode,
  setIsDarkMode,
  language,
  setLanguage,
  t,
  canOpenMain,
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
  navigate: (path: string) => void;
}) {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [selectedAgentId, setSelectedAgentId] = useState(() => localStorage.getItem('codexBridge.selectedAgentId') || '');
  const [runs, setRuns] = useState<OrchestrationRun[]>([]);
  const [activeRunId, setActiveRunId] = useState('');
  const [events, setEvents] = useState<OrchestrationEvent[]>([]);
  const [approvals, setApprovals] = useState<ApprovalItemState[]>([]);
  const [mode, setMode] = useState<'collaboration' | 'debate'>('collaboration');
  const [firstCli, setFirstCli] = useState<'claude' | 'codex'>('claude');
  const [profile, setProfile] = useState<'default' | 'formal-proof'>('default');
  const [nativeContextCompaction, setNativeContextCompaction] = useState<NativeContextCompaction>('off');
  const [prompt, setPrompt] = useState('');
  const [cwd, setCwd] = useState('');
  const [maxTurns, setMaxTurns] = useState(4);
  const [files, setFiles] = useState<UploadAttachment[]>([]);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsFocus, setSettingsFocus] = useState<'cli' | ''>('');
  const [error, setError] = useState('');
  const [sharingRunId, setSharingRunId] = useState('');
  const [shareCopiedRunId, setShareCopiedRunId] = useState('');
  const [creating, setCreating] = useState(false);
  const [connectionStatus, setConnectionStatus] = useState(t.disconnected);
  const [showScrollBottom, setShowScrollBottom] = useState(false);
  const [collapsedTimelineGroups, setCollapsedTimelineGroups] = useState<Record<string, boolean>>({});
  const [refreshingOrchestration, setRefreshingOrchestration] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const activeRunIdRef = useRef('');
  const selectedAgentIdRef = useRef(selectedAgentId);
  const stickToBottomRef = useRef(true);
  const timelineOrderRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const taskInputRef = useRef<HTMLTextAreaElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const endRef = useRef<HTMLDivElement | null>(null);
  const collapsedTimelineRunIdRef = useRef('');
  const refreshOrchestrationInFlightRef = useRef<Promise<void> | null>(null);

  const selectedAgent = agents.find((agent) => agent.id === selectedAgentId) || null;
  const onlineAgent = selectedAgent?.online ? selectedAgent : agents.find((agent) => agent.online);
  const agentRuns = useMemo(() => {
    if (!selectedAgent?.id) return [];
    return runs.filter((run) => run.agentId === selectedAgent.id);
  }, [runs, selectedAgent?.id]);
  const activeRun = runs.find((run) => run.id === activeRunId && (!selectedAgent?.id || run.agentId === selectedAgent.id)) || null;
  const activeRunFiles = useMemo(() => {
    return activeRun ? mergeOrchestrationFiles(activeRun.files, orchestrationRunFilesFromEvents(events, activeRun.id)) : [];
  }, [activeRun, events]);
  const visibleEvents = useMemo(() => activeRun ? visibleOrchestrationEvents(events, activeRunId, activeRun, t) : [], [activeRun, events, activeRunId, t]);
  const isRunning = activeOrchestrationStatus(activeRun?.status);
  const currentTurnInfo = useMemo(() => activeRun ? orchestrationTurnInfoFromEvents(events, activeRun.id, activeRun.maxTurns, isRunning) : {}, [activeRun, events, isRunning]);
  const currentTurnLabel = useMemo(() => orchestrationTurnLabel(currentTurnInfo, t), [currentTurnInfo, t]);
  const visibleApprovals = useMemo(() => approvals.filter((item) => item.approval.runId === activeRunId), [approvals, activeRunId]);
  const timelineItems = useMemo(() => orchestrationTimelineItems(visibleEvents, visibleApprovals), [visibleEvents, visibleApprovals]);
  const timelineGroups = useMemo(() => orchestrationTimelineGroups(timelineItems, activeRun, events), [timelineItems, activeRun, events]);
  const orchestrationStreamStatus = activeRun && isRunning ? connectionStatus : t.idle;
  const continuingRun = Boolean(activeRun && !isRunning);
  const canCancelRun = canCancelOrchestrationStatus(activeRun?.status);
  const capabilityProblems = useMemo(() => orchestrationCapabilityProblems(selectedAgent, t), [selectedAgent, t]);
  const workingDirs = useMemo(() => {
    return Array.from(new Set((selectedAgent?.workingDirs || []).map((dir) => dir.trim()).filter(Boolean)));
  }, [selectedAgent]);

  const loadAgents = useCallback(async () => {
    const data = await api<{ agents: Agent[] }>('/api/agents');
    const nextAgents = data.agents || [];
    setAgents(nextAgents);
    setSelectedAgentId((current) => {
      const next = preferredAgentID(nextAgents, current);
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
    setSelectedAgentId((current) => {
      const next = preferredAgentID(nextAgents, current);
      selectedAgentIdRef.current = next;
      if (next) localStorage.setItem('codexBridge.selectedAgentId', next);
      else localStorage.removeItem('codexBridge.selectedAgentId');
      return next;
    });
  }, []);

  const loadRuns = useCallback(async () => {
    const data = await api<{ runs: OrchestrationRun[] }>('/api/orchestrations');
    setRuns(data.runs || []);
    return data.runs || [];
  }, []);

  const loadRun = useCallback(async (runId: string) => {
    const data = await api<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(runId)}`);
    setRuns((current) => upsertOrchestrationRun(current, data.run));
    return data.run;
  }, []);

  const loadRunEvents = useCallback(async (runId: string, replace = false) => {
    const data = await api<{ events: OrchestrationEvent[] }>(`/api/orchestrations/${encodeURIComponent(runId)}/events`);
    const incoming = data.events || [];
    if (replace) {
      timelineOrderRef.current = 0;
    }
    setEvents((current) => {
      if (activeRunIdRef.current !== runId) return current;
      return replace ? incoming.slice().sort(compareOrchestrationEvents) : mergeOrchestrationEvents(current, incoming);
    });
    return incoming;
  }, []);

  const scrollTimelineToBottom = useCallback((behavior: ScrollBehavior = 'smooth') => {
    const container = scrollRef.current;
    if (!container) return;
    const target = endRef.current;
    if (target) {
      target.scrollIntoView({ block: 'end', behavior });
    } else {
      container.scrollTo({ top: container.scrollHeight, behavior });
    }
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
  }, [timelineItems.length]);

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

  const clearActiveOrchestration = useCallback((forget = false) => {
    closeWS();
    if (forget) {
      const activeRun = runs.find((run) => run.id === activeRunIdRef.current);
      if (activeRun?.agentId) forgetActiveOrchestrationRunForAgent(activeRun.agentId, activeRun.id);
    }
    activeRunIdRef.current = '';
    setActiveRunId('');
    localStorage.removeItem(activeOrchestrationRunStorageKey);
    setEvents([]);
    setApprovals([]);
    timelineOrderRef.current = 0;
    setConnectionStatus(t.idle);
    setShowScrollBottom(false);
  }, [closeWS, runs, t.idle]);

  const applyEvent = useCallback((event: OrchestrationEvent) => {
    const nextEvent = { ...event, timelineOrder: typeof event.timelineOrder === 'number' ? event.timelineOrder : ++timelineOrderRef.current };
    setEvents((current) => {
      if (activeRunIdRef.current !== nextEvent.runId) return current;
      return mergeOrchestrationEvents(current, [nextEvent]);
    });
    setRuns((current) => {
      if (!current.some((run) => run.id === nextEvent.runId)) return current;
      return current
        .map((run) => run.id === nextEvent.runId ? applyOrchestrationEventToRun(run, nextEvent) : run)
        .sort((a, b) => (b.updatedAt || b.createdAt || 0) - (a.updatedAt || a.createdAt || 0));
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
      void loadRunEvents(runId).catch(() => undefined);
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
        void Promise.all([loadRun(runId), loadRunEvents(runId)])
          .then(([run]) => {
            if (activeRunIdRef.current === runId && activeOrchestrationStatus(run.status)) connectRun(runId);
          })
          .catch(() => {
            if (activeRunIdRef.current === runId) connectRun(runId);
          });
      }, delay);
    };
  }, [applyEvent, clearReconnect, closeWS, loadRun, loadRunEvents, startWSHeartbeat, t.connected, t.connecting, t.connectionError, t.disconnected]);

  const activateRun = useCallback(async (run: OrchestrationRun) => {
    const runAgentId = run.agentId || selectedAgentIdRef.current;
    timelineOrderRef.current = 0;
    activeRunIdRef.current = run.id;
    setActiveRunId(run.id);
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
    setFirstCli(run.firstCli === 'codex' ? 'codex' : 'claude');
    setProfile(run.profile === 'formal-proof' ? 'formal-proof' : 'default');
    setNativeContextCompaction(run.nativeContextCompaction === 'after-turn' ? 'after-turn' : 'off');
    setCwd(run.cwd || '');
    setMaxTurns(run.maxTurns || 4);
    stickToBottomRef.current = true;
    setShowScrollBottom(false);
    await loadRunEvents(run.id, true);
    if (activeRunIdRef.current !== run.id) return;
    if (activeOrchestrationStatus(run.status)) {
      connectRun(run.id);
    } else {
      closeWS();
      setConnectionStatus(t.idle);
    }
  }, [closeWS, connectRun, loadRunEvents, t.idle]);

  const selectRun = useCallback(async (runId: string) => {
    timelineOrderRef.current = 0;
    activeRunIdRef.current = runId;
    setActiveRunId(runId);
    setEvents((current) => current.filter((event) => event.runId === runId));
    setApprovals((current) => current.filter((item) => item.approval.runId === runId));
    const run = await loadRun(runId);
    if (activeRunIdRef.current !== runId) return;
    await activateRun(run);
  }, [activateRun, loadRun]);

  const respondOrchestrationApproval = useCallback((requestId: string, decision: 'accept' | 'decline' | 'cancel') => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN || !activeRunIdRef.current) return;
    wsRef.current.send(JSON.stringify({
      type: 'approval_response',
      payload: { requestId, decision },
    }));
    setApprovals((current) => updateApprovalItemStatus(current, requestId, approvalStatusFromDecision(decision)));
  }, []);

  const switchAgentRun = useCallback(async (agentId: string, availableRuns: OrchestrationRun[] = runs) => {
    if (!agentId) {
      clearActiveOrchestration();
      return;
    }
    const scopedRuns = availableRuns.filter((run) => run.agentId === agentId);
    const rememberedRunId = readActiveOrchestrationRunByAgent()[agentId] || '';
    const legacyRunId = localStorage.getItem(activeOrchestrationRunStorageKey) || '';
    const nextRun =
      scopedRuns.find((run) => run.id === rememberedRunId) ||
      scopedRuns.find((run) => run.id === legacyRunId) ||
      scopedRuns[0];
    if (!nextRun) {
      clearActiveOrchestration();
      forgetActiveOrchestrationRunForAgent(agentId);
      return;
    }
    activeRunIdRef.current = nextRun.id;
    setActiveRunId(nextRun.id);
    const loaded = await loadRun(nextRun.id);
    if (activeRunIdRef.current !== nextRun.id) return;
    await activateRun(loaded);
  }, [activateRun, clearActiveOrchestration, loadRun, runs]);

  const refreshOrchestration = useCallback(async () => {
    if (refreshOrchestrationInFlightRef.current) return refreshOrchestrationInFlightRef.current;
    const task = (async () => {
      setRefreshingOrchestration(true);
      try {
        const [loadedAgents, loadedRuns] = await Promise.all([loadAgents(), loadRuns()]);
        const savedAgentId = localStorage.getItem('codexBridge.selectedAgentId') || selectedAgentIdRef.current;
        const agentId = preferredAgentID(loadedAgents, savedAgentId);
        selectedAgentIdRef.current = agentId;
        setSelectedAgentId(agentId);
        if (agentId) localStorage.setItem('codexBridge.selectedAgentId', agentId);
        else localStorage.removeItem('codexBridge.selectedAgentId');
        const currentRun = loadedRuns.find((run) => run.id === activeRunIdRef.current);
        if (currentRun && (!agentId || currentRun.agentId === agentId)) {
          rememberActiveOrchestrationRunForAgent(currentRun.agentId, currentRun.id);
          return;
        }
        await switchAgentRun(agentId, loadedRuns);
      } finally {
        refreshOrchestrationInFlightRef.current = null;
        setRefreshingOrchestration(false);
      }
    })();
    refreshOrchestrationInFlightRef.current = task;
    return task;
  }, [loadAgents, loadRuns, switchAgentRun]);

  useEffect(() => {
    refreshOrchestration().catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration));
    return () => closeWS();
  }, []);

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
    if (!selectedAgent?.id) {
      clearActiveOrchestration();
      return;
    }
    const currentRun = runs.find((run) => run.id === activeRunIdRef.current);
    if (currentRun?.agentId === selectedAgent.id) return;
    switchAgentRun(selectedAgent.id).catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration));
  }, [clearActiveOrchestration, runs, selectedAgent?.id, switchAgentRun, t.failedLoadOrchestration]);

  useEffect(() => {
    if (!activeRunId || !activeOrchestrationStatus(activeRun?.status)) return;
    let stopped = false;
    const syncActiveRun = async () => {
      try {
        await Promise.all([loadRun(activeRunId), loadRunEvents(activeRunId)]);
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
    if (capabilityProblems.length > 0) {
      setError(capabilityProblems.join(' '));
      return;
    }
    setCreating(true);
    setError('');
    try {
      const endpoint = activeRun ? `/api/orchestrations/${encodeURIComponent(activeRun.id)}/prompts` : '/api/orchestrations';
      const data = await api<{ run: OrchestrationRun }>(endpoint, {
        method: 'POST',
        body: JSON.stringify({
          mode,
          firstCli,
          profile,
          nativeContextCompaction,
          prompt: task,
          title: titleFromPrompt(task, t),
          cwd: cwd.trim(),
          maxTurns,
          agentId: selectedAgent?.id || '',
          files: files.map(({ name, mimeType, size, data }) => ({ name, mimeType, size, data })),
        }),
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
    selectedAgentIdRef.current = agentId;
    setSelectedAgentId(agentId);
    if (agentId) localStorage.setItem('codexBridge.selectedAgentId', agentId);
    else localStorage.removeItem('codexBridge.selectedAgentId');
    switchAgentRun(agentId).catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration));
  };

  const openSettings = (focus: 'cli' | '' = '') => {
    setSettingsFocus(focus);
    setSettingsOpen(true);
  };

  const startDraftRun = () => {
    clearActiveOrchestration(true);
    setPrompt(t.reviewCurrentRepository);
    setFiles([]);
    setError('');
    window.setTimeout(() => taskInputRef.current?.focus(), 0);
  };

  const toggleTimelineGroup = (key: string) => {
    setCollapsedTimelineGroups((current) => ({ ...current, [key]: !current[key] }));
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
            <button
              key={run.id}
              onClick={() => selectRun(run.id).catch((err) => setError(err.message))}
              className={cn(
                "w-full text-left px-2 py-2 rounded-md text-sm transition-colors",
                activeRunId === run.id ? "bg-sidebar-accent text-sidebar-accent-foreground" : "text-sidebar-foreground hover:bg-sidebar-accent/50"
              )}
            >
              <div className="flex items-center gap-2">
                {run.mode === 'debate' ? <Swords className="h-3.5 w-3.5 opacity-70 shrink-0" /> : <UsersRound className="h-3.5 w-3.5 opacity-70 shrink-0" />}
                <span className="truncate font-medium">{run.title}</span>
                <span
                  className={cn(
                    "ml-auto inline-flex h-6 shrink-0 items-center gap-1 rounded border border-sidebar-border bg-sidebar px-1.5 text-[10px] font-medium text-muted-foreground hover:bg-sidebar-border",
                    shareCopiedRunId === run.id && "text-emerald-600 dark:text-emerald-400"
                  )}
                  onClick={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                    shareRun(run).catch((err) => setError(err.message));
                  }}
                  title={shareCopiedRunId === run.id ? t.copied : t.shareRun}
                >
                  {sharingRunId === run.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : shareCopiedRunId === run.id ? <Check className="h-3 w-3" /> : <Share2 className="h-3 w-3" />}
                  <span>{shareCopiedRunId === run.id ? t.copied : t.shareRun}</span>
                </span>
              </div>
              <div className="mt-1 flex items-center justify-between text-[10px] text-muted-foreground">
                <span>{sessionDateLabel(run.updatedAt || run.createdAt, t)}</span>
                <span>{run.status}</span>
              </div>
            </button>
          ))}
        </div>
        <div className="p-3 border-t border-sidebar-border shrink-0">
          <button onClick={() => openSettings()} className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-sm hover:bg-sidebar-accent transition-colors">
            <Settings className="h-3.5 w-3.5" />
            <span className="flex-1 text-left">{t.settings}</span>
            <div className={cn("h-1.5 w-1.5 rounded-full", onlineAgent ? "bg-emerald-500" : "bg-muted-foreground")} />
          </button>
        </div>
      </aside>

      <main className="flex-1 flex flex-col min-w-0 h-full">
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
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground rounded-full h-8 w-8"
              onClick={() => refreshOrchestration().catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration))}
              disabled={refreshingOrchestration}
              aria-label={t.refresh}
              title={t.refresh}
            >
              <RefreshCw className={cn("h-4 w-4", refreshingOrchestration && "animate-spin")} />
            </Button>
          </div>
        </header>

        <div className="bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          <AgentSelector
            agents={agents}
            selectedAgentId={selectedAgentId}
            onSelect={selectAgent}
            t={t}
            className="sm:hidden min-w-[220px] shrink-0"
            disabled={creating}
          />
          <div className="flex items-center gap-1.5">
            <Server className="h-3.5 w-3.5" />
            <span>{t.workers}: {orchestrationWorkerLabel(selectedAgent, t)} · {selectedAgent?.online ? t.online : t.offline}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <ShieldQuestion className="h-3.5 w-3.5" />
            <span>{t.browserApproval}: {orchestrationApprovalMode(selectedAgent) === 'auto-execute' ? t.autoExecute : capabilityProblems.length ? t.notAvailable : t.available}</span>
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
                  {timelineGroups.map((group) => (
                    <OrchestrationTimelineGroupItem
                      key={group.key}
                      group={group}
                      collapsed={Boolean(collapsedTimelineGroups[group.key])}
                      onToggle={() => toggleTimelineGroup(group.key)}
                      onApprovalDecision={respondOrchestrationApproval}
                      t={t}
                    />
                  ))}
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
                    <button className={cn("h-8 rounded-md text-xs font-medium", mode === 'collaboration' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setMode('collaboration')}>
                      {t.collaborate}
                    </button>
                    <button className={cn("h-8 rounded-md text-xs font-medium", mode === 'debate' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setMode('debate')}>
                      {t.debate}
                    </button>
                  </div>
                </div>

                <div className="space-y-2">
                  <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.firstCli}</label>
                  <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                    <button className={cn("h-8 rounded-md text-xs font-medium", firstCli === 'claude' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setFirstCli('claude')} disabled={creating || isRunning}>
                      Claude
                    </button>
                    <button className={cn("h-8 rounded-md text-xs font-medium", firstCli === 'codex' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setFirstCli('codex')} disabled={creating || isRunning}>
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
                    <Input type="number" min={2} max={12} value={maxTurns} onChange={(event) => setMaxTurns(Number(event.target.value) || 4)} disabled={creating || isRunning} />
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
                  <Button className="w-full gap-2" onClick={() => startRun()} disabled={!prompt.trim() || creating || !selectedAgent || capabilityProblems.length > 0}>
                    {creating ? <RefreshCw className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
                    {continuingRun ? t.continueRun : t.start}
                  </Button>
                )}
              </div>
            </div>
          </aside>
        </div>
      </main>

      {settingsOpen && (
        <SettingsModal
          user={user}
          agents={agents}
          selectedAgentId={selectedAgentId}
          onSelectAgent={selectAgent}
          onAgentsChanged={loadAgents}
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
