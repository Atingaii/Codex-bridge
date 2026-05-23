const state = {
  user: null,
  agents: [],
  sessions: [],
  activeSession: null,
  ws: null,
  assistantNode: null,
  assistantText: "",
  runner: "-",
  thread: "-",
  statusKey: "disconnected",
  statusText: "",
  lang: normalizeLanguage(localStorage.getItem("codexBridge.lang")),
  theme: normalizeTheme(localStorage.getItem("codexBridge.theme")),
  sidebarCollapsed: localStorage.getItem("codexBridge.sidebarCollapsed") === "1",
  autoExecute: localStorage.getItem("codexBridge.autoExecute") !== "0",
  sessionQuery: "",
  wsSeq: 0,
  reconnectTimer: null,
  reconnectDelay: 1000,
  keepaliveTimer: null,
  activeRun: null,
  toolNodes: new Map(),
  pendingPrompt: null,
};

const el = (id) => document.getElementById(id);
const HEARTBEAT_MS = 12000;
const RECONNECT_MAX_MS = 15000;

function normalizeLanguage(value) {
  return value === "zh" ? "zh" : "en";
}

function normalizeTheme(value) {
  return value === "light" ? "light" : "dark";
}

const i18n = {
  en: {
    langSwitch: "中文",
    workspace: "Workspace",
    loginSubtitle: "Secure connection to your workspace",
    username: "Username",
    password: "Password",
    signIn: "Connect to Workspace",
    newChat: "New Session",
    conversations: "Conversations",
    searchSessions: "Search sessions...",
    today: "Today",
    yesterday: "Yesterday",
    previous7Days: "Previous 7 Days",
    older: "Older",
    disconnected: "Disconnected",
    connecting: "Connecting",
    connected: "Connected",
    connectionError: "Connection error",
    opening: "Opening",
    ready: "Ready",
    running: "Running",
    canceling: "Canceling",
    runActive: "A prompt is already running in this chat.",
    error: "Error",
    noAgent: "No agent",
    noBridge: "No bridge connected",
    noChats: "No chats",
    noMatches: "No matching sessions",
    unknownError: "Unknown error",
    wsDisconnected: "WebSocket is not connected",
    stop: "Stop",
    logout: "Logout",
    send: "Send",
    sendHint: "Enter to send, Shift+Enter for a new line",
    promptPlaceholder: "Ask Codex to inspect, change, or explain your workspace",
    metricAgent: "Agent",
    metricAgentHint: "Bridge status",
    metricSessions: "Sessions",
    metricSessionHint: "Conversation tabs",
    metricRunnerHint: "Current runtime",
    online: "Online",
    offline: "Offline",
    agentOnline: "Agent Online",
    agentOffline: "Agent Offline",
    agent: "Agent",
    reverseTunnel: "Reverse tunnel",
    runtime: "Runtime",
    session: "Session",
    runner: "Runner",
    thread: "Thread",
    roleUser: "You",
    roleAssistant: "Codex",
    roleSystem: "System",
    openNavigation: "Open navigation",
    closeNavigation: "Close navigation",
    collapseSidebar: "Collapse sidebar",
    expandSidebar: "Expand sidebar",
    refresh: "Refresh",
    copy: "Copy",
    copied: "Copied",
    copyFailed: "Copy failed",
    copyMessage: "Copy message",
    copyCode: "Copy code",
    tool: "Tool",
    toolRunning: "Running",
    toolCompleted: "Completed",
    toolFailed: "Failed",
    settings: "Settings",
    close: "Close",
    account: "Account",
    appearance: "Appearance",
    theme: "Theme",
    light: "Light",
    dark: "Dark",
    agentsRuntime: "Agents & Runtime",
    localAdmin: "Local Administrator",
    cancel: "Cancel",
    savePreferences: "Save Preferences",
    autoExecute: "Auto-execute",
    attach: "Attach",
    rename: "Rename",
    delete: "Delete",
    renamePrompt: "Rename session",
    deleteConfirm: "Delete this session? This cannot be undone.",
    emptyTitle: "How can I help you today?",
    emptyBody: "I can execute code, read files, run terminal commands, and help you build your project.",
    suggestionRead: "Read project files",
    suggestionReadHint: "Explore current directory",
    suggestionTest: "Run test suite",
    suggestionTestHint: "Execute the configured tests",
  },
  zh: {
    langSwitch: "EN",
    workspace: "工作区",
    loginSubtitle: "安全连接到你的服务器工作区",
    username: "用户名",
    password: "密码",
    signIn: "连接到工作区",
    newChat: "新会话",
    conversations: "对话",
    searchSessions: "搜索会话...",
    today: "今天",
    yesterday: "昨天",
    previous7Days: "最近 7 天",
    older: "更早",
    disconnected: "未连接",
    connecting: "连接中",
    connected: "已连接",
    connectionError: "连接异常",
    opening: "打开中",
    ready: "就绪",
    running: "运行中",
    canceling: "正在停止",
    runActive: "当前对话已有任务在运行。",
    error: "错误",
    noAgent: "没有可用 Agent",
    noBridge: "Bridge 未连接",
    noChats: "暂无对话",
    noMatches: "没有匹配的会话",
    unknownError: "未知错误",
    wsDisconnected: "WebSocket 未连接",
    stop: "停止",
    logout: "退出",
    send: "发送",
    sendHint: "Enter 发送，Shift+Enter 换行",
    promptPlaceholder: "让 Codex 检查、修改或解释你的工作区",
    metricAgent: "Agent",
    metricAgentHint: "Bridge 状态",
    metricSessions: "会话",
    metricSessionHint: "对话标签",
    metricRunnerHint: "当前运行时",
    online: "在线",
    offline: "离线",
    agentOnline: "Agent 在线",
    agentOffline: "Agent 离线",
    agent: "Agent",
    reverseTunnel: "反向隧道",
    runtime: "运行时",
    session: "会话",
    runner: "运行器",
    thread: "线程",
    roleUser: "你",
    roleAssistant: "Codex",
    roleSystem: "系统",
    openNavigation: "打开导航",
    closeNavigation: "关闭导航",
    collapseSidebar: "折叠侧边栏",
    expandSidebar: "展开侧边栏",
    refresh: "刷新",
    copy: "复制",
    copied: "已复制",
    copyFailed: "复制失败",
    copyMessage: "复制消息",
    copyCode: "复制代码",
    tool: "工具",
    toolRunning: "执行中",
    toolCompleted: "已完成",
    toolFailed: "失败",
    settings: "设置",
    close: "关闭",
    account: "账户",
    appearance: "外观",
    theme: "主题",
    light: "浅色",
    dark: "深色",
    agentsRuntime: "Agents 与运行时",
    localAdmin: "本地管理员",
    cancel: "取消",
    savePreferences: "保存偏好",
    autoExecute: "自动执行",
    attach: "附件",
    rename: "重命名",
    delete: "删除",
    renamePrompt: "重命名会话",
    deleteConfirm: "删除这个会话？此操作不可撤销。",
    emptyTitle: "今天想处理什么？",
    emptyBody: "我可以执行代码、读取文件、运行终端命令，并协助你构建项目。",
    suggestionRead: "读取项目文件",
    suggestionReadHint: "浏览当前目录",
    suggestionTest: "运行测试套件",
    suggestionTestHint: "执行项目测试",
  },
};

const t = (key) => i18n[state.lang][key] || i18n.en[key] || key;

const icons = {
  terminal: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 7.5 10 12l-5 4.5M12 17h7"/></svg>',
  user: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 21a8 8 0 0 0-16 0M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z"/></svg>',
  alert: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 9v4M12 17h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/></svg>',
  message: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4Z"/></svg>',
  edit: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>',
  trash: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18M8 6V4h8v2M6 6l1 15h10l1-15M10 11v6M14 11v6"/></svg>',
  panelClose: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 18 9 12l6-6M4 4v16"/></svg>',
  panelOpen: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18 6-6-6-6M4 4v16"/></svg>',
};

async function api(path, options = {}) {
  const res = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.message || body.code || `HTTP ${res.status}`);
  }
  return body;
}

function show(view) {
  el("loginView").classList.toggle("hidden", view !== "login");
  el("chatView").classList.toggle("hidden", view !== "chat");
  document.body.classList.toggle("is-chat", view === "chat");
  updateConnectionKeeper();
}

function setText(id, value) {
  const node = el(id);
  if (node) node.textContent = value;
}

function setButtonText(id, value) {
  const node = el(id);
  if (!node) return;
  const label = node.querySelector("span");
  if (label) {
    label.textContent = value;
  } else {
    node.textContent = value;
  }
}

function setAttr(id, attr, value) {
  const node = el(id);
  if (node) node.setAttribute(attr, value);
}

function applyTheme() {
  document.documentElement.classList.toggle("dark", state.theme === "dark");
  const themeColor = state.theme === "dark" ? "#0a0a0a" : "#ffffff";
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", themeColor);
  el("themeLightBtn")?.classList.toggle("active", state.theme === "light");
  el("themeDarkBtn")?.classList.toggle("active", state.theme === "dark");
}

function setTheme(theme) {
  state.theme = normalizeTheme(theme);
  localStorage.setItem("codexBridge.theme", state.theme);
  applyTheme();
}

function applyLanguage() {
  document.documentElement.lang = state.lang === "zh" ? "zh-CN" : "en";
  setButtonText("loginLangBtn", t("langSwitch"));
  setButtonText("langBtn", t("langSwitch"));
  setText("loginSubtitle", t("loginSubtitle"));
  setText("usernameLabel", t("username"));
  setText("passwordLabel", t("password"));
  setText("loginSubmitBtn", t("signIn"));
  setText("newChatText", t("newChat"));
  setText("conversationLabel", t("conversations"));
  setText("metricAgentLabel", t("metricAgent"));
  setText("metricAgentHint", t("metricAgentHint"));
  setText("metricSessionLabel", t("metricSessions"));
  setText("metricSessionHint", t("metricSessionHint"));
  setText("metricRunnerLabel", t("runner"));
  setText("metricRunnerHint", t("metricRunnerHint"));
  setText("agentTitle", t("agent"));
  setText("agentSubtitle", t("reverseTunnel"));
  setText("runtimeSessionLabel", t("session"));
  setText("runtimeRunnerLabel", t("runner"));
  setText("runtimeThreadLabel", t("thread"));
  setText("settingsTitle", t("settings"));
  setText("accountLabel", t("account"));
  setText("appearanceLabel", t("appearance"));
  setText("themeLabel", t("theme"));
  setText("themeLightBtn", t("light"));
  setText("themeDarkBtn", t("dark"));
  setText("agentsRuntimeLabel", t("agentsRuntime"));
  setText("settingsRole", t("localAdmin"));
  setText("cancelSettingsBtn", t("cancel"));
  setText("saveSettingsBtn", t("savePreferences"));
  setText("settingsLogoutBtn", t("logout"));
  setText("autoExecuteText", t("autoExecute"));
  setAttr("sessionSearch", "placeholder", t("searchSessions"));
  setAttr("prompt", "placeholder", t("promptPlaceholder"));
  setAttr("prompt", "title", t("sendHint"));
  setAttr("prompt", "aria-label", t("promptPlaceholder"));
  setAttr("sendBtn", "title", t("send"));
  setAttr("sendBtn", "aria-label", t("send"));
  setAttr("refreshBtn", "title", t("refresh"));
  setAttr("refreshBtn", "aria-label", t("refresh"));
  setAttr("openNavBtn", "title", t("openNavigation"));
  setAttr("openNavBtn", "aria-label", t("openNavigation"));
  setAttr("closeNavBtn", "title", t("closeNavigation"));
  setAttr("closeNavBtn", "aria-label", t("closeNavigation"));
  setAttr("settingsBtn", "title", t("settings"));
  setAttr("settingsBtn", "aria-label", t("settings"));
  setAttr("settingsTopBtn", "title", t("settings"));
  setAttr("settingsTopBtn", "aria-label", t("settings"));
  setAttr("closeSettingsBtn", "title", t("close"));
  setAttr("closeSettingsBtn", "aria-label", t("close"));
  setAttr("attachBtn", "title", t("attach"));
  setAttr("attachBtn", "aria-label", t("attach"));
  setAttr("logoutBtn", "title", t("logout"));
  setAttr("logoutBtn", "aria-label", t("logout"));
  applySidebarState();
  renderAgents();
  renderSessions();
  renderMetrics();
  renderRuntime();
  renderUser();
  relabelRoles();
  relabelCopyButtons();
  updateComposerState();
  if (state.statusKey) {
    setStatusKey(state.statusKey);
  } else if (state.statusText) {
    setStatus(state.statusText);
  }
  if (!state.activeSession || isMessagesEmptyState()) {
    renderEmptyState();
  }
}

function toggleLanguage() {
  state.lang = state.lang === "zh" ? "en" : "zh";
  localStorage.setItem("codexBridge.lang", state.lang);
  applyLanguage();
}

function applySidebarState() {
  const button = el("collapseSidebarBtn");
  if (!button) return;
  el("chatView").classList.toggle("sidebar-collapsed", state.sidebarCollapsed);
  button.innerHTML = state.sidebarCollapsed ? icons.panelOpen : icons.panelClose;
  button.title = state.sidebarCollapsed ? t("expandSidebar") : t("collapseSidebar");
  button.setAttribute("aria-label", button.title);
}

function toggleSidebarCollapsed() {
  state.sidebarCollapsed = !state.sidebarCollapsed;
  localStorage.setItem("codexBridge.sidebarCollapsed", state.sidebarCollapsed ? "1" : "0");
  applySidebarState();
}

async function boot() {
  applyTheme();
  applyLanguage();
  try {
    const me = await api("/api/me");
    state.user = me.user;
    renderUser();
    show("chat");
    applySidebarState();
    await refreshAll();
  } catch {
    show("login");
  }
}

async function refreshAll() {
  await Promise.all([loadAgents(), loadSessions()]);
  if (state.activeSession && !state.sessions.some((session) => session.id === state.activeSession.id)) {
    closeWS();
    state.activeSession = null;
  }
  if (!state.activeSession && state.sessions.length) {
    await selectSession(state.sessions[0].id);
  }
  if (!state.activeSession && state.agents.length) {
    await createSession();
  }
  renderMetrics();
  updateComposerState();
}

async function loadAgents() {
  const data = await api("/api/agents");
  state.agents = data.agents || [];
  renderAgents();
  renderMetrics();
}

async function loadSessions() {
  const data = await api("/api/sessions");
  state.sessions = data.sessions || [];
  if (state.activeSession) {
    const fresh = state.sessions.find((session) => session.id === state.activeSession.id);
    if (fresh) state.activeSession = fresh;
  }
  renderSessions();
  renderMetrics();
}

function renderUser() {
  const username = state.user?.username || t("workspace");
  setText("mobileUser", username);
  setText("settingsUsername", username);
  setText("settingsAvatar", initials(username));
}

function renderAgents() {
  const list = el("agentList");
  if (list) {
    list.textContent = "";
    if (!state.agents.length) {
      list.append(emptyLine(t("noBridge")));
    } else {
      for (const agent of state.agents) {
        const row = document.createElement("div");
        row.className = "agent-item";
        row.innerHTML = `<span><i class="dot ${agent.online ? "online" : ""}"></i>${escapeHtml(agent.name)}</span><small>${escapeHtml(agent.hostname || agent.machineId)}</small>`;
        list.append(row);
      }
    }
  }
  renderSettingsAgents();
  renderMetrics();
}

function renderSettingsAgents() {
  const list = el("settingsAgentList");
  if (!list) return;
  list.textContent = "";
  if (!state.agents.length) {
    list.append(emptyLine(t("noBridge")));
    return;
  }
  for (const agent of state.agents) {
    const row = document.createElement("div");
    row.className = "settings-agent";
    row.innerHTML = `
      <div>
        <strong>${escapeHtml(agent.name)}</strong>
        <span>${escapeHtml(agent.hostname || agent.machineId || "-")}</span>
      </div>
      <span class="agent-status ${agent.online ? "online" : ""}">${agent.online ? t("online") : t("offline")}</span>
    `;
    list.append(row);
  }
}

function renderSessions() {
  const list = el("sessionList");
  if (!list) return;
  list.textContent = "";

  const filtered = state.sessions.filter((session) => {
    const query = state.sessionQuery.trim().toLowerCase();
    if (!query) return true;
    return displaySessionTitle(session).toLowerCase().includes(query);
  });

  if (!state.sessions.length) {
    list.append(emptyLine(t("noChats")));
    return;
  }
  if (!filtered.length) {
    list.append(emptyLine(t("noMatches")));
    return;
  }

  for (const [label, sessions] of groupSessions(filtered)) {
    const group = document.createElement("section");
    group.className = "session-group";
    const heading = document.createElement("div");
    heading.className = "session-group-label";
    heading.textContent = label;
    group.append(heading);
    for (const session of sessions) {
      group.append(sessionItem(session));
    }
    list.append(group);
  }
}

function sessionItem(session) {
  const row = document.createElement("div");
  row.className = `session-item ${state.activeSession?.id === session.id ? "active" : ""}`;
  row.role = "button";
  row.tabIndex = 0;
  row.innerHTML = `
    ${icons.message}
    <span class="session-main">
      <span class="session-title">${escapeHtml(displaySessionTitle(session))}</span>
      <small class="session-time">${formatDateTime(session.updatedAt)}</small>
    </span>
    <span class="session-actions">
      <button class="mini-action edit-session" type="button" title="${escapeHtml(t("rename"))}" aria-label="${escapeHtml(t("rename"))}">${icons.edit}</button>
      <button class="mini-action danger delete-session" type="button" title="${escapeHtml(t("delete"))}" aria-label="${escapeHtml(t("delete"))}">${icons.trash}</button>
    </span>
  `;
  row.addEventListener("click", (event) => {
    if (event.target.closest(".mini-action")) return;
    el("chatView").classList.remove("nav-open");
    selectSession(session.id);
  });
  row.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      selectSession(session.id);
    }
  });
  row.querySelector(".edit-session").addEventListener("click", (event) => {
    event.stopPropagation();
    renameSession(session).catch((err) => showError(err));
  });
  row.querySelector(".delete-session").addEventListener("click", (event) => {
    event.stopPropagation();
    deleteSession(session).catch((err) => showError(err));
  });
  return row;
}

function groupSessions(sessions) {
  const groups = new Map();
  for (const session of sessions) {
    const label = sessionDateLabel(session.updatedAt);
    if (!groups.has(label)) groups.set(label, []);
    groups.get(label).push(session);
  }
  return groups;
}

function sessionDateLabel(timestamp) {
  const date = new Date((timestamp || 0) * 1000);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const target = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const diffDays = Math.round((today - target) / 86400000);
  if (diffDays <= 0) return t("today");
  if (diffDays === 1) return t("yesterday");
  if (diffDays <= 7) return t("previous7Days");
  return t("older");
}

function renderMetrics() {
  const onlineAgents = state.agents.filter((agent) => agent.online).length;
  const hasOnline = onlineAgents > 0;
  setText("metricAgentValue", hasOnline ? t("online") : t("offline"));
  setText("metricSessionValue", String(state.sessions.length));
  setText("metricRunnerValue", state.runner || "-");
  setText("headerAgentStatus", hasOnline ? t("agentOnline") : t("agentOffline"));
  el("headerAgentDot")?.classList.toggle("online", hasOnline);
}

function emptyLine(text) {
  const div = document.createElement("div");
  div.className = "agent-item";
  div.innerHTML = `<small>${escapeHtml(text)}</small>`;
  return div;
}

async function createSession() {
  const online = state.agents.find((agent) => agent.online) || state.agents[0];
  if (!online) {
    setStatusKey("noAgent");
    return;
  }
  const data = await api("/api/sessions", {
    method: "POST",
    body: JSON.stringify({ agentId: online.id, title: "New Session" }),
  });
  state.sessions = [data.session, ...state.sessions.filter((session) => session.id !== data.session.id)];
  renderSessions();
  el("chatView").classList.remove("nav-open");
  await selectSession(data.session.id);
}

async function renameSession(session) {
  const title = window.prompt(t("renamePrompt"), displaySessionTitle(session));
  if (title === null) return;
  const trimmed = title.trim();
  if (!trimmed || trimmed === session.title) return;
  const data = await api(`/api/sessions/${encodeURIComponent(session.id)}`, {
    method: "PATCH",
    body: JSON.stringify({ title: trimmed }),
  });
  state.sessions = state.sessions.map((item) => (item.id === data.session.id ? data.session : item));
  if (state.activeSession?.id === data.session.id) {
    state.activeSession = data.session;
    el("chatTitle").textContent = displaySessionTitle(data.session);
  }
  renderSessions();
  renderMetrics();
}

async function deleteSession(session) {
  if (!window.confirm(t("deleteConfirm"))) return;
  if (state.activeSession?.id === session.id && state.ws?.readyState === WebSocket.OPEN && state.activeRun) {
    try {
      state.ws.send(JSON.stringify({ type: "cancel", sid: session.id }));
    } catch {
      // The delete request below is authoritative for local state.
    }
  }
  await api(`/api/sessions/${encodeURIComponent(session.id)}`, { method: "DELETE" });
  state.sessions = state.sessions.filter((item) => item.id !== session.id);
  if (state.activeSession?.id === session.id) {
    closeWS();
    state.activeSession = null;
    state.activeRun = null;
    state.toolNodes = new Map();
    el("messages").textContent = "";
    if (state.sessions.length) {
      await selectSession(state.sessions[0].id);
    } else {
      el("chatTitle").textContent = "Codex Bridge";
      state.runner = "-";
      state.thread = "-";
      renderRuntime();
      renderEmptyState();
    }
  }
  renderSessions();
  renderMetrics();
  updateComposerState();
}

async function selectSession(id) {
  const session = state.sessions.find((item) => item.id === id);
  if (!session) return;
  state.activeSession = session;
  state.runner = "-";
  state.thread = session.remoteThreadId || "-";
  state.assistantNode = null;
  state.assistantText = "";
  state.activeRun = null;
  state.toolNodes = new Map();
  state.pendingPrompt = null;
  renderSessions();
  renderRuntime();
  renderMetrics();
  el("chatTitle").textContent = displaySessionTitle(session);
  el("messages").textContent = "";
  try {
    const data = await api(`/api/sessions/${encodeURIComponent(id)}/messages`);
    const messages = data.messages || [];
    if (!messages.length) {
      renderEmptyState();
    } else {
      for (const msg of messages) {
        appendMessage(msg.role, msg.content, msg.createdAt);
      }
    }
    await loadRuns(id);
    connectWS();
    updateConnectionKeeper();
  } catch (err) {
    showError(err);
  }
}

async function loadRuns(id) {
  const data = await api(`/api/sessions/${encodeURIComponent(id)}/runs`);
  const runs = data.runs || [];
  state.activeRun = runs.find((run) => run.status === "queued" || run.status === "running") || null;
  updateComposerState();
  if (state.activeRun) setStatusKey("running");
}

function connectWS() {
  clearReconnectTimer();
  closeWS();
  if (!shouldKeepChatConnected()) return;
  const sid = state.activeSession.id;
  const seq = ++state.wsSeq;
  const scheme = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${scheme}://${location.host}/ws/chat?sid=${encodeURIComponent(sid)}`);
  state.ws = ws;
  setStatusKey("connecting");
  ws.onopen = () => {
    if (!isCurrentWS(ws, seq, sid)) return;
    state.reconnectDelay = 1000;
    setStatusKey("connected");
    sendHeartbeat();
  };
  ws.onclose = () => {
    if (!isCurrentWS(ws, seq, sid)) return;
    state.ws = null;
    if (shouldKeepChatConnected()) {
      scheduleReconnect();
    } else {
      setStatusKey("disconnected");
    }
  };
  ws.onerror = () => {
    if (!isCurrentWS(ws, seq, sid)) return;
    setStatusKey("connectionError");
  };
  ws.onmessage = (event) => {
    if (!isCurrentWS(ws, seq, sid)) return;
    let env;
    try {
      env = JSON.parse(event.data);
    } catch {
      return;
    }
    handleEnvelope(env);
  };
}

function closeWS() {
  clearReconnectTimer();
  if (state.ws) {
    const ws = state.ws;
    state.ws = null;
    state.wsSeq++;
    ws.close();
  }
}

function isCurrentWS(ws, seq, sid) {
  return state.ws === ws && state.wsSeq === seq && state.activeSession?.id === sid;
}

function shouldKeepChatConnected() {
  return Boolean(state.user && state.activeSession && !el("chatView").classList.contains("hidden"));
}

function updateConnectionKeeper() {
  if (shouldKeepChatConnected()) {
    startConnectionKeeper();
    ensureChatConnection();
    return;
  }
  stopConnectionKeeper();
}

function startConnectionKeeper() {
  if (state.keepaliveTimer) return;
  state.keepaliveTimer = window.setInterval(maintainChatConnection, HEARTBEAT_MS);
}

function stopConnectionKeeper() {
  if (state.keepaliveTimer) {
    window.clearInterval(state.keepaliveTimer);
    state.keepaliveTimer = null;
  }
  clearReconnectTimer();
}

function maintainChatConnection() {
  if (!shouldKeepChatConnected()) {
    stopConnectionKeeper();
    return;
  }
  if (state.ws?.readyState === WebSocket.OPEN) {
    sendHeartbeat();
    return;
  }
  if (!state.ws || state.ws.readyState === WebSocket.CLOSED || state.ws.readyState === WebSocket.CLOSING) {
    scheduleReconnect(0);
  }
}

function ensureChatConnection() {
  if (!shouldKeepChatConnected()) return;
  if (state.ws?.readyState === WebSocket.OPEN) {
    sendHeartbeat();
    return;
  }
  if (state.ws?.readyState === WebSocket.CONNECTING) return;
  scheduleReconnect(0);
}

function scheduleReconnect(delay) {
  if (!shouldKeepChatConnected() || state.reconnectTimer) return;
  const wait = Number.isFinite(delay) ? delay : state.reconnectDelay;
  setStatusKey("connecting");
  state.reconnectTimer = window.setTimeout(() => {
    state.reconnectTimer = null;
    if (!shouldKeepChatConnected()) return;
    state.reconnectDelay = Math.min(state.reconnectDelay * 2, RECONNECT_MAX_MS);
    connectWS();
  }, wait);
}

function clearReconnectTimer() {
  if (!state.reconnectTimer) return;
  window.clearTimeout(state.reconnectTimer);
  state.reconnectTimer = null;
}

function sendHeartbeat() {
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN || !state.activeSession) return;
  try {
    state.ws.send(JSON.stringify({ type: "heartbeat", sid: state.activeSession.id, payload: { ts: Date.now() } }));
  } catch {
    scheduleReconnect(0);
  }
}

function handleEnvelope(env) {
  if (env.sid && state.activeSession && env.sid !== state.activeSession.id) return;
  const payload = env.payload || {};
  switch (env.type) {
    case "status":
      setStatusKey(statusKey(payload.status) || "connected");
      if (payload.runId) {
        state.activeRun = { id: payload.runId, promptId: payload.promptId, status: payload.status || "running" };
        state.pendingPrompt = null;
        updateComposerState();
      } else if (payload.status === "canceling" && state.activeRun) {
        state.activeRun = { ...state.activeRun, status: "canceling" };
        updateComposerState();
      }
      break;
    case "session_opened":
      state.runner = payload.runner || state.runner;
      state.thread = payload.remoteThreadId || state.thread;
      renderRuntime();
      renderMetrics();
      if (state.activeRun) {
        setStatusKey(state.activeRun.status || "running");
      } else {
        setStatusKey("ready");
      }
      break;
    case "session_update":
      if (payload.runId) {
        const currentStatus = state.activeRun?.id === payload.runId ? state.activeRun.status : "";
        state.activeRun = {
          id: payload.runId,
          promptId: payload.promptId,
          status: currentStatus === "canceling" ? "canceling" : "running",
        };
        updateComposerState();
      }
      if (payload.tool) appendToolEvent(payload.tool);
      if (payload.content) {
        setAssistantContent(payload.content);
      } else {
        appendAssistantDelta(payload.delta || "");
      }
      break;
    case "prompt_complete":
      if (payload.content) setAssistantContent(payload.content);
      state.thread = payload.remoteThreadId || state.thread;
      state.assistantNode = null;
      state.assistantText = "";
      state.activeRun = null;
      state.pendingPrompt = null;
      touchActiveSession();
      renderRuntime();
      renderMetrics();
      updateComposerState();
      setStatusKey("ready");
      break;
    case "error":
      if (payload.code === "SESSION_DELETED") {
        closeWS();
        state.activeSession = null;
        renderEmptyState();
        setStatusKey("disconnected");
        return;
      }
      if (payload.code === "CANCELED") {
        appendMessage("system", payload.message || t("canceling"));
      } else {
        appendMessage("system", payload.message || t("unknownError"));
      }
      const sameRun = payload.runId
        ? state.activeRun?.id === payload.runId
        : payload.promptId && state.activeRun?.promptId === payload.promptId;
      if (sameRun) {
        state.activeRun = null;
        state.pendingPrompt = null;
        updateComposerState();
      } else if (payload.code === "RUN_ACTIVE" && payload.runId) {
        state.activeRun = { id: payload.runId, promptId: payload.promptId, status: "running" };
        updateComposerState();
      }
      setStatus(errorStatusText(payload));
      break;
    case "heartbeat":
      break;
  }
}

function appendMessage(role, content, createdAt) {
  clearEmptyState();
  const wrap = document.createElement("article");
  wrap.className = `message ${role}`;
  const avatar = document.createElement("div");
  avatar.className = "message-avatar";
  avatar.innerHTML = role === "user" ? icons.user : role === "system" ? icons.alert : icons.terminal;

  const body = document.createElement("div");
  body.className = "message-body";
  const meta = document.createElement("div");
  meta.className = "message-meta";
  const label = document.createElement("div");
  label.className = "role";
  label.dataset.role = role;
  label.textContent = roleLabel(role);
  const time = document.createElement("span");
  time.className = "message-time";
  time.textContent = formatTime(createdAt);
  const bubble = document.createElement("div");
  bubble.className = "bubble";
  bubble.dataset.rawContent = content || "";
  const copyButton = createCopyButton("message", () => bubble.dataset.rawContent || "");

  renderMarkdownInto(bubble, content);
  meta.append(label, time, copyButton);
  body.append(meta, bubble);
  wrap.append(avatar, body);
  el("messages").append(wrap);
  scrollMessagesToBottom();
  return bubble;
}

function appendAssistantDelta(delta) {
  if (!delta) return;
  if (!state.assistantNode) {
    state.assistantNode = appendMessage("assistant", "");
    state.assistantText = "";
  }
  state.assistantText += delta;
  state.assistantNode.dataset.rawContent = state.assistantText;
  renderMarkdownInto(state.assistantNode, state.assistantText);
  scrollMessagesToBottom();
}

function setAssistantContent(content) {
  if (!content) return;
  if (!state.assistantNode) {
    state.assistantNode = appendMessage("assistant", "");
  }
  state.assistantText = content;
  state.assistantNode.dataset.rawContent = content;
  renderMarkdownInto(state.assistantNode, content);
  scrollMessagesToBottom();
}

function appendToolEvent(tool) {
  if (!tool) return;
  clearEmptyState();
  const key = tool.id || tool.command || `tool-${state.toolNodes.size}`;
  let node = state.toolNodes.get(key);
  if (!node) {
    node = document.createElement("details");
    node.className = "tool-event";
    node.open = true;
    const summary = document.createElement("summary");
    const body = document.createElement("pre");
    body.className = "tool-output";
    node.append(summary, body);
    el("messages").append(node);
    state.toolNodes.set(key, node);
  }
  const summary = node.querySelector("summary");
  const body = node.querySelector(".tool-output");
  const status = tool.status || "running";
  summary.textContent = `${t("tool")} · ${toolStatusLabel(status)}${tool.command ? ` · ${tool.command}` : ""}`;
  const exit = tool.exitCode === 0 || tool.exitCode ? `\n\nexit: ${tool.exitCode}` : "";
  body.textContent = `${tool.command || ""}${tool.output ? `\n\n${tool.output}` : ""}${exit}`.trim();
  scrollMessagesToBottom();
}

function toolStatusLabel(status) {
  switch (status) {
    case "completed":
      return t("toolCompleted");
    case "failed":
      return t("toolFailed");
    default:
      return t("toolRunning");
  }
}

function sendPrompt(text) {
  if (!state.activeSession) {
    appendMessage("system", t("noChats"));
    return;
  }
  if (state.activeRun) {
    appendMessage("system", t("runActive"));
    return;
  }
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
    ensureChatConnection();
    appendMessage("system", t("wsDisconnected"));
    return;
  }
  appendMessage("user", text);
  state.assistantNode = null;
  state.assistantText = "";
  state.toolNodes = new Map();
  const promptId = newID("prm");
  state.activeRun = { promptId, status: "running" };
  state.pendingPrompt = promptId;
  updateComposerState();
  setStatusKey("running");
  try {
    state.ws.send(JSON.stringify({ type: "prompt", sid: state.activeSession.id, payload: { content: text, promptId } }));
  } catch {
    state.activeRun = null;
    state.pendingPrompt = null;
    updateComposerState();
    appendMessage("system", t("wsDisconnected"));
    ensureChatConnection();
  }
}

async function submitPrompt() {
  const text = el("prompt").value.trim();
  if (!text || state.activeRun) return;
  if (!state.activeSession && state.agents.length) {
    await createSession();
  }
  el("prompt").value = "";
  resizePrompt();
  sendPrompt(text);
}

function updateComposerState() {
  const busy = Boolean(state.activeRun);
  el("sendBtn").disabled = busy || !state.activeSession;
  el("prompt").disabled = busy || (!state.activeSession && !state.agents.length);
  el("stopBtn").disabled = !busy || state.activeRun?.status === "canceling";
  el("stopBtn").classList.toggle("hidden", !busy);
  if (state.activeRun?.status === "canceling") {
    setText("stopBtn", t("canceling"));
  } else {
    setText("stopBtn", t("stop"));
  }
  el("autoExecuteBtn").classList.toggle("active", state.autoExecute);
}

function newID(prefix) {
  if (!window.crypto?.getRandomValues) {
    return `${prefix}_${Date.now().toString(16)}${Math.random().toString(16).slice(2)}`;
  }
  const random = window.crypto.getRandomValues(new Uint32Array(4));
  return `${prefix}_${Array.from(random, (part) => part.toString(16).padStart(8, "0")).join("")}`;
}

function resizePrompt() {
  const prompt = el("prompt");
  prompt.style.height = "auto";
  prompt.style.height = `${Math.min(prompt.scrollHeight, 300)}px`;
}

function scrollMessagesToBottom() {
  const messages = el("messages");
  messages.scrollTop = messages.scrollHeight;
}

function setStatus(text) {
  state.statusKey = "";
  state.statusText = text;
  el("connStatus").textContent = text;
}

function setStatusKey(key) {
  state.statusKey = key;
  state.statusText = "";
  el("connStatus").textContent = t(key);
}

function statusKey(value) {
  if (!value) return "";
  return {
    opening: "opening",
    connected: "connected",
    disconnected: "disconnected",
    ready: "ready",
    running: "running",
    canceling: "canceling",
  }[String(value).toLowerCase()] || "";
}

function errorStatusText(payload) {
  if (payload.code === "CANCELED") return t("ready");
  if (payload.code === "RUN_ACTIVE") return t("runActive");
  return payload.code || t("error");
}

function renderRuntime() {
  el("runtimeSession").textContent = state.activeSession?.id || "-";
  el("runtimeRunner").textContent = state.runner || "-";
  el("runtimeThread").textContent = state.thread || "-";
}

function renderEmptyState() {
  const messages = el("messages");
  if (!messages || messages.children.length && !isMessagesEmptyState()) return;
  messages.dataset.empty = "1";
  messages.innerHTML = `
    <section class="empty-state">
      <div class="empty-icon">${icons.terminal}</div>
      <div>
        <h2>${escapeHtml(t("emptyTitle"))}</h2>
        <p>${escapeHtml(t("emptyBody"))}</p>
      </div>
      <div class="suggestions">
        <button class="suggestion-button" type="button" data-prompt="${escapeHtml(t("suggestionRead"))}">
          <strong>${escapeHtml(t("suggestionRead"))}</strong>
          <span>${escapeHtml(t("suggestionReadHint"))}</span>
        </button>
        <button class="suggestion-button" type="button" data-prompt="${escapeHtml(t("suggestionTest"))}">
          <strong>${escapeHtml(t("suggestionTest"))}</strong>
          <span>${escapeHtml(t("suggestionTestHint"))}</span>
        </button>
      </div>
    </section>
  `;
  for (const button of messages.querySelectorAll(".suggestion-button")) {
    button.addEventListener("click", () => {
      el("prompt").value = button.dataset.prompt || "";
      resizePrompt();
      el("prompt").focus();
    });
  }
}

function clearEmptyState() {
  const messages = el("messages");
  if (messages?.dataset.empty === "1") {
    messages.textContent = "";
    delete messages.dataset.empty;
  }
}

function isMessagesEmptyState() {
  return el("messages")?.dataset.empty === "1";
}

function openSettings() {
  renderUser();
  renderSettingsAgents();
  applyTheme();
  el("settingsOverlay").classList.remove("hidden");
}

function closeSettings() {
  el("settingsOverlay").classList.add("hidden");
}

function showError(err) {
  const message = err?.message || t("unknownError");
  if (el("chatView").classList.contains("hidden")) {
    el("loginError").textContent = message;
  } else {
    appendMessage("system", message);
  }
}

function touchActiveSession() {
  if (!state.activeSession) return;
  const updated = { ...state.activeSession, updatedAt: Math.floor(Date.now() / 1000) };
  state.activeSession = updated;
  state.sessions = [updated, ...state.sessions.filter((session) => session.id !== updated.id)];
  renderSessions();
}

function initials(value) {
  const text = String(value || "CB").trim();
  if (!text) return "CB";
  const parts = text.split(/\s+/).slice(0, 2);
  return parts.map((part) => part[0]).join("").toUpperCase();
}

function formatDateTime(timestamp) {
  if (!timestamp) return "";
  return new Date(timestamp * 1000).toLocaleString();
}

function formatTime(timestamp) {
  const date = timestamp ? new Date(timestamp * 1000) : new Date();
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function escapeHtml(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  })[ch]);
}

function renderMarkdownInto(node, markdown) {
  node.innerHTML = markdownToHTML(markdown);
  node.dataset.rawContent = markdown || "";
  for (const link of node.querySelectorAll("a")) {
    link.target = "_blank";
    link.rel = "noopener noreferrer";
  }
  enhanceCodeBlocks(node);
}

function markdownToHTML(markdown) {
  const blocks = [];
  const text = String(markdown || "").replace(/\r\n/g, "\n");
  const withoutCode = text.replace(/```([a-zA-Z0-9_-]*)\n?([\s\S]*?)```/g, (_, lang, code) => {
    const token = `\u0000CODE${blocks.length}\u0000`;
    blocks.push(`<pre><code${lang ? ` data-lang="${escapeHtml(lang)}"` : ""}>${escapeHtml(trimTrailingNewline(code))}</code></pre>`);
    return token;
  });
  const lines = withoutCode.split("\n");
  const html = [];
  let paragraph = [];
  let list = null;
  let quote = [];

  const flushParagraph = () => {
    if (!paragraph.length) return;
    html.push(`<p>${inlineMarkdown(paragraph.join(" "))}</p>`);
    paragraph = [];
  };
  const flushList = () => {
    if (!list) return;
    html.push(`<${list.type}>${list.items.map((item) => `<li>${inlineMarkdown(item)}</li>`).join("")}</${list.type}>`);
    list = null;
  };
  const flushQuote = () => {
    if (!quote.length) return;
    html.push(`<blockquote>${quote.map((line) => inlineMarkdown(line)).join("<br>")}</blockquote>`);
    quote = [];
  };
  const flushAll = () => {
    flushParagraph();
    flushList();
    flushQuote();
  };

  for (const line of lines) {
    const codeToken = line.match(/^\u0000CODE(\d+)\u0000$/);
    if (codeToken) {
      flushAll();
      html.push(blocks[Number(codeToken[1])] || "");
      continue;
    }
    if (!line.trim()) {
      flushAll();
      continue;
    }
    const heading = line.match(/^(#{1,3})\s+(.+)$/);
    if (heading) {
      flushAll();
      html.push(`<h${heading[1].length}>${inlineMarkdown(heading[2])}</h${heading[1].length}>`);
      continue;
    }
    const quoteLine = line.match(/^>\s?(.*)$/);
    if (quoteLine) {
      flushParagraph();
      flushList();
      quote.push(quoteLine[1]);
      continue;
    }
    const unordered = line.match(/^\s*[-*]\s+(.+)$/);
    const ordered = line.match(/^\s*\d+[.)]\s+(.+)$/);
    if (unordered || ordered) {
      flushParagraph();
      flushQuote();
      const type = unordered ? "ul" : "ol";
      if (!list || list.type !== type) {
        flushList();
        list = { type, items: [] };
      }
      list.items.push((unordered || ordered)[1]);
      continue;
    }
    flushList();
    flushQuote();
    paragraph.push(line.trim());
  }
  flushAll();
  return restoreCodeBlocks(html.join(""), blocks);
}

function inlineMarkdown(text) {
  const codes = [];
  const links = [];
  let value = escapeHtml(text).replace(/`([^`]+)`/g, (_, code) => {
    const token = `\u0000INLINE${codes.length}\u0000`;
    codes.push(`<code>${code}</code>`);
    return token;
  });
  value = value
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/\*([^*]+)\*/g, "<em>$1</em>")
    .replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, (_, label, href) => {
      const token = `\u0000LINK${links.length}\u0000`;
      links.push(`<a href="${href}">${label}</a>`);
      return token;
    })
    .replace(/(https?:\/\/[^\s<]+)/g, (_, href) => {
      const token = `\u0000LINK${links.length}\u0000`;
      links.push(`<a href="${href}">${href}</a>`);
      return token;
    });
  return restoreLinks(restoreInlineCode(value, codes), links);
}

function restoreCodeBlocks(html, blocks) {
  return html.replace(/\u0000CODE(\d+)\u0000/g, (_, index) => blocks[Number(index)] || "");
}

function restoreInlineCode(html, codes) {
  return html.replace(/\u0000INLINE(\d+)\u0000/g, (_, index) => codes[Number(index)] || "");
}

function restoreLinks(html, links) {
  return html.replace(/\u0000LINK(\d+)\u0000/g, (_, index) => links[Number(index)] || "");
}

function trimTrailingNewline(value) {
  return String(value).replace(/\n$/, "");
}

function enhanceCodeBlocks(node) {
  for (const pre of node.querySelectorAll("pre")) {
    if (pre.parentElement?.classList.contains("code-frame")) continue;
    const code = pre.querySelector("code");
    const frame = document.createElement("div");
    frame.className = "code-frame";
    const bar = document.createElement("div");
    bar.className = "code-toolbar";
    const lang = document.createElement("span");
    lang.className = "code-lang";
    lang.textContent = code?.dataset.lang || "code";
    const copyButton = createCopyButton("code", () => code?.textContent || pre.textContent || "");
    bar.append(lang, copyButton);
    pre.replaceWith(frame);
    frame.append(bar, pre);
  }
}

function createCopyButton(scope, getText) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = `copy-button ${scope}-copy`;
  button.dataset.copyScope = scope;
  setCopyButtonLabel(button);
  button.addEventListener("click", async (event) => {
    event.preventDefault();
    event.stopPropagation();
    try {
      await copyTextToClipboard(String(getText() || ""));
      flashCopyButton(button, "copied");
    } catch {
      flashCopyButton(button, "copyFailed");
    }
  });
  return button;
}

async function copyTextToClipboard(text) {
  if (navigator.clipboard?.writeText && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.inset = "0 auto auto 0";
  textarea.style.opacity = "0";
  document.body.append(textarea);
  textarea.select();
  const ok = document.execCommand("copy");
  textarea.remove();
  if (!ok) throw new Error("copy failed");
}

function flashCopyButton(button, key) {
  const token = String(Date.now());
  button.dataset.copyToken = token;
  button.classList.toggle("copied", key === "copied");
  button.textContent = t(key);
  button.title = t(key);
  button.setAttribute("aria-label", t(key));
  window.setTimeout(() => {
    if (button.dataset.copyToken !== token) return;
    button.classList.remove("copied");
    setCopyButtonLabel(button);
  }, 1400);
}

function setCopyButtonLabel(button) {
  const scope = button.dataset.copyScope || "message";
  const titleKey = scope === "code" ? "copyCode" : "copyMessage";
  button.textContent = t("copy");
  button.title = t(titleKey);
  button.setAttribute("aria-label", t(titleKey));
}

function displaySessionTitle(session) {
  if (!session?.title || session.title === "New chat" || session.title === "New Session") {
    return t("newChat");
  }
  return session.title;
}

function roleLabel(role) {
  switch (role) {
    case "user":
      return t("roleUser");
    case "assistant":
      return t("roleAssistant");
    case "system":
      return t("roleSystem");
    default:
      return role;
  }
}

function relabelRoles() {
  for (const node of document.querySelectorAll(".message .role")) {
    node.textContent = roleLabel(node.dataset.role || node.textContent.toLowerCase());
  }
}

function relabelCopyButtons() {
  for (const button of document.querySelectorAll(".copy-button")) {
    if (button.classList.contains("copied")) continue;
    setCopyButtonLabel(button);
  }
}

async function logout() {
  closeWS();
  state.activeSession = null;
  state.activeRun = null;
  await api("/api/logout", { method: "POST", body: "{}" });
  closeSettings();
  show("login");
}

function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) return;
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  });
}

el("loginForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  el("loginError").textContent = "";
  try {
    const username = el("username").value.trim();
    const password = el("password").value;
    const data = await api("/api/login", { method: "POST", body: JSON.stringify({ username, password }) });
    state.user = data.user;
    renderUser();
    show("chat");
    await refreshAll();
  } catch (err) {
    el("loginError").textContent = err.message;
  }
});

el("promptForm").addEventListener("submit", (event) => {
  event.preventDefault();
  submitPrompt().catch((err) => showError(err));
});

el("prompt").addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    submitPrompt().catch((err) => showError(err));
  }
});
el("prompt").addEventListener("input", resizePrompt);
el("prompt").addEventListener("focus", ensureChatConnection);

el("newChatBtn").addEventListener("click", () => createSession().catch((err) => showError(err)));
el("refreshBtn").addEventListener("click", () => refreshAll().catch((err) => showError(err)));
el("loginLangBtn").addEventListener("click", toggleLanguage);
el("langBtn").addEventListener("click", toggleLanguage);
el("collapseSidebarBtn").addEventListener("click", toggleSidebarCollapsed);
el("openNavBtn").addEventListener("click", () => el("chatView").classList.add("nav-open"));
el("closeNavBtn").addEventListener("click", () => el("chatView").classList.remove("nav-open"));
el("sessionSearch").addEventListener("input", (event) => {
  state.sessionQuery = event.target.value || "";
  renderSessions();
});
el("settingsBtn").addEventListener("click", openSettings);
el("settingsTopBtn").addEventListener("click", openSettings);
el("closeSettingsBtn").addEventListener("click", closeSettings);
el("cancelSettingsBtn").addEventListener("click", closeSettings);
el("saveSettingsBtn").addEventListener("click", closeSettings);
el("settingsOverlay").addEventListener("click", (event) => {
  if (event.target === el("settingsOverlay")) closeSettings();
});
el("themeLightBtn").addEventListener("click", () => setTheme("light"));
el("themeDarkBtn").addEventListener("click", () => setTheme("dark"));
el("autoExecuteBtn").addEventListener("click", () => {
  state.autoExecute = !state.autoExecute;
  localStorage.setItem("codexBridge.autoExecute", state.autoExecute ? "1" : "0");
  updateComposerState();
});
el("attachBtn").addEventListener("click", () => el("prompt").focus());
el("chatView").addEventListener("click", (event) => {
  if (event.target === el("chatView")) {
    el("chatView").classList.remove("nav-open");
  }
});
el("stopBtn").addEventListener("click", () => {
  if (state.ws && state.activeSession) {
    if (state.activeRun) {
      state.activeRun = { ...state.activeRun, status: "canceling" };
      updateComposerState();
      setStatusKey("canceling");
    }
    try {
      state.ws.send(JSON.stringify({ type: "cancel", sid: state.activeSession.id }));
    } catch {
      appendMessage("system", t("wsDisconnected"));
      ensureChatConnection();
    }
  }
});
el("logoutBtn").addEventListener("click", () => logout().catch((err) => showError(err)));
el("settingsLogoutBtn").addEventListener("click", () => logout().catch((err) => showError(err)));

window.addEventListener("focus", ensureChatConnection);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) ensureChatConnection();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    closeSettings();
    el("chatView").classList.remove("nav-open");
  }
});

registerServiceWorker();
boot();
