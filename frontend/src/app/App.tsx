import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Terminal, User, Lock, Globe, ChevronDown,
  PanelLeftClose, PanelLeft, Plus, MessageSquare,
  Settings, LogOut, Search,
  ImagePlus, Send, Square, AlertCircle,
  RefreshCw, FileCode, CheckCircle,
  Menu, X, Server, Activity, Command,
  Trash2, Edit2
} from 'lucide-react';
import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

type UserAccount = {
  id: string;
  username: string;
  createdAt: number;
};

type Agent = {
  id: string;
  name: string;
  machineId: string;
  hostname: string;
  instance?: string;
  lastSeenAt: number;
  online: boolean;
};

type Session = {
  id: string;
  agentId: string;
  userId: string;
  title: string;
  remoteThreadId?: string;
  createdAt: number;
  updatedAt: number;
};

type Message = {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  createdAt: number;
};

type Run = {
  id: string;
  promptId: string;
  status: string;
};

type ToolEvent = {
  id?: string;
  name?: string;
  command?: string;
  input?: string;
  output?: string;
  status?: string;
  exitCode?: number;
};

type ChatItem =
  | { id: string; type: 'message'; role: 'user' | 'assistant' | 'system'; content: string; createdAt?: number }
  | { id: string; type: 'tool'; tool: ToolEvent };

type Envelope = {
  type: string;
  sid?: string;
  payload?: any;
};

type ImageAttachment = {
  id: string;
  name: string;
  mimeType: string;
  size: number;
  data: string;
  previewUrl: string;
};

async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
    ...options,
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.message || body.code || `HTTP ${res.status}`);
  }
  return body;
}

function newID(prefix: string) {
  if (!window.crypto?.getRandomValues) {
    return `${prefix}_${Date.now().toString(16)}${Math.random().toString(16).slice(2)}`;
  }
  const random = window.crypto.getRandomValues(new Uint32Array(4));
  return `${prefix}_${Array.from(random, (part) => part.toString(16).padStart(8, '0')).join('')}`;
}

function displaySessionTitle(session?: Session | null) {
  if (!session?.title || session.title === 'New chat') return 'New Session';
  return session.title;
}

function titleFromPrompt(prompt: string) {
  const compact = prompt.replace(/\s+/g, ' ').trim();
  if (!compact) return 'New Session';
  return compact.length > 48 ? `${compact.slice(0, 48)}...` : compact;
}

function formatTime(timestamp?: number) {
  const date = timestamp ? new Date(timestamp * 1000) : new Date();
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function sessionDateLabel(timestamp: number) {
  const date = new Date(timestamp * 1000);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const target = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const diffDays = Math.round((today.getTime() - target.getTime()) / 86400000);
  if (diffDays <= 0) return 'Today';
  if (diffDays === 1) return 'Yesterday';
  if (diffDays <= 7) return 'Previous 7 Days';
  return 'Older';
}

function initials(username: string) {
  return (username || 'CB')
    .split(/\s+/)
    .slice(0, 2)
    .map((part) => part[0])
    .join('')
    .toUpperCase();
}

function activeStatus(status?: string) {
  return status === 'queued' || status === 'running' || status === 'canceling';
}

function waitForOpen(ws: WebSocket, timeout = 3000) {
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

function escapeBasic(value: string) {
  return value.replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  })[ch] || ch);
}

function renderInlineMarkdown(text: string) {
  return escapeBasic(text)
    .replace(/!\[([^\]]*)\]\((blob:[^)]+|data:image\/[^)]+|https?:\/\/[^)]+)\)/g, '<img alt="$1" src="$2" class="mt-2 max-h-64 rounded-lg border border-border object-contain" />')
    .replace(/`([^`]+)`/g, '<code class="px-1 py-0.5 rounded bg-muted font-mono text-[0.92em]">$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
}

function readImageAttachment(file: File): Promise<ImageAttachment> {
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

function MessageContent({ content }: { content: string }) {
  const html = useMemo(() => {
    const chunks = String(content || '').split(/```([\s\S]*?)```/g);
    return chunks.map((chunk, index) => {
      if (index % 2 === 1) {
        return `<pre class="my-3 overflow-x-auto rounded-lg border border-border bg-[#0f172a] p-3 text-xs leading-relaxed text-slate-200"><code>${escapeBasic(chunk.replace(/^\w+\n/, ''))}</code></pre>`;
      }
      return renderInlineMarkdown(chunk).replace(/\n/g, '<br />');
    }).join('');
  }, [content]);

  return <div className="text-[14px] leading-relaxed text-foreground" dangerouslySetInnerHTML={{ __html: html }} />;
}

const Button = React.forwardRef<HTMLButtonElement, React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'ghost' | 'destructive', size?: 'sm' | 'md' | 'icon' }>(
  ({ className, variant = 'primary', size = 'md', ...props }, ref) => {
    return (
      <button
        ref={ref}
        className={cn(
          "inline-flex items-center justify-center rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
          {
            'bg-primary text-primary-foreground hover:bg-primary/90 shadow-sm': variant === 'primary',
            'bg-secondary text-secondary-foreground hover:bg-secondary/80': variant === 'secondary',
            'hover:bg-accent hover:text-accent-foreground': variant === 'ghost',
            'bg-destructive text-destructive-foreground hover:bg-destructive/90 shadow-sm': variant === 'destructive',
            'h-9 px-4 py-2': size === 'md',
            'h-8 rounded-md px-3 text-xs': size === 'sm',
            'h-9 w-9': size === 'icon',
          },
          className
        )}
        {...props}
      />
    );
  }
);
Button.displayName = 'Button';

const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        className={cn(
          "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
          className
        )}
        ref={ref}
        {...props}
      />
    );
  }
);
Input.displayName = 'Input';

export default function App() {
  const [user, setUser] = useState<UserAccount | null>(null);
  const [booting, setBooting] = useState(true);
  const [isDarkMode, setIsDarkMode] = useState(() => localStorage.getItem('codexBridge.theme') !== 'light');

  useEffect(() => {
    document.documentElement.classList.toggle('dark', isDarkMode);
    localStorage.setItem('codexBridge.theme', isDarkMode ? 'dark' : 'light');
  }, [isDarkMode]);

  useEffect(() => {
    api<{ user: UserAccount }>('/api/me')
      .then((data) => setUser(data.user))
      .catch(() => setUser(null))
      .finally(() => setBooting(false));
  }, []);

  if (booting) {
    return (
      <div className="min-h-screen w-full flex items-center justify-center bg-background text-foreground">
        <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!user) {
    return <LoginScreen onLogin={setUser} />;
  }

  return (
    <Workspace
      user={user}
      onLogout={() => setUser(null)}
      isDarkMode={isDarkMode}
      setIsDarkMode={setIsDarkMode}
    />
  );
}

function LoginScreen({ onLogin }: { onLogin: (user: UserAccount) => void }) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleLogin = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    const form = new FormData(e.currentTarget);
    try {
      const data = await api<{ user: UserAccount }>('/api/login', {
        method: 'POST',
        body: JSON.stringify({
          username: String(form.get('username') || ''),
          password: String(form.get('password') || ''),
        }),
      });
      onLogin(data.user);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Connection failed.');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen w-full flex items-center justify-center bg-background text-foreground p-4">
      <div className="w-full max-w-[360px] flex flex-col gap-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <div className="h-12 w-12 rounded-xl bg-primary text-primary-foreground flex items-center justify-center mb-2 shadow-sm">
            <Terminal className="h-6 w-6" />
          </div>
          <h1 className="text-xl font-medium tracking-tight">Codex Bridge</h1>
          <p className="text-sm text-muted-foreground">Secure connection to your workspace</p>
        </div>

        <form onSubmit={handleLogin} className="flex flex-col gap-4">
          <div className="space-y-4">
            <div className="space-y-2">
              <label className="text-sm font-medium leading-none" htmlFor="username">
                Username
              </label>
              <div className="relative">
                <User className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input id="username" name="username" placeholder="admin" className="pl-9" autoComplete="username" required />
              </div>
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium leading-none" htmlFor="password">
                Password
              </label>
              <div className="relative">
                <Lock className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input id="password" name="password" type="password" placeholder="••••••••" className="pl-9" autoComplete="current-password" required />
              </div>
            </div>
          </div>

          {error && (
            <div className="p-3 text-sm bg-destructive/10 text-destructive rounded-md border border-destructive/20 flex items-start gap-2">
              <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
              <p>{error}</p>
            </div>
          )}

          <Button type="submit" className="w-full" disabled={loading}>
            {loading ? <RefreshCw className="h-4 w-4 animate-spin" /> : 'Connect to Workspace'}
          </Button>
        </form>

        <div className="flex justify-center mt-4">
          <Button variant="ghost" size="sm" className="text-muted-foreground gap-2">
            <Globe className="h-4 w-4" />
            English
            <ChevronDown className="h-3 w-3 opacity-50" />
          </Button>
        </div>
      </div>
    </div>
  );
}

function Workspace({ user, onLogout, isDarkMode, setIsDarkMode }: { user: UserAccount, onLogout: () => void, isDarkMode: boolean, setIsDarkMode: (v: boolean) => void }) {
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [activeSessionId, setActiveSessionId] = useState('');
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [inputVal, setInputVal] = useState('');
  const [attachments, setAttachments] = useState<ImageAttachment[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [items, setItems] = useState<ChatItem[]>([]);
  const [runner, setRunner] = useState('-');
  const [thread, setThread] = useState('-');
  const [connectionStatus, setConnectionStatus] = useState('Disconnected');
  const [activeRun, setActiveRun] = useState<Run | null>(null);
  const [search, setSearch] = useState('');
  const wsRef = useRef<WebSocket | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const activeSessionIdRef = useRef('');
  const assistantItemIdRef = useRef<string | null>(null);
  const assistantTextRef = useRef('');

  const activeSession = sessions.find((session) => session.id === activeSessionId) || null;
  const onlineAgent = agents.find((agent) => agent.online);
  const isGenerating = Boolean(activeRun && activeStatus(activeRun.status));

  const loadAgents = useCallback(async () => {
    const data = await api<{ agents: Agent[] }>('/api/agents');
    setAgents(data.agents || []);
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

  const loadRuns = useCallback(async (sessionId: string) => {
    const data = await api<{ runs: Run[] }>(`/api/sessions/${encodeURIComponent(sessionId)}/runs`);
    setActiveRun((data.runs || []).find((run) => activeStatus(run.status)) || null);
  }, []);

  const touchSession = useCallback((sessionId: string) => {
    setSessions((current) => {
      const session = current.find((item) => item.id === sessionId);
      if (!session) return current;
      const updated = { ...session, updatedAt: Math.floor(Date.now() / 1000) };
      return [updated, ...current.filter((item) => item.id !== sessionId)];
    });
  }, []);

  const handleEnvelope = useCallback((env: Envelope) => {
    if (env.sid && activeSessionIdRef.current && env.sid !== activeSessionIdRef.current) return;
    const payload = env.payload || {};

    switch (env.type) {
      case 'status':
        setConnectionStatus(payload.status ? String(payload.status) : 'Connected');
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
        setConnectionStatus('Ready');
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
      case 'prompt_complete':
        if (payload.content) {
          if (!assistantItemIdRef.current) assistantItemIdRef.current = newID('msg');
          assistantTextRef.current = String(payload.content);
          const id = assistantItemIdRef.current;
          setItems((current) => upsertAssistant(current, id, assistantTextRef.current));
        }
        setThread(payload.remoteThreadId || thread || '-');
        setActiveRun(null);
        assistantItemIdRef.current = null;
        assistantTextRef.current = '';
        setConnectionStatus('Ready');
        if (activeSessionIdRef.current) touchSession(activeSessionIdRef.current);
        break;
      case 'error':
        if (payload.code === 'SESSION_DELETED') {
          closeWS();
          setActiveSessionId('');
          setItems([]);
          setActiveRun(null);
          return;
        }
        appendSystem(payload.message || payload.code || 'Unknown error');
        setActiveRun(null);
        setConnectionStatus(payload.code || 'Error');
        break;
      default:
        break;
    }
  }, [appendSystem, closeWS, thread, touchSession]);

  const connectWS = useCallback((sessionId: string) => {
    closeWS();
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${scheme}://${location.host}/ws/chat?sid=${encodeURIComponent(sessionId)}`);
    wsRef.current = ws;
    setConnectionStatus('Connecting');
    ws.onopen = () => {
      setConnectionStatus('Connected');
      ws.send(JSON.stringify({ type: 'heartbeat', sid: sessionId, payload: { ts: Date.now() } }));
    };
    ws.onmessage = (event) => {
      try {
        handleEnvelope(JSON.parse(event.data));
      } catch {
        // Ignore malformed frames.
      }
    };
    ws.onerror = () => setConnectionStatus('Connection error');
    ws.onclose = () => {
      if (activeSessionIdRef.current === sessionId) setConnectionStatus('Disconnected');
    };
    return ws;
  }, [closeWS, handleEnvelope]);

  const selectSession = useCallback(async (sessionId: string) => {
    setActiveSessionId(sessionId);
    activeSessionIdRef.current = sessionId;
    setRunner('-');
    setThread(sessions.find((session) => session.id === sessionId)?.remoteThreadId || '-');
    setActiveRun(null);
    setMobileMenuOpen(false);
    await loadMessages(sessionId);
    await loadRuns(sessionId);
    connectWS(sessionId);
  }, [connectWS, loadMessages, loadRuns, sessions]);

  const refreshAll = useCallback(async () => {
    const [_, loadedSessions] = await Promise.all([loadAgents(), loadSessions()]);
    if (!activeSessionIdRef.current && loadedSessions.length) {
      await selectSession(loadedSessions[0].id);
    }
  }, [loadAgents, loadSessions, selectSession]);

  useEffect(() => {
    refreshAll().catch((err) => appendSystem(err.message));
    return () => closeWS();
  }, []);

  useEffect(() => {
    activeSessionIdRef.current = activeSessionId;
  }, [activeSessionId]);

  const createSession = async (title = 'New Session') => {
    const agent = agents.find((item) => item.online) || agents[0];
    if (!agent) {
      appendSystem('No bridge connected');
      return;
    }
    const data = await api<{ session: Session }>('/api/sessions', {
      method: 'POST',
      body: JSON.stringify({ agentId: agent.id, title }),
    });
    setSessions((current) => [data.session, ...current.filter((session) => session.id !== data.session.id)]);
    await selectSession(data.session.id);
  };

  const renameSession = async (session: Session) => {
    const title = window.prompt('Rename session', displaySessionTitle(session));
    if (!title?.trim()) return;
    const data = await api<{ session: Session }>(`/api/sessions/${encodeURIComponent(session.id)}`, {
      method: 'PATCH',
      body: JSON.stringify({ title: title.trim() }),
    });
    setSessions((current) => current.map((item) => item.id === data.session.id ? data.session : item));
  };

  const deleteSession = async (session: Session) => {
    if (!window.confirm('Delete this session? This cannot be undone.')) return;
    await api(`/api/sessions/${encodeURIComponent(session.id)}`, { method: 'DELETE' });
    const remaining = sessions.filter((item) => item.id !== session.id);
    setSessions(remaining);
    if (activeSessionId === session.id) {
      closeWS();
      setItems([]);
      setActiveRun(null);
      setActiveSessionId('');
      if (remaining[0]) await selectSession(remaining[0].id);
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
    const promptText = text || 'Please analyze the uploaded image.';
    const wasUntitled = !activeSession || activeSession.title === 'New chat' || activeSession.title === 'New Session';
    if (!sessionId) {
      await createSession(titleFromPrompt(promptText));
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
    setItems((current) => [...current, { id: promptId, type: 'message', role: 'user', content: userContent, createdAt: Math.floor(Date.now() / 1000) }]);
    assistantItemIdRef.current = null;
    assistantTextRef.current = '';
    setActiveRun({ id: '', promptId, status: 'running' });
    if (wasUntitled && promptText) {
      api<{ session: Session }>(`/api/sessions/${encodeURIComponent(sessionId)}`, {
        method: 'PATCH',
        body: JSON.stringify({ title: titleFromPrompt(promptText) }),
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

  const groupedSessions = useMemo(() => {
    const query = search.trim().toLowerCase();
    return sessions
      .filter((session) => !query || displaySessionTitle(session).toLowerCase().includes(query))
      .reduce((acc, session) => {
        const label = sessionDateLabel(session.updatedAt || session.createdAt);
        if (!acc[label]) acc[label] = [];
        acc[label].push(session);
        return acc;
      }, {} as Record<string, Session[]>);
  }, [sessions, search]);

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
          renameSession={(session) => renameSession(session).catch((err) => appendSystem(err.message))}
          deleteSession={(session) => deleteSession(session).catch((err) => appendSystem(err.message))}
          search={search}
          setSearch={setSearch}
          openSettings={() => setSettingsOpen(true)}
          agentOnline={Boolean(onlineAgent)}
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
              renameSession={(session) => renameSession(session).catch((err) => appendSystem(err.message))}
              deleteSession={(session) => deleteSession(session).catch((err) => appendSystem(err.message))}
              search={search}
              setSearch={setSearch}
              openSettings={() => setSettingsOpen(true)}
              agentOnline={Boolean(onlineAgent)}
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
                {displaySessionTitle(activeSession)}
              </span>
            </div>
          </div>

          <div className="flex items-center gap-3 shrink-0">
            <div className="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-secondary/50 border border-border/50 text-xs text-muted-foreground">
              <div className={cn("h-2 w-2 rounded-full", onlineAgent ? "bg-emerald-500" : "bg-muted-foreground")} />
              {onlineAgent ? 'Agent Online' : 'Agent Offline'}
            </div>

            <Button variant="ghost" size="icon" className="text-muted-foreground rounded-full h-8 w-8" onClick={() => refreshAll().catch((err) => appendSystem(err.message))}>
              <RefreshCw className="h-4 w-4" />
            </Button>
          </div>
        </header>

        <div className="bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          <div className="flex items-center gap-1.5">
            <Server className="h-3.5 w-3.5" />
            <span>Runner: {runner}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            <span>Thread: {thread}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Command className="h-3.5 w-3.5" />
            <span>Status: {connectionStatus}</span>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-4 md:p-6 space-y-4 scroll-smooth elegant-scrollbar">
          {!items.length ? (
            <div className="h-full flex flex-col items-center justify-center text-center max-w-md mx-auto space-y-4 animate-in fade-in zoom-in-95 duration-500">
              <div className="h-12 w-12 rounded-2xl bg-primary/5 border border-border flex items-center justify-center mb-2">
                <Terminal className="h-6 w-6 text-primary" />
              </div>
              <h2 className="text-lg font-medium">How can I help you today?</h2>
              <p className="text-sm text-muted-foreground mb-4">
                I can execute code, read files, run terminal commands, and help you build your project.
              </p>
              <div className="grid grid-cols-2 gap-2 w-full">
                <Button variant="secondary" className="h-auto py-3 px-4 justify-start text-left flex-col items-start gap-1" onClick={() => setInputVal('Read project files')}>
                  <span className="text-sm font-medium">Read project files</span>
                  <span className="text-xs text-muted-foreground font-normal">Explore current directory</span>
                </Button>
                <Button variant="secondary" className="h-auto py-3 px-4 justify-start text-left flex-col items-start gap-1" onClick={() => setInputVal('Run test suite')}>
                  <span className="text-sm font-medium">Run test suite</span>
                  <span className="text-xs text-muted-foreground font-normal">Execute configured tests</span>
                </Button>
              </div>
            </div>
          ) : (
            items.map((item) => item.type === 'message'
              ? <MessageItem key={item.id} msg={item} />
              : <ToolItem key={item.id} tool={item.tool} />
            )
          )}
          <div className="h-4" />
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
              placeholder="Ask Codex to read files, run commands, or write code..."
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
                      aria-label={`Remove ${attachment.name}`}
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
                  aria-label="Upload images"
                >
                  <ImagePlus className="h-4 w-4" />
                </Button>
              </div>

              <div className="flex items-center gap-2">
                {isGenerating ? (
                  <Button variant="secondary" size="sm" type="button" className="h-8 px-3 rounded-lg gap-1.5 text-xs" onClick={stopRun}>
                    <Square className="h-3.5 w-3.5 fill-current" />
                    {activeRun?.status === 'canceling' ? 'Stopping' : 'Stop'}
                  </Button>
                ) : (
                  <Button size="sm" type="submit" className="h-8 px-3 rounded-lg gap-1.5 text-xs font-medium" disabled={!inputVal.trim() && !attachments.length}>
                    Send
                    <Send className="h-3.5 w-3.5" />
                  </Button>
                )}
              </div>
            </div>
          </form>
          <div className="text-center mt-2">
            <span className="text-[10px] text-muted-foreground/60 font-medium">Codex Bridge may produce inaccurate results. Verify important changes.</span>
          </div>
        </div>
      </main>

      {settingsOpen && (
        <SettingsModal
          user={user}
          agents={agents}
          onLogout={logout}
          isDarkMode={isDarkMode}
          setIsDarkMode={setIsDarkMode}
          close={() => setSettingsOpen(false)}
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

function SidebarContent({
  groupedSessions,
  activeSession,
  setActiveSession,
  createSession,
  renameSession,
  deleteSession,
  search,
  setSearch,
  openSettings,
  agentOnline,
}: {
  groupedSessions: Record<string, Session[]>;
  activeSession: string;
  setActiveSession: (id: string) => void;
  createSession: () => void;
  renameSession: (session: Session) => void;
  deleteSession: (session: Session) => void;
  search: string;
  setSearch: (value: string) => void;
  openSettings: () => void;
  agentOnline: boolean;
}) {
  return (
    <>
      <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
        <div className="flex items-center gap-2 font-medium">
          <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
            <Terminal className="h-3.5 w-3.5" />
          </div>
          <span className="text-sm">Codex Bridge</span>
        </div>
      </div>

      <div className="p-3">
        <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm" onClick={createSession}>
          <Plus className="h-4 w-4" />
          New Session
        </Button>
      </div>

      <div className="px-3 pb-2">
        <div className="relative">
          <Search className="absolute left-2.5 top-2 h-3.5 w-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search sessions..."
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="w-full h-8 pl-8 pr-3 text-xs bg-sidebar-accent/50 border border-sidebar-border rounded-md focus:outline-none focus:ring-1 focus:ring-ring transition-all"
          />
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-2 space-y-4 elegant-scrollbar">
        {Object.keys(groupedSessions).length === 0 ? (
          <div className="px-2 py-1.5 text-xs text-muted-foreground">No sessions</div>
        ) : Object.entries(groupedSessions).map(([date, sessions]) => (
          <div key={date}>
            <h4 className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider mb-1.5 px-2">
              {date}
            </h4>
            <div className="space-y-0.5">
              {sessions.map((session) => (
                <button
                  key={session.id}
                  onClick={() => setActiveSession(session.id)}
                  className={cn(
                    "w-full text-left px-2 py-1.5 rounded-md text-sm flex items-center gap-2 transition-colors group",
                    activeSession === session.id
                      ? "bg-sidebar-accent text-sidebar-accent-foreground font-medium"
                      : "text-sidebar-foreground hover:bg-sidebar-accent/50"
                  )}
                >
                  <MessageSquare className="h-3.5 w-3.5 opacity-70 shrink-0" />
                  <span className="truncate">{displaySessionTitle(session)}</span>

                  {activeSession === session.id && (
                    <div className="ml-auto flex items-center gap-1 opacity-100 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
                      <span
                        className="h-5 w-5 rounded flex items-center justify-center hover:bg-sidebar-border text-muted-foreground"
                        onClick={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          renameSession(session);
                        }}
                      >
                        <Edit2 className="h-3 w-3" />
                      </span>
                      <span
                        className="h-5 w-5 rounded flex items-center justify-center hover:bg-destructive/10 hover:text-destructive text-muted-foreground"
                        onClick={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                          deleteSession(session);
                        }}
                      >
                        <Trash2 className="h-3 w-3" />
                      </span>
                    </div>
                  )}
                </button>
              ))}
            </div>
          </div>
        ))}
      </div>

      <div className="p-3 border-t border-sidebar-border shrink-0 mt-auto bg-sidebar">
        <button
          onClick={openSettings}
          className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-sm hover:bg-sidebar-accent transition-colors text-sidebar-foreground"
        >
          <div className="h-6 w-6 rounded-full bg-sidebar-primary/10 flex items-center justify-center">
            <Settings className="h-3.5 w-3.5" />
          </div>
          <span className="flex-1 text-left">Settings</span>
          <div className={cn("h-1.5 w-1.5 rounded-full", agentOnline ? "bg-emerald-500" : "bg-muted-foreground")} title={agentOnline ? 'Connected' : 'Disconnected'} />
        </button>
      </div>
    </>
  );
}

function MessageItem({ msg }: { msg: Extract<ChatItem, { type: 'message' }> }) {
  const isUser = msg.role === 'user';

  return (
    <div className="flex gap-4 w-full max-w-4xl mx-auto py-2 group">
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
        <div className="flex items-center gap-2 mb-0.5">
          <span className="text-xs font-semibold">{isUser ? 'You' : msg.role === 'system' ? 'System' : 'Codex'}</span>
          <span className="text-[10px] text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity">{formatTime(msg.createdAt)}</span>
        </div>

        <MessageContent content={msg.content} />
      </div>
    </div>
  );
}

function ToolItem({ tool }: { tool: ToolEvent }) {
  return (
    <div className="w-full max-w-4xl mx-auto mt-2 bg-muted/30 border border-border rounded-lg overflow-hidden text-[13px] group/tool">
      <div className="flex items-center gap-2 px-3 py-1.5 bg-muted/50 border-b border-border">
        <Terminal className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="font-medium text-xs">Run: {tool.name || 'Bash'}</span>
        <span className="ml-auto text-xs text-muted-foreground font-mono truncate max-w-[260px]">{tool.command || tool.input || tool.status || 'running'}</span>
        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground opacity-50" />
      </div>
      <div className="p-3 font-mono text-[11px] whitespace-pre-wrap text-muted-foreground overflow-x-auto max-h-40 bg-background/50 elegant-scrollbar">
        {[tool.command, tool.output, typeof tool.exitCode === 'number' ? `exit: ${tool.exitCode}` : ''].filter(Boolean).join('\n\n')}
      </div>
    </div>
  );
}

function SettingsModal({
  user,
  agents,
  onLogout,
  isDarkMode,
  setIsDarkMode,
  close,
}: {
  user: UserAccount;
  agents: Agent[];
  onLogout: () => void;
  isDarkMode: boolean;
  setIsDarkMode: (value: boolean) => void;
  close: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm animate-in fade-in">
      <div className="bg-card w-full max-w-md rounded-xl border border-border shadow-lg flex flex-col overflow-hidden animate-in zoom-in-95">
        <div className="px-4 py-3 border-b border-border flex items-center justify-between bg-muted/30">
          <h2 className="font-medium">Settings</h2>
          <Button variant="ghost" size="icon" className="h-7 w-7 rounded-md" onClick={close}>
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="p-4 space-y-6 overflow-y-auto max-h-[70vh] elegant-scrollbar">
          <div className="space-y-3">
            <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Account</h3>
            <div className="flex items-center justify-between p-3 rounded-lg border border-border bg-muted/20">
              <div className="flex items-center gap-3">
                <div className="h-9 w-9 rounded-full bg-primary/10 flex items-center justify-center text-primary font-medium">
                  {initials(user.username)}
                </div>
                <div>
                  <div className="text-sm font-medium">{user.username}</div>
                  <div className="text-xs text-muted-foreground">Local Administrator</div>
                </div>
              </div>
              <Button variant="ghost" size="sm" className="h-8 text-destructive hover:bg-destructive/10 hover:text-destructive" onClick={onLogout}>
                <LogOut className="h-4 w-4 mr-1.5" />
                Logout
              </Button>
            </div>
          </div>

          <div className="space-y-3">
            <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Appearance</h3>
            <div className="space-y-2">
              <div className="flex items-center justify-between py-2">
                <span className="text-sm">Theme</span>
                <div className="flex items-center gap-1 bg-muted p-1 rounded-lg border border-border/50">
                  <button
                    className={cn("px-2.5 py-1 text-xs rounded-md font-medium transition-colors", !isDarkMode ? "bg-background shadow-sm border border-border/50" : "text-muted-foreground hover:text-foreground")}
                    onClick={() => setIsDarkMode(false)}
                  >
                    Light
                  </button>
                  <button
                    className={cn("px-2.5 py-1 text-xs rounded-md font-medium transition-colors", isDarkMode ? "bg-background shadow-sm border border-border/50" : "text-muted-foreground hover:text-foreground")}
                    onClick={() => setIsDarkMode(true)}
                  >
                    Dark
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div className="space-y-3">
            <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Agents & Runtime</h3>
            <div className="space-y-2">
              {agents.length ? agents.map((agent) => (
                <div key={agent.id} className="flex items-center justify-between p-2.5 rounded-lg border border-border bg-muted/20">
                  <div className="flex flex-col min-w-0">
                    <span className="text-sm font-medium truncate">{agent.name}</span>
                    <span className="text-xs text-muted-foreground font-mono mt-0.5 truncate">{agent.hostname || agent.machineId}</span>
                  </div>
                  <div className={cn(
                    "px-2 py-0.5 rounded-full text-[10px] font-medium border uppercase tracking-wide",
                    agent.online
                      ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400"
                      : "bg-muted text-muted-foreground border-border"
                  )}>
                    {agent.online ? 'online' : 'offline'}
                  </div>
                </div>
              )) : (
                <div className="text-sm text-muted-foreground p-2.5 rounded-lg border border-border bg-muted/20">No agents enrolled</div>
              )}
            </div>
          </div>
        </div>

        <div className="p-4 border-t border-border flex justify-end gap-2 bg-muted/30">
          <Button variant="ghost" size="sm" onClick={close}>Cancel</Button>
          <Button size="sm" onClick={close}>Save Preferences</Button>
        </div>
      </div>
    </div>
  );
}
