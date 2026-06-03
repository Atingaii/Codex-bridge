import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  ArrowDownToLine,
  Check,
  Command,
  GitBranch,
  ImagePlus,
  Menu,
  PanelLeft,
  PanelLeftClose,
  Plus,
  RefreshCw,
  Send,
  Server,
  Share2,
  Square,
  Terminal,
  X,
} from 'lucide-react';
import { api } from '../lib/api';
import type {
  Agent,
  ApprovalRequest,
  ChatItem,
  Envelope,
  ImageAttachment,
  Message,
  Run,
  Session,
  ShareInfo,
  ToolEvent,
  UserAccount,
} from '../lib/types';
import type { Language, UIText } from '../lib/i18n';
import { AgentSelector } from '../components/AgentSelector';
import { ApprovalCard } from '../components/chat/ApprovalCard';
import { TakeoverHint } from '../components/chat/TakeoverHint';
import { MessageItem, ToolItem } from '../components/chat/MessageItem';
import { RenameSessionModal, SettingsModal } from '../components/Settings';
import { SidebarContent } from '../components/SidebarContent';
import { Button } from '../components/ui';
import {
  activeStatus,
  approvalStatusFromDecision,
  cn,
  copyText,
  displaySessionTitle,
  forgetActiveSessionForAgent,
  isNearBottom,
  newID,
  preferredAgentID,
  readActiveSessionByAgent,
  readImageAttachment,
  rememberActiveSessionForAgent,
  sessionDateLabel,
  startWSHeartbeat,
  titleFromPrompt,
  waitForOpen,
} from '../lib/utils';

export function Workspace({
  user,
  onLogout,
  isDarkMode,
  setIsDarkMode,
  language,
  setLanguage,
  t,
  navigate,
}: {
  user: UserAccount;
  onLogout: () => void;
  isDarkMode: boolean;
  setIsDarkMode: (value: boolean) => void;
  language: Language;
  setLanguage: (value: Language) => void;
  t: UIText;
  navigate: (path: string) => void;
}) {
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [activeSessionId, setActiveSessionId] = useState('');
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsFocus, setSettingsFocus] = useState<'cli' | ''>('');
  const [inputVal, setInputVal] = useState('');
  const [attachments, setAttachments] = useState<ImageAttachment[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [selectedAgentId, setSelectedAgentId] = useState(() => localStorage.getItem('codexBridge.selectedAgentId') || '');
  const [sessions, setSessions] = useState<Session[]>([]);
  const [items, setItems] = useState<ChatItem[]>([]);
  const [runner, setRunner] = useState('-');
  const [thread, setThread] = useState('-');
  // Native local-takeover info for ACP-backed sessions (target B).
  const [nativeResumeCommand, setNativeResumeCommand] = useState('');
  const [nativeResumeId, setNativeResumeId] = useState('');
  const [takeoverCopied, setTakeoverCopied] = useState(false);
  const [connectionStatus, setConnectionStatus] = useState(t.disconnected);
  const [activeRun, setActiveRun] = useState<Run | null>(null);
  const [search, setSearch] = useState('');
  const [renameTarget, setRenameTarget] = useState<Session | null>(null);
  const [renameDraft, setRenameDraft] = useState('');
  const [renameError, setRenameError] = useState('');
  const [renaming, setRenaming] = useState(false);
  const [sharingSessionId, setSharingSessionId] = useState('');
  const [shareCopiedSessionId, setShareCopiedSessionId] = useState('');
  const [showScrollBottom, setShowScrollBottom] = useState(false);
  const [refreshingAll, setRefreshingAll] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const messageScrollRef = useRef<HTMLDivElement | null>(null);
  const messageEndRef = useRef<HTMLDivElement | null>(null);
  const stickToBottomRef = useRef(true);
  const activeSessionIdRef = useRef('');
  const selectedAgentIdRef = useRef(selectedAgentId);
  const assistantItemIdRef = useRef<string | null>(null);
  const assistantTextRef = useRef('');
  const refreshAllInFlightRef = useRef<Promise<void> | null>(null);

  const activeSession = sessions.find((session) => session.id === activeSessionId) || null;
  const selectedAgent = agents.find((agent) => agent.id === selectedAgentId) || null;
  const onlineAgent = selectedAgent?.online ? selectedAgent : agents.find((agent) => agent.online);
  const isGenerating = Boolean(activeRun && activeStatus(activeRun.status));
  const agentSessions = useMemo(() => {
    if (!selectedAgent?.id) return [];
    return sessions.filter((session) => session.agentId === selectedAgent.id);
  }, [sessions, selectedAgent?.id]);

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

  const loadSessions = useCallback(async () => {
    const data = await api<{ sessions: Session[] }>('/api/sessions');
    setSessions(data.sessions || []);
    return data.sessions || [];
  }, []);

  const appendSystem = useCallback((content: string) => {
    setItems((current) => [...current, { id: newID('sys'), type: 'message', role: 'system', content, createdAt: Math.floor(Date.now() / 1000) }]);
  }, []);

  const closeWS = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
  }, []);

  const loadMessages = useCallback(async (sessionId: string) => {
    const data = await api<{ messages: Message[] }>(`/api/sessions/${encodeURIComponent(sessionId)}/messages`);
    if (activeSessionIdRef.current !== sessionId) return;
    setItems((data.messages || []).map((message) => ({
      id: message.id,
      type: 'message',
      role: message.role,
      content: message.content,
      createdAt: message.createdAt,
    })));
    assistantItemIdRef.current = null;
    assistantTextRef.current = '';
  }, []);

  const scrollMessagesToBottom = useCallback((behavior: ScrollBehavior = 'smooth') => {
    const container = messageScrollRef.current;
    if (!container) return;
    const target = messageEndRef.current;
    if (target) {
      target.scrollIntoView({ block: 'end', behavior });
    } else {
      container.scrollTo({ top: container.scrollHeight, behavior });
    }
    stickToBottomRef.current = true;
    setShowScrollBottom(false);
  }, []);

  const updateMessageScrollState = useCallback(() => {
    const container = messageScrollRef.current;
    if (!container) {
      setShowScrollBottom(false);
      return;
    }
    const nearBottom = isNearBottom(container);
    stickToBottomRef.current = nearBottom;
    setShowScrollBottom(items.length > 0 && !nearBottom);
  }, [items.length]);

  const loadRuns = useCallback(async (sessionId: string) => {
    const data = await api<{ runs: Run[] }>(`/api/sessions/${encodeURIComponent(sessionId)}/runs`);
    if (activeSessionIdRef.current !== sessionId) return;
    setActiveRun((data.runs || []).find((run) => activeStatus(run.status)) || null);
  }, []);

  const clearActiveChat = useCallback(() => {
    closeWS();
    activeSessionIdRef.current = '';
    assistantItemIdRef.current = null;
    assistantTextRef.current = '';
    setActiveSessionId('');
    setItems([]);
    setRunner('-');
    setThread('-');
    setNativeResumeCommand('');
    setNativeResumeId('');
    setTakeoverCopied(false);
    setActiveRun(null);
    setConnectionStatus(t.disconnected);
    setShowScrollBottom(false);
  }, [closeWS, t.disconnected]);

  const touchSession = useCallback((sessionId: string) => {
    setSessions((current) => {
      const session = current.find((item) => item.id === sessionId);
      if (!session) return current;
      const updated = { ...session, updatedAt: Math.floor(Date.now() / 1000) };
      return [updated, ...current.filter((item) => item.id !== sessionId)];
    });
  }, []);

  // captureNativeResume reads the optional native local-takeover fields that an
  // ACP-backed Bridge attaches to session_opened / prompt_complete, updates the
  // takeover UI, and persists the native resume id onto the session record so a
  // reopen can rehydrate the hint without waiting for the next turn.
  const captureNativeResume = useCallback((payload: any) => {
    const command = typeof payload?.nativeResumeCommand === 'string' ? payload.nativeResumeCommand : '';
    const nativeId = typeof payload?.nativeResumeId === 'string' ? payload.nativeResumeId : '';
    setNativeResumeCommand(command);
    setNativeResumeId(nativeId);
    setTakeoverCopied(false);
    if (nativeId && activeSessionIdRef.current) {
      const sid = activeSessionIdRef.current;
      setSessions((current) => current.map((item) => item.id === sid ? { ...item, nativeResumeId: nativeId } : item));
    }
  }, []);

  const handleEnvelope = useCallback((env: Envelope) => {
    if (!env.sid || env.sid !== activeSessionIdRef.current) return;
    const payload = env.payload || {};

    switch (env.type) {
      case 'status':
        setConnectionStatus(payload.status ? String(payload.status) : t.connected);
        if (payload.runId) {
          setActiveRun({ id: payload.runId, promptId: payload.promptId, status: payload.status || 'running' });
        }
        if (payload.status === 'canceling') {
          setActiveRun((current) => current ? { ...current, status: 'canceling' } : current);
        }
        break;
      case 'session_opened':
        setRunner(payload.runner || '-');
        setThread(payload.remoteThreadId || '-');
        captureNativeResume(payload);
        setConnectionStatus(t.ready);
        break;
      case 'session_update':
        if (payload.runId) {
          setActiveRun((current) => ({
            id: payload.runId,
            promptId: payload.promptId,
            status: current?.status === 'canceling' ? 'canceling' : 'running',
          }));
        }
        if (payload.tool) {
          const tool = payload.tool as ToolEvent;
          const id = tool.id || tool.command || newID('tool');
          setItems((current) => {
            const existing = current.findIndex((item) => item.type === 'tool' && item.id === id);
            const next: ChatItem = { id, type: 'tool', tool };
            if (existing === -1) return [...current, next];
            return current.map((item, index) => index === existing ? next : item);
          });
        }
        if (payload.content) {
          const content = String(payload.content);
          if (!assistantItemIdRef.current) assistantItemIdRef.current = newID('msg');
          assistantTextRef.current = content;
          const id = assistantItemIdRef.current;
          setItems((current) => upsertAssistant(current, id, content));
        } else if (payload.delta) {
          if (!assistantItemIdRef.current) assistantItemIdRef.current = newID('msg');
          assistantTextRef.current += String(payload.delta);
          const id = assistantItemIdRef.current;
          const content = assistantTextRef.current;
          setItems((current) => upsertAssistant(current, id, content));
        }
        break;
      case 'approval_request':
        if (payload.requestId) {
          const approval = payload as ApprovalRequest;
          setItems((current) => {
            const existing = current.findIndex((item) => item.type === 'approval' && item.approval.requestId === approval.requestId);
            const next: ChatItem = { id: approval.requestId, type: 'approval', approval, status: 'pending' };
            if (existing === -1) return [...current, next];
            return current.map((item, index) => index === existing ? next : item);
          });
        }
        break;
      case 'prompt_complete':
        if (payload.content) {
          if (!assistantItemIdRef.current) assistantItemIdRef.current = newID('msg');
          assistantTextRef.current = String(payload.content);
          const id = assistantItemIdRef.current;
          setItems((current) => upsertAssistant(current, id, assistantTextRef.current));
        }
        setThread(payload.remoteThreadId || thread || '-');
        captureNativeResume(payload);
        setActiveRun(null);
        assistantItemIdRef.current = null;
        assistantTextRef.current = '';
        setConnectionStatus(t.ready);
        if (activeSessionIdRef.current) touchSession(activeSessionIdRef.current);
        break;
      case 'error':
        if (payload.code === 'SESSION_DELETED') {
          clearActiveChat();
          return;
        }
        appendSystem(payload.message || payload.code || t.error);
        setActiveRun(null);
        setConnectionStatus(payload.code || t.error);
        break;
      default:
        break;
    }
  }, [appendSystem, captureNativeResume, clearActiveChat, t.connected, t.error, t.ready, thread, touchSession]);

  const connectWS = useCallback((sessionId: string) => {
    closeWS();
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${scheme}://${location.host}/ws/chat?sid=${encodeURIComponent(sessionId)}`);
    wsRef.current = ws;
    setConnectionStatus(t.connecting);
    let stopHeartbeat: (() => void) | null = null;
    ws.onopen = () => {
      if (activeSessionIdRef.current !== sessionId || wsRef.current !== ws) return;
      setConnectionStatus(t.connected);
      stopHeartbeat = startWSHeartbeat(ws, sessionId);
    };
    ws.onmessage = (event) => {
      if (activeSessionIdRef.current !== sessionId || wsRef.current !== ws) return;
      try {
        handleEnvelope(JSON.parse(event.data));
      } catch {
        // Ignore malformed frames.
      }
    };
    ws.onerror = () => {
      if (activeSessionIdRef.current === sessionId && wsRef.current === ws) setConnectionStatus(t.connectionError);
    };
    ws.onclose = () => {
      stopHeartbeat?.();
      if (activeSessionIdRef.current === sessionId) setConnectionStatus(t.disconnected);
    };
    return ws;
  }, [closeWS, handleEnvelope, startWSHeartbeat, t.connected, t.connecting, t.connectionError, t.disconnected]);

  const selectSession = useCallback(async (sessionId: string) => {
    const session = sessions.find((item) => item.id === sessionId);
    if (!session) {
      clearActiveChat();
      return;
    }
    if (session.agentId !== selectedAgentIdRef.current) {
      selectedAgentIdRef.current = session.agentId;
      setSelectedAgentId(session.agentId);
      localStorage.setItem('codexBridge.selectedAgentId', session.agentId);
    }
    rememberActiveSessionForAgent(session.agentId, session.id);
    stickToBottomRef.current = true;
    setShowScrollBottom(false);
    setActiveSessionId(sessionId);
    activeSessionIdRef.current = sessionId;
    setRunner('-');
    setThread(session.remoteThreadId || '-');
    // Hydrate the takeover hint from the stored native id until the next
    // session_opened / prompt_complete refreshes it (command is re-sent then).
    setNativeResumeId(session.nativeResumeId || '');
    setNativeResumeCommand('');
    setTakeoverCopied(false);
    setActiveRun(null);
    setMobileMenuOpen(false);
    await loadMessages(sessionId);
    await loadRuns(sessionId);
    if (activeSessionIdRef.current !== sessionId) return;
    connectWS(sessionId);
  }, [clearActiveChat, connectWS, loadMessages, loadRuns, sessions]);

  const selectLoadedSession = useCallback(async (session: Session) => {
    rememberActiveSessionForAgent(session.agentId, session.id);
    stickToBottomRef.current = true;
    setShowScrollBottom(false);
    setActiveSessionId(session.id);
    activeSessionIdRef.current = session.id;
    setRunner('-');
    setThread(session.remoteThreadId || '-');
    setNativeResumeId(session.nativeResumeId || '');
    setNativeResumeCommand('');
    setTakeoverCopied(false);
    setActiveRun(null);
    setMobileMenuOpen(false);
    await loadMessages(session.id);
    await loadRuns(session.id);
    if (activeSessionIdRef.current !== session.id) return;
    connectWS(session.id);
  }, [connectWS, loadMessages, loadRuns]);

  const switchAgentSession = useCallback(async (agentId: string, availableSessions: Session[] = sessions) => {
    if (!agentId) {
      clearActiveChat();
      return;
    }
    const scoped = availableSessions.filter((session) => session.agentId === agentId);
    const remembered = readActiveSessionByAgent()[agentId];
    const next = scoped.find((session) => session.id === remembered) || scoped[0];
    if (next) {
      await selectLoadedSession(next);
    } else {
      clearActiveChat();
      forgetActiveSessionForAgent(agentId);
    }
  }, [clearActiveChat, selectLoadedSession, sessions]);

  const refreshAll = useCallback(async () => {
    if (refreshAllInFlightRef.current) return refreshAllInFlightRef.current;
    const task = (async () => {
      setRefreshingAll(true);
      try {
        const [loadedAgents, loadedSessions] = await Promise.all([loadAgents(), loadSessions()]);
        const savedAgentId = localStorage.getItem('codexBridge.selectedAgentId') || selectedAgentIdRef.current;
        const agentId = preferredAgentID(loadedAgents, savedAgentId);
        selectedAgentIdRef.current = agentId;
        const activeSession = loadedSessions.find((session) => session.id === activeSessionIdRef.current);
        if (activeSession && (!agentId || activeSession.agentId === agentId)) {
          return;
        }
        await switchAgentSession(agentId, loadedSessions);
      } finally {
        refreshAllInFlightRef.current = null;
        setRefreshingAll(false);
      }
    })();
    refreshAllInFlightRef.current = task;
    return task;
  }, [loadAgents, loadSessions, switchAgentSession]);

  useEffect(() => {
    refreshAll().catch((err) => appendSystem(err.message));
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
    activeSessionIdRef.current = activeSessionId;
  }, [activeSessionId]);

  useEffect(() => {
    selectedAgentIdRef.current = selectedAgentId;
  }, [selectedAgentId]);

  useEffect(() => {
    const frame = window.requestAnimationFrame(() => {
      const container = messageScrollRef.current;
      if (!container) return;
      if (stickToBottomRef.current) {
        scrollMessagesToBottom('auto');
        return;
      }
      setShowScrollBottom(items.length > 0 && !isNearBottom(container));
    });
    return () => window.cancelAnimationFrame(frame);
  }, [activeSessionId, items, scrollMessagesToBottom]);

  const createSession = async (title = t.newSession) => {
    const agent = selectedAgent;
    if (!agent) {
      appendSystem(t.noBridgeConnected);
      return;
    }
    const data = await api<{ session: Session }>('/api/sessions', {
      method: 'POST',
      body: JSON.stringify({ agentId: agent.id, title }),
    });
    setSessions((current) => [data.session, ...current.filter((session) => session.id !== data.session.id)]);
    await selectLoadedSession(data.session);
  };

  useEffect(() => {
    if (!selectedAgent?.id) {
      clearActiveChat();
      return;
    }
    const activeSession = sessions.find((session) => session.id === activeSessionIdRef.current);
    if (activeSession?.agentId === selectedAgent.id) return;
    switchAgentSession(selectedAgent.id).catch((err) => appendSystem(err.message));
  }, [appendSystem, clearActiveChat, selectedAgent?.id, sessions, switchAgentSession]);

  const renameSession = (session: Session) => {
    setRenameTarget(session);
    setRenameDraft(displaySessionTitle(session, t));
    setRenameError('');
  };

  const closeRenameModal = () => {
    if (renaming) return;
    setRenameTarget(null);
    setRenameDraft('');
    setRenameError('');
  };

  const saveRenameSession = async () => {
    if (!renameTarget) return;
    const title = renameDraft.trim();
    if (!title) {
      setRenameError(t.sessionNameRequired);
      return;
    }
    if (title === displaySessionTitle(renameTarget, t)) {
      closeRenameModal();
      return;
    }
    setRenaming(true);
    setRenameError('');
    try {
      const data = await api<{ session: Session }>(`/api/sessions/${encodeURIComponent(renameTarget.id)}`, {
        method: 'PATCH',
        body: JSON.stringify({ title }),
      });
      setSessions((current) => current.map((item) => item.id === data.session.id ? data.session : item));
      setRenameTarget(null);
      setRenameDraft('');
    } catch (err) {
      setRenameError(err instanceof Error ? err.message : t.failedRenameSession);
    } finally {
      setRenaming(false);
    }
  };

  const deleteSession = async (session: Session) => {
    if (!window.confirm(t.deleteSessionConfirm)) return;
    await api(`/api/sessions/${encodeURIComponent(session.id)}`, { method: 'DELETE' });
    const remaining = sessions.filter((item) => item.id !== session.id);
    setSessions(remaining);
    if (activeSessionId === session.id) {
      const sameAgent = remaining.filter((item) => item.agentId === session.agentId);
      forgetActiveSessionForAgent(session.agentId, session.id);
      if (sameAgent[0]) {
        await selectLoadedSession(sameAgent[0]);
      } else {
        clearActiveChat();
      }
    }
  };

  const shareSession = async (session: Session | null) => {
    if (!session || sharingSessionId) return;
    setSharingSessionId(session.id);
    try {
      const data = await api<{ share: ShareInfo }>(`/api/sessions/${encodeURIComponent(session.id)}/share`, { method: 'POST', body: '{}' });
      const url = data.share.url || `${window.location.origin}/share/${data.share.id}`;
      await copyText(url);
      setShareCopiedSessionId(session.id);
      window.setTimeout(() => setShareCopiedSessionId(''), 1400);
    } catch (err) {
      appendSystem(err instanceof Error ? `${t.failedCreateShare}: ${err.message}` : t.failedCreateShare);
    } finally {
      setSharingSessionId('');
    }
  };

  const addImages = async (files: FileList | null) => {
    if (!files?.length) return;
    const next = await Promise.all(Array.from(files).map(readImageAttachment));
    setAttachments((current) => [...current, ...next].slice(0, 4));
    if (fileInputRef.current) fileInputRef.current.value = '';
  };

  const removeAttachment = (id: string) => {
    setAttachments((current) => {
      const target = current.find((item) => item.id === id);
      if (target) URL.revokeObjectURL(target.previewUrl);
      return current.filter((item) => item.id !== id);
    });
  };

  const sendPrompt = async () => {
    const text = inputVal.trim();
    if ((!text && !attachments.length) || isGenerating) return;
    let sessionId = activeSessionId;
    const promptText = text || t.analyzeUploadedImage;
    const wasUntitled = !activeSession || activeSession.title === 'New chat' || activeSession.title === 'New Session' || activeSession.title === t.newSession;
    if (!sessionId) {
      await createSession(titleFromPrompt(promptText, t));
      sessionId = activeSessionIdRef.current;
    }
    if (!sessionId) return;
    const ws = wsRef.current?.readyState === WebSocket.OPEN ? wsRef.current : connectWS(sessionId);
    await waitForOpen(ws);
    setInputVal('');
    setAttachments([]);
    const promptId = newID('prm');
    const userContent = attachments.length
      ? `${promptText}\n\n${attachments.map((item) => `![${item.name}](${item.previewUrl})`).join('\n')}`
      : promptText;
    stickToBottomRef.current = true;
    setItems((current) => [...current, { id: promptId, type: 'message', role: 'user', content: userContent, createdAt: Math.floor(Date.now() / 1000) }]);
    assistantItemIdRef.current = null;
    assistantTextRef.current = '';
    setActiveRun({ id: '', promptId, status: 'running' });
    if (wasUntitled && promptText) {
      api<{ session: Session }>(`/api/sessions/${encodeURIComponent(sessionId)}`, {
        method: 'PATCH',
        body: JSON.stringify({ title: titleFromPrompt(promptText, t) }),
      })
        .then((data) => setSessions((current) => current.map((item) => item.id === data.session.id ? data.session : item)))
        .catch(() => undefined);
    }
    ws.send(JSON.stringify({
      type: 'prompt',
      sid: sessionId,
      payload: {
        content: promptText,
        promptId,
        attachments: attachments.map(({ name, mimeType, size, data }) => ({ name, mimeType, size, data })),
      },
    }));
  };

  const respondApproval = (requestId: string, decision: 'accept' | 'decline' | 'cancel') => {
    if (!wsRef.current || !activeSessionId) return;
    wsRef.current.send(JSON.stringify({
      type: 'approval_response',
      sid: activeSessionId,
      payload: { requestId, decision },
    }));
    setItems((current) => current.map((item) => {
      if (item.type !== 'approval' || item.approval.requestId !== requestId) return item;
      return { ...item, status: approvalStatusFromDecision(decision) };
    }));
  };

  const stopRun = () => {
    if (!wsRef.current || !activeSessionId) return;
    setActiveRun((current) => current ? { ...current, status: 'canceling' } : current);
    wsRef.current.send(JSON.stringify({ type: 'cancel', sid: activeSessionId }));
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
    switchAgentSession(agentId).catch((err) => appendSystem(err.message));
  };

  const openSettings = (focus: 'cli' | '' = '') => {
    setSettingsFocus(focus);
    setSettingsOpen(true);
  };

  const groupedSessions = useMemo(() => {
    const query = search.trim().toLowerCase();
    return agentSessions
      .filter((session) => !query || displaySessionTitle(session, t).toLowerCase().includes(query))
      .reduce((acc, session) => {
        const label = sessionDateLabel(session.updatedAt || session.createdAt, t);
        if (!acc[label]) acc[label] = [];
        acc[label].push(session);
        return acc;
      }, {} as Record<string, Session[]>);
  }, [agentSessions, search]);

  return (
    <div className="h-screen w-full flex bg-background text-foreground overflow-hidden font-sans">
      <aside
        className={cn(
          "hidden md:flex flex-col border-r border-sidebar-border bg-sidebar transition-all duration-300 ease-in-out",
          sidebarOpen ? "w-[260px]" : "w-0 opacity-0 overflow-hidden border-r-0"
        )}
      >
        <SidebarContent
          groupedSessions={groupedSessions}
          activeSession={activeSessionId}
          setActiveSession={(id) => selectSession(id).catch((err) => appendSystem(err.message))}
          createSession={() => createSession().catch((err) => appendSystem(err.message))}
          shareSession={(session) => shareSession(session).catch((err) => appendSystem(err.message))}
          renameSession={renameSession}
          deleteSession={(session) => deleteSession(session).catch((err) => appendSystem(err.message))}
          search={search}
          setSearch={setSearch}
          openSettings={() => openSettings()}
          agentOnline={Boolean(onlineAgent)}
          openOrchestration={() => navigate('/orchestrate')}
          shareCopiedSessionId={shareCopiedSessionId}
          sharingSessionId={sharingSessionId}
          t={t}
        />
      </aside>

      {mobileMenuOpen && (
        <div className="md:hidden fixed inset-0 z-50 flex">
          <div className="fixed inset-0 bg-black/50" onClick={() => setMobileMenuOpen(false)} />
          <div className="relative flex flex-col w-[280px] h-full bg-sidebar border-r border-sidebar-border animate-in slide-in-from-left">
            <Button variant="ghost" size="icon" className="absolute right-2 top-2 z-10" onClick={() => setMobileMenuOpen(false)}>
              <X className="h-4 w-4" />
            </Button>
            <SidebarContent
              groupedSessions={groupedSessions}
              activeSession={activeSessionId}
              setActiveSession={(id) => selectSession(id).catch((err) => appendSystem(err.message))}
              createSession={() => createSession().catch((err) => appendSystem(err.message))}
              shareSession={(session) => shareSession(session).catch((err) => appendSystem(err.message))}
              renameSession={renameSession}
              deleteSession={(session) => deleteSession(session).catch((err) => appendSystem(err.message))}
              search={search}
              setSearch={setSearch}
              openSettings={() => openSettings()}
              agentOnline={Boolean(onlineAgent)}
              openOrchestration={() => {
                setMobileMenuOpen(false);
                navigate('/orchestrate');
              }}
              shareCopiedSessionId={shareCopiedSessionId}
              sharingSessionId={sharingSessionId}
              t={t}
            />
          </div>
        </div>
      )}

      <main className="flex-1 flex flex-col min-w-0 h-full">
        <header className="h-14 shrink-0 border-b border-border flex items-center justify-between px-3 md:px-4 bg-background z-10">
          <div className="flex items-center gap-2 overflow-hidden">
            <Button variant="ghost" size="icon" className="md:hidden shrink-0 text-muted-foreground" onClick={() => setMobileMenuOpen(true)}>
              <Menu className="h-5 w-5" />
            </Button>
            <Button variant="ghost" size="icon" className="hidden md:flex shrink-0 text-muted-foreground" onClick={() => setSidebarOpen(!sidebarOpen)}>
              {sidebarOpen ? <PanelLeftClose className="h-5 w-5" /> : <PanelLeft className="h-5 w-5" />}
            </Button>

            <div className="h-4 w-px bg-border mx-1 hidden md:block" />

            <div className="flex items-center gap-2 min-w-0">
              <span className="text-sm font-medium truncate">
                {displaySessionTitle(activeSession, t)}
              </span>
            </div>
          </div>

          <div className="flex items-center gap-3 shrink-0">
            <AgentSelector
              agents={agents}
              selectedAgentId={selectedAgentId}
              onSelect={selectAgent}
              t={t}
              className="hidden sm:inline-flex"
            />

            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground rounded-full h-8 w-8"
              onClick={() => refreshAll().catch((err) => appendSystem(err.message))}
              disabled={refreshingAll}
              aria-label={t.refresh}
              title={t.refresh}
            >
              <RefreshCw className={cn("h-4 w-4", refreshingAll && "animate-spin")} />
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className={cn(
                "h-8 gap-1.5 rounded-lg",
                shareCopiedSessionId === activeSession?.id && "text-emerald-600 dark:text-emerald-400"
              )}
              onClick={() => shareSession(activeSession).catch((err) => appendSystem(err.message))}
              disabled={!activeSession || sharingSessionId === activeSession.id}
              aria-label={t.shareConversation}
              title={shareCopiedSessionId === activeSession?.id ? t.copied : t.shareConversation}
            >
              {sharingSessionId === activeSession?.id ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : shareCopiedSessionId === activeSession?.id ? <Check className="h-3.5 w-3.5" /> : <Share2 className="h-3.5 w-3.5" />}
              <span>{shareCopiedSessionId === activeSession?.id ? t.copied : t.shareConversation}</span>
            </Button>
            <Button variant="secondary" size="sm" className="hidden sm:inline-flex h-8 gap-1.5 rounded-lg" onClick={() => openSettings('cli')}>
              <Plus className="h-3.5 w-3.5" />
              {t.addCliEndpoint}
            </Button>
            <Button variant="secondary" size="sm" className="hidden sm:inline-flex h-8 gap-1.5 rounded-lg" onClick={() => navigate('/orchestrate')}>
              <GitBranch className="h-3.5 w-3.5" />
              {t.orchestrate}
            </Button>
          </div>
        </header>

        <div className="bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          <div className="flex items-center gap-1.5">
            <Server className="h-3.5 w-3.5" />
            <span>{t.runner}: {runner}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            <span>{t.thread}: {thread}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Command className="h-3.5 w-3.5" />
            <span>{t.status}: {connectionStatus}</span>
          </div>
          {runner === 'acp' && (
            <span className="inline-flex items-center gap-1 rounded-full border border-border bg-background/70 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide">
              {t.acpBadge}
            </span>
          )}
          <AgentSelector
            agents={agents}
            selectedAgentId={selectedAgentId}
            onSelect={selectAgent}
            t={t}
            className="sm:hidden min-w-[220px]"
          />
        </div>

        {(nativeResumeCommand || (runner === 'acp' && (nativeResumeId || activeSessionId))) && (
          <div className="border-b border-border bg-background px-3 py-2 md:px-4">
            <TakeoverHint
              command={nativeResumeCommand}
              nativeId={nativeResumeId}
              available={!!nativeResumeCommand}
              copied={takeoverCopied}
              onCopy={() => {
                if (!nativeResumeCommand) return;
                copyText(nativeResumeCommand)
                  .then((ok) => {
                    if (ok) {
                      setTakeoverCopied(true);
                      window.setTimeout(() => setTakeoverCopied(false), 1500);
                    }
                  })
                  .catch(() => undefined);
              }}
              t={t}
            />
          </div>
        )}

        <div className="relative flex-1 min-h-0">
          <div
            ref={messageScrollRef}
            onScroll={updateMessageScrollState}
            className="h-full overflow-y-auto p-4 md:p-6 space-y-4 scroll-smooth elegant-scrollbar"
          >
            {!items.length ? (
              <div className="h-full flex flex-col items-center justify-center text-center max-w-md mx-auto space-y-4 animate-in fade-in zoom-in-95 duration-500">
                <div className="h-12 w-12 rounded-2xl bg-primary/5 border border-border flex items-center justify-center mb-2">
                  <Terminal className="h-6 w-6 text-primary" />
                </div>
                <h2 className="text-lg font-medium">{t.howCanIHelp}</h2>
                <div className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-muted/40 px-2 py-1 text-xs text-muted-foreground">
                  <Server className="h-3.5 w-3.5 shrink-0" />
                  <span className="truncate">{selectedAgent?.name || t.noBridgeConnected}</span>
                  {!agentSessions.length && <span className="shrink-0">· {t.noSessions}</span>}
                </div>
                <p className="text-sm text-muted-foreground mb-4">
                  {t.codexCapability}
                </p>
                <div className="grid grid-cols-2 gap-2 w-full">
                  <Button variant="secondary" className="h-auto py-3 px-4 justify-start text-left flex-col items-start gap-1" onClick={() => setInputVal(t.readProjectFiles)}>
                    <span className="text-sm font-medium">{t.readProjectFiles}</span>
                    <span className="text-xs text-muted-foreground font-normal">{t.exploreCurrentDirectory}</span>
                  </Button>
                  <Button variant="secondary" className="h-auto py-3 px-4 justify-start text-left flex-col items-start gap-1" onClick={() => setInputVal(t.runTestSuite)}>
                    <span className="text-sm font-medium">{t.runTestSuite}</span>
                    <span className="text-xs text-muted-foreground font-normal">{t.executeConfiguredTests}</span>
                  </Button>
                </div>
              </div>
            ) : (
              items.map((item) => item.type === 'message'
                ? <MessageItem key={item.id} msg={item} t={t} />
                : item.type === 'tool'
                  ? <ToolItem key={item.id} tool={item.tool} t={t} />
                  : <ApprovalCard key={item.id} item={item} t={t} onDecision={respondApproval} />
              )
            )}
            <div ref={messageEndRef} className="h-4" />
          </div>

          {showScrollBottom && (
            <Button
              variant="secondary"
              size="icon"
              type="button"
              className="absolute bottom-4 left-1/2 z-20 h-9 w-9 -translate-x-1/2 rounded-full border border-border bg-card/95 text-muted-foreground shadow-lg backdrop-blur hover:text-foreground"
              onClick={() => scrollMessagesToBottom()}
              aria-label={t.jumpToLatestMessage}
              title={t.jumpToBottom}
            >
              <ArrowDownToLine className="h-4 w-4" />
            </Button>
          )}
        </div>

        <div className="shrink-0 p-4 border-t border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
          <form
            onSubmit={(event) => {
              event.preventDefault();
              sendPrompt().catch((err) => appendSystem(err.message));
            }}
            className="max-w-4xl mx-auto flex flex-col bg-card border border-border rounded-xl shadow-sm focus-within:ring-1 focus-within:ring-ring focus-within:border-border transition-all"
          >
            <textarea
              className="w-full bg-transparent border-0 resize-none p-3 text-sm focus:outline-none focus:ring-0 min-h-[60px] max-h-[300px] elegant-scrollbar"
              placeholder={t.askCodex}
              value={inputVal}
              onChange={(e) => setInputVal(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  sendPrompt().catch((err) => appendSystem(err.message));
                }
              }}
              disabled={isGenerating}
            />
            {attachments.length > 0 && (
              <div className="flex gap-2 px-3 pb-2 overflow-x-auto elegant-scrollbar">
                {attachments.map((attachment) => (
                  <div key={attachment.id} className="relative h-14 w-14 shrink-0 overflow-hidden rounded-md border border-border bg-muted">
                    <img src={attachment.previewUrl} alt={attachment.name} className="h-full w-full object-cover" />
                    <button
                      type="button"
                      className="absolute right-0.5 top-0.5 flex h-5 w-5 items-center justify-center rounded-full bg-background/90 text-foreground shadow-sm hover:bg-background"
                      onClick={() => removeAttachment(attachment.id)}
                      aria-label={`${t.removeFile} ${attachment.name}`}
                    >
                      <X className="h-3 w-3" />
                    </button>
                  </div>
                ))}
              </div>
            )}

            <div className="flex items-center justify-between p-2 pt-0">
              <div className="flex items-center gap-1">
                <input
                  ref={fileInputRef}
                  type="file"
                  accept="image/*"
                  multiple
                  className="hidden"
                  onChange={(event) => addImages(event.target.files).catch((err) => appendSystem(err.message))}
                />
                <Button
                  variant="ghost"
                  size="icon"
                  type="button"
                  className="h-8 w-8 text-muted-foreground rounded-lg"
                  onClick={() => fileInputRef.current?.click()}
                  disabled={isGenerating}
                  aria-label={t.uploadImages}
                >
                  <ImagePlus className="h-4 w-4" />
                </Button>
              </div>

              <div className="flex items-center gap-2">
                {isGenerating ? (
                  <Button variant="secondary" size="sm" type="button" className="h-8 px-3 rounded-lg gap-1.5 text-xs" onClick={stopRun}>
                    <Square className="h-3.5 w-3.5 fill-current" />
                    {activeRun?.status === 'canceling' ? t.stopping : t.stop}
                  </Button>
                ) : (
                  <Button size="sm" type="submit" className="h-8 px-3 rounded-lg gap-1.5 text-xs font-medium" disabled={!inputVal.trim() && !attachments.length}>
                    {t.send}
                    <Send className="h-3.5 w-3.5" />
                  </Button>
                )}
              </div>
            </div>
          </form>
          <div className="text-center mt-2">
            <span className="text-[10px] text-muted-foreground/60 font-medium">{t.verifyNotice}</span>
          </div>
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

      {renameTarget && (
        <RenameSessionModal
          title={renameDraft}
          error={renameError}
          saving={renaming}
          onChange={(value) => {
            setRenameDraft(value);
            if (renameError) setRenameError('');
          }}
          onClose={closeRenameModal}
          onSave={saveRenameSession}
          t={t}
        />
      )}
    </div>
  );
}

function upsertAssistant(items: ChatItem[], id: string, content: string): ChatItem[] {
  const found = items.some((item) => item.id === id);
  if (!found) {
    return [...items, { id, type: 'message', role: 'assistant', content, createdAt: Math.floor(Date.now() / 1000) }];
  }
  return items.map((item) => item.id === id && item.type === 'message' ? { ...item, content } : item);
}
