import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Terminal, User, Lock, Globe, ChevronDown, ArrowDownToLine,
  PanelLeftClose, PanelLeft, Plus, MessageSquare,
  Settings, LogOut, Search,
  ImagePlus, Send, Square, AlertCircle,
  RefreshCw, Check, Clipboard,
  Menu, X, Server, Activity, Command,
  Trash2, Edit2, GitBranch, Swords, UsersRound, ArrowLeft,
  FileUp, FileText, FolderInput, ShieldQuestion, Wrench, Share2
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
  isAdmin?: boolean;
};

type Agent = {
  id: string;
  userId?: string;
  name: string;
  machineId: string;
  hostname: string;
  instance?: string;
  workingDirs?: string[];
  lastSeenAt: number;
  online: boolean;
  capabilities?: BridgeCapabilities;
};

type BridgeCapabilities = {
  runner?: string;
  sandbox?: string;
  approvalPolicy?: string;
  chat?: Record<string, BridgeCLICapability | undefined>;
  orchestration?: Record<string, BridgeCLICapability | undefined>;
  metadata?: Record<string, string | undefined>;
};

type BridgeCLICapability = {
  available?: boolean;
  execution?: string;
  browserApproval?: boolean;
  approvalMode?: string;
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

type OrchestrationFile = {
  name: string;
  mimeType: string;
  size: number;
};

type OrchestrationRun = {
  id: string;
  agentId: string;
  title: string;
  mode: 'collaboration' | 'debate';
  prompt: string;
  cwd?: string;
  maxTurns: number;
  status: string;
  error?: string;
  files?: OrchestrationFile[];
  createdAt: number;
  updatedAt: number;
  finishedAt?: number;
};

type OrchestrationEvent = {
  id?: string;
  runId: string;
  seq?: number;
  kind: string;
  role?: string;
  cli?: string;
  turnId?: string;
  content?: string;
  status?: string;
  error?: string;
  data?: Record<string, any>;
  createdAt?: number;
};

type OrchestrationTurnInfo = {
  ordinal?: number;
  total?: number;
  verifier?: boolean;
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

type ApprovalRequest = {
  requestId: string;
  kind: string;
  command?: string;
  cwd?: string;
  reason?: string;
  runId?: string;
  turnId?: string;
  promptId?: string;
};

type ApprovalStatus = 'pending' | 'accepted' | 'declined' | 'canceled';

type ChatItem =
  | { id: string; type: 'message'; role: 'user' | 'assistant' | 'system'; content: string; createdAt?: number }
  | { id: string; type: 'tool'; tool: ToolEvent }
  | { id: string; type: 'approval'; approval: ApprovalRequest; status?: ApprovalStatus };

type ApprovalItemState = {
  id: string;
  approval: ApprovalRequest;
  status?: ApprovalStatus;
};

type OrchestrationVisibleEvent =
  | {
      type: 'message';
      key: string;
      runId: string;
      kind: string;
      role?: string;
      cli?: string;
      turnId?: string;
      content: string;
      status?: string;
      error?: string;
      createdAt?: number;
      files?: OrchestrationFile[];
      commands: OrchestrationEvent[];
    }
  | {
      type: 'command';
      key: string;
      runId: string;
      kind: string;
      role?: string;
      cli?: string;
      turnId?: string;
      content: string;
      status?: string;
      error?: string;
      createdAt?: number;
      command: OrchestrationEvent;
    }
  | {
      type: 'status';
      key: string;
      runId: string;
      kind: string;
      role?: string;
      cli?: string;
      turnId?: string;
      content: string;
      status?: string;
      error?: string;
      createdAt?: number;
    };

type Envelope = {
  type: string;
  sid?: string;
  payload?: any;
};

type BridgeTokenResponse = {
  token: string;
  expiresAt: number;
  label: string;
  hubUrl: string;
  downloadUrl: string;
  permissionProfile: PermissionProfileId;
  permissionProfiles?: BridgePermissionProfile[];
  setupCommand: string;
  installCommand: string;
  connectCommand: string;
  sudoSetupCommand?: string;
  sudoInstallCommand?: string;
  sudoConnectCommand?: string;
  sudoCommands?: string[];
  commands: string[];
  agentId?: string;
  machineId?: string;
};

type PermissionProfileId = 'review-required' | 'auto-execute';

type BridgePermissionProfile = {
  id: PermissionProfileId;
  setupCommand: string;
  connectCommand: string;
  sudoSetupCommand?: string;
  sudoConnectCommand?: string;
};

type ShareInfo = {
  id: string;
  kind: 'chat' | 'orchestration';
  title?: string;
  url?: string;
  createdAt: number;
  updatedAt: number;
};

type PublicSession = {
  id: string;
  title?: string;
  createdAt: number;
  updatedAt: number;
};

type PublicMessage = {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  createdAt: number;
};

type PublicOrchestrationRun = {
  id: string;
  title: string;
  mode: 'collaboration' | 'debate';
  prompt: string;
  cwd?: string;
  maxTurns: number;
  status: string;
  error?: string;
  files?: OrchestrationFile[];
  createdAt: number;
  updatedAt: number;
  finishedAt?: number;
};

type PublicSharePayload = {
  share: ShareInfo;
  session?: PublicSession;
  messages?: PublicMessage[];
  run?: PublicOrchestrationRun;
  events?: OrchestrationEvent[];
};

type ImageAttachment = {
  id: string;
  name: string;
  mimeType: string;
  size: number;
  data: string;
  previewUrl: string;
};

type UploadAttachment = {
  id: string;
  name: string;
  mimeType: string;
  size: number;
  data: string;
};

type Language = 'en' | 'zh';

const uiText = {
  en: {
    secureConnection: 'Secure connection to your workspace',
    username: 'Username',
    password: 'Password',
    connectToWorkspace: 'Connect to Workspace',
    connectionFailed: 'Connection failed.',
    disconnected: 'Disconnected',
    connected: 'Connected',
    connecting: 'Connecting',
    connectionError: 'Connection error',
    ready: 'Ready',
    error: 'Error',
    idle: 'idle',
    failedLoadOrchestration: 'Failed to load orchestration state',
    failedStartOrchestration: 'Failed to start orchestration',
    orchestrationCapabilityUnavailable: 'Selected CLI endpoint cannot run review-required browser approval for both orchestration CLIs.',
    jumpToLatestMessage: 'Jump to latest message',
    jumpToBottom: 'Jump to bottom',
    uploadImages: 'Upload images',
    commandEvent: 'command event',
    commandDetails: 'Command details',
    commands: 'Commands',
    runningFor: 'Running',
    duration: 'Duration',
    noVisibleAnswer: 'No user-visible answer was returned for this turn.',
    agent: 'agent',
    reviewCurrentRepository: 'Review the current repository, implement the requested change, and verify with tests.',
    english: 'English',
    chinese: 'Chinese',
    language: 'Language',
    newSession: 'New Session',
    newRun: 'New Run',
    conversationSnapshot: 'Conversation Snapshot',
    shareConversation: 'Share conversation',
    shareRun: 'Share run',
    shareRunCopied: 'Share link copied',
    publicShare: 'Public share',
    signInToContinue: 'Sign in to continue',
    failedCreateShare: 'Failed to create share link',
    failedLoadShare: 'Failed to load shared conversation',
    readOnlySnapshot: 'Read-only snapshot',
    orchestration: 'Orchestration',
    codexBridge: 'Codex Bridge',
    searchSessions: 'Search sessions...',
    noSessions: 'No sessions',
    settings: 'Settings',
    agentOnline: 'Agent Online',
    agentOffline: 'Agent Offline',
    orchestrate: 'Orchestrate',
    runner: 'Runner',
    thread: 'Thread',
    status: 'Status',
    howCanIHelp: 'How can I help you today?',
    codexCapability: 'I can execute code, read files, run terminal commands, and help you build your project.',
    readProjectFiles: 'Read project files',
    exploreCurrentDirectory: 'Explore current directory',
    runTestSuite: 'Run test suite',
    executeConfiguredTests: 'Execute configured tests',
    askCodex: 'Ask Codex to read files, run commands, or write code...',
    stopping: 'Stopping',
    stop: 'Stop',
    send: 'Send',
    verifyNotice: 'Codex Bridge may produce inaccurate results. Verify important changes.',
    noBridgeConnected: 'No bridge connected',
    sessionNameRequired: 'Session name is required',
    failedRenameSession: 'Failed to rename session',
    deleteSessionConfirm: 'Delete this session? This cannot be undone.',
    analyzeUploadedImage: 'Please analyze the uploaded image.',
    today: 'Today',
    yesterday: 'Yesterday',
    previous7Days: 'Previous 7 Days',
    older: 'Older',
    user: 'You',
    system: 'System',
    copyMessage: 'Copy message',
    copied: 'Copied',
    copy: 'Copy',
    copyOutput: 'Copy output',
    run: 'Run',
    bash: 'Bash',
    running: 'running',
    account: 'Account',
    localAdministrator: 'Local Administrator',
    logout: 'Logout',
    appearance: 'Appearance',
    theme: 'Theme',
    light: 'Light',
    dark: 'Dark',
    agentsRuntime: 'Agents & Runtime',
    noAgentsEnrolled: 'No agents enrolled',
    cliEndpoint: 'CLI Endpoint',
    selectEndpoint: 'Select endpoint',
    addCliEndpoint: 'Add CLI Endpoint',
    deleteCliEndpoint: 'Delete CLI Endpoint',
    deleteCliEndpointConfirm: 'Delete this CLI endpoint? Running background bridge processes for this endpoint should also be stopped locally.',
    endpointLabel: 'Endpoint label',
    permissionProfile: 'Permission profile',
    reviewRequired: 'Review required',
    reviewRequiredDescription: 'Codex chat, Codex orchestration, and Claude Code orchestration ask in the browser before untrusted commands.',
    autoExecute: 'Auto execute',
    autoExecuteDescription: 'Trusted mode. Codex and Claude run without local permission prompts and can modify files directly.',
    manualOrchestration: 'Claude Code + Codex CLI',
    selectedProfileCommand: 'Selected profile',
    alternateProfileCommand: 'Alternative profile',
    approvalRequired: 'Approval required',
    approve: 'Approve',
    deny: 'Deny',
    approved: 'Approved',
    denied: 'Denied',
    approvalCanceled: 'Canceled',
    generating: 'Generating',
    setupCommand: 'Install and connect',
    installCommand: 'Install command',
    normalUserCommands: 'No sudo',
    sudoRootCommands: 'With sudo',
    linkCommand: 'Link command',
    connectCommand: 'Connect',
    repairConnection: 'Repair connection',
    repairConnectionCommand: 'Repair command',
    generateRepairCommand: 'Generate repair command',
    repairCommandHint: 'Run this on the endpoint machine to update Bridge and reconnect this same CLI endpoint.',
    failedCreateRepairToken: 'Failed to create repair command',
    agentDetails: 'Details',
    machineId: 'Machine ID',
    noCapabilitiesReported: 'No capabilities reported. Reconnect with the latest command.',
    enrollToken: 'Token',
    expiresIn24h: 'Expires in 24h',
    failedCreateBridgeToken: 'Failed to create CLI token',
    failedDeleteAgent: 'Failed to delete CLI endpoint',
    online: 'online',
    offline: 'offline',
    cancel: 'Cancel',
    savePreferences: 'Save Preferences',
    save: 'Save',
    renameSession: 'Rename Session',
    sessionName: 'Session name',
    cliOrchestration: 'CLI Orchestration',
    workers: 'Workers',
    capabilityMatrix: 'Capabilities',
    browserApproval: 'Browser approval',
    notAvailable: 'Not available',
    available: 'Available',
    codexOrchestrationApprovalMissing: 'Codex orchestration browser approval is unavailable on this endpoint.',
    claudeOrchestrationApprovalMissing: 'Claude orchestration browser approval is unavailable on this endpoint.',
    stream: 'Event stream',
    currentTurn: 'Current turn',
    lastTurn: 'Last turn',
    turnPrefix: 'Turn',
    turnSuffix: '',
    verifierTurn: 'Final check',
    coordinateClaudeCodex: 'Coordinate Claude Code and Codex',
    startCollaborationHint: 'Start a collaboration or debate run from the panel on the right.',
    mode: 'Mode',
    collaborate: 'Collaborate',
    debate: 'Debate',
    task: 'Task',
    taskPlaceholder: 'Describe the proof, code change, or review task...',
    workingDirectory: 'Working directory',
    bridgeDefaultCwd: 'Bridge default cwd',
    noWorkingDirs: 'No workspace paths advertised',
    removeFile: 'Remove file',
    turns: 'Turns',
    files: 'Files',
    attachedFiles: 'Attached files',
    currentRunFiles: 'Run files',
    add: 'Add',
    uploadProofFiles: 'Upload Coq, Lean, Isabelle, source, logs, or screenshots.',
    stopRun: 'Stop Run',
    start: 'Start',
    noOrchestrationRuns: 'No orchestration runs',
    continueRun: 'Continue',
  },
  zh: {
    secureConnection: '安全连接到你的工作区',
    username: '用户名',
    password: '密码',
    connectToWorkspace: '连接工作区',
    connectionFailed: '连接失败。',
    disconnected: '已断开',
    connected: '已连接',
    connecting: '连接中',
    connectionError: '连接错误',
    ready: '就绪',
    error: '错误',
    idle: '空闲',
    failedLoadOrchestration: '加载编排状态失败',
    failedStartOrchestration: '启动编排失败',
    orchestrationCapabilityUnavailable: '所选 CLI 端不能同时为两个编排 CLI 提供网页审批。',
    jumpToLatestMessage: '跳转到最新消息',
    jumpToBottom: '跳到底部',
    uploadImages: '上传图片',
    commandEvent: '命令事件',
    commandDetails: '命令详情',
    commands: '命令',
    runningFor: '已运行',
    duration: '耗时',
    noVisibleAnswer: '这一轮没有返回面向用户的可读回答。',
    agent: 'agent',
    reviewCurrentRepository: '审查当前仓库，完成请求的改动，并用测试验证。',
    english: 'English',
    chinese: '中文',
    language: '语言',
    newSession: '新会话',
    newRun: '新运行',
    conversationSnapshot: '对话快照',
    shareConversation: '分享对话',
    shareRun: '分享运行',
    shareRunCopied: '分享链接已复制',
    publicShare: '公开分享',
    signInToContinue: '登录后继续',
    failedCreateShare: '创建分享链接失败',
    failedLoadShare: '加载分享对话失败',
    readOnlySnapshot: '只读快照',
    orchestration: '编排',
    codexBridge: 'Codex Bridge',
    searchSessions: '搜索会话...',
    noSessions: '暂无会话',
    settings: '设置',
    agentOnline: 'Agent 在线',
    agentOffline: 'Agent 离线',
    orchestrate: '编排',
    runner: '运行器',
    thread: '线程',
    status: '状态',
    howCanIHelp: '今天需要我做什么？',
    codexCapability: '我可以执行代码、读取文件、运行终端命令，并协助构建项目。',
    readProjectFiles: '读取项目文件',
    exploreCurrentDirectory: '探索当前目录',
    runTestSuite: '运行测试套件',
    executeConfiguredTests: '执行已配置的测试',
    askCodex: '让 Codex 读取文件、运行命令或编写代码...',
    stopping: '正在停止',
    stop: '停止',
    send: '发送',
    verifyNotice: 'Codex Bridge 可能产生不准确结果，请核验重要变更。',
    noBridgeConnected: '没有已连接的 bridge',
    sessionNameRequired: '会话名称不能为空',
    failedRenameSession: '重命名会话失败',
    deleteSessionConfirm: '确定删除这个会话？此操作无法撤销。',
    analyzeUploadedImage: '请分析上传的图片。',
    today: '今天',
    yesterday: '昨天',
    previous7Days: '过去 7 天',
    older: '更早',
    user: '你',
    system: '系统',
    copyMessage: '复制消息',
    copied: '已复制',
    copy: '复制',
    copyOutput: '复制输出',
    run: '运行',
    bash: 'Bash',
    running: '运行中',
    account: '账户',
    localAdministrator: '本地管理员',
    logout: '退出登录',
    appearance: '外观',
    theme: '主题',
    light: '浅色',
    dark: '深色',
    agentsRuntime: 'Agent 与运行时',
    noAgentsEnrolled: '暂无已注册 Agent',
    cliEndpoint: 'CLI 端',
    selectEndpoint: '选择 CLI 端',
    addCliEndpoint: '添加 CLI 端',
    deleteCliEndpoint: '删除 CLI 端',
    deleteCliEndpointConfirm: '确定删除这个 CLI 端？这个端对应的本地后台 bridge 进程也应该在本机停止。',
    endpointLabel: '端名称',
    permissionProfile: '权限策略',
    reviewRequired: '需要确认',
    reviewRequiredDescription: 'Codex 聊天、Codex 编排和 Claude Code 编排都会在网页端确认不可信命令。',
    autoExecute: '无需授权',
    autoExecuteDescription: '可信模式。Codex 和 Claude 不再弹本机权限确认，可直接修改文件和执行命令。',
    manualOrchestration: 'Claude Code + Codex CLI',
    selectedProfileCommand: '当前策略',
    alternateProfileCommand: '备用策略',
    approvalRequired: '需要确认',
    approve: '允许',
    deny: '拒绝',
    approved: '已允许',
    denied: '已拒绝',
    approvalCanceled: '已取消',
    generating: '生成中',
    setupCommand: '安装并连接',
    installCommand: '安装命令',
    normalUserCommands: '无需 sudo',
    sudoRootCommands: '使用 sudo',
    linkCommand: '链接命令',
    connectCommand: '连接',
    repairConnection: '修复连接',
    repairConnectionCommand: '修复命令',
    generateRepairCommand: '生成修复命令',
    repairCommandHint: '在这个端点所在机器上运行，用最新 Bridge 重连同一个 CLI 端。',
    failedCreateRepairToken: '创建修复命令失败',
    agentDetails: '详情',
    machineId: 'Machine ID',
    noCapabilitiesReported: '尚未上报能力。请用最新命令重新连接。',
    enrollToken: 'Token',
    expiresIn24h: '24 小时内有效',
    failedCreateBridgeToken: '创建 CLI token 失败',
    failedDeleteAgent: '删除 CLI 端失败',
    online: '在线',
    offline: '离线',
    cancel: '取消',
    savePreferences: '保存偏好',
    save: '保存',
    renameSession: '重命名会话',
    sessionName: '会话名称',
    cliOrchestration: 'CLI 编排',
    workers: '工作器',
    capabilityMatrix: '能力矩阵',
    browserApproval: '网页审批',
    notAvailable: '不可用',
    available: '可用',
    codexOrchestrationApprovalMissing: '该端的 Codex 编排网页审批不可用。',
    claudeOrchestrationApprovalMissing: '该端的 Claude 编排网页审批不可用。',
    stream: '事件流',
    currentTurn: '当前轮次',
    lastTurn: '最后轮次',
    turnPrefix: '第',
    turnSuffix: '轮',
    verifierTurn: '最终检查',
    coordinateClaudeCodex: '协同 Claude Code 与 Codex',
    startCollaborationHint: '从右侧面板启动协作或辩论运行。',
    mode: '模式',
    collaborate: '协作',
    debate: '辩论',
    task: '任务',
    taskPlaceholder: '描述证明、代码改动或审查任务...',
    workingDirectory: '工作目录',
    bridgeDefaultCwd: 'Bridge 默认工作目录',
    noWorkingDirs: '尚未上报可选工作区路径',
    removeFile: '移除文件',
    turns: '轮次',
    files: '文件',
    attachedFiles: '已上传文件',
    currentRunFiles: '本轮文件',
    add: '添加',
    uploadProofFiles: '上传 Coq、Lean、Isabelle、源码、日志或截图。',
    stopRun: '停止运行',
    start: '开始',
    noOrchestrationRuns: '暂无编排运行',
    continueRun: '继续',
  },
};

type UIText = typeof uiText.en;

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

function displaySessionTitle(session: Session | null | undefined, t: UIText = uiText.en) {
  if (!session?.title || session.title === 'New chat') return t.newSession;
  return session.title;
}

function titleFromPrompt(prompt: string, t: UIText = uiText.en) {
  const compact = prompt.replace(/\s+/g, ' ').trim();
  if (!compact) return t.newSession;
  return compact.length > 48 ? `${compact.slice(0, 48)}...` : compact;
}

function formatTime(timestamp?: number) {
  if (!timestamp) return '';
  const date = new Date(timestamp * 1000);
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function formatDuration(ms?: number) {
  if (!Number.isFinite(ms || NaN) || !ms || ms < 0) return '';
  const totalSeconds = Math.max(1, Math.floor(ms / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}h ${minutes}m ${seconds}s`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

function sessionDateLabel(timestamp: number, t: UIText = uiText.en) {
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

function activeOrchestrationStatus(status?: string) {
  return status === 'queued' || status === 'running' || status === 'canceling';
}

function terminalOrchestrationStatus(status?: string) {
  return status === 'completed' || status === 'failed' || status === 'canceled';
}

const activeOrchestrationRunStorageKey = 'codexBridge.activeOrchestrationRunId';
const activeOrchestrationRunByAgentStorageKey = 'codexBridge.activeOrchestrationRunByAgent';

function readActiveOrchestrationRunByAgent(): Record<string, string> {
  try {
    const raw = localStorage.getItem(activeOrchestrationRunByAgentStorageKey);
    const parsed = raw ? JSON.parse(raw) : {};
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, string> : {};
  } catch {
    return {};
  }
}

function rememberActiveOrchestrationRunForAgent(agentId: string, runId: string) {
  if (!agentId || !runId) return;
  const current = readActiveOrchestrationRunByAgent();
  current[agentId] = runId;
  localStorage.setItem(activeOrchestrationRunByAgentStorageKey, JSON.stringify(current));
}

function forgetActiveOrchestrationRunForAgent(agentId: string, runId?: string) {
  if (!agentId) return;
  const current = readActiveOrchestrationRunByAgent();
  if (!runId || current[agentId] === runId) {
    delete current[agentId];
    localStorage.setItem(activeOrchestrationRunByAgentStorageKey, JSON.stringify(current));
  }
}

function canCancelOrchestrationStatus(status?: string) {
  return status === 'queued' || status === 'running';
}

function orchestrationRunStatusFromEvent(event: OrchestrationEvent) {
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

function orchestrationEventKey(event: OrchestrationEvent, index = 0) {
  if (event.id) return `id:${event.id}`;
  if (event.seq && event.runId) return `seq:${event.runId}:${event.seq}`;
  return `fallback:${event.runId}:${event.kind}:${event.turnId || ''}:${event.role || ''}:${event.cli || ''}:${event.createdAt || ''}:${index}`;
}

function compareOrchestrationEvents(a: OrchestrationEvent, b: OrchestrationEvent) {
  if (a.runId !== b.runId) return a.runId.localeCompare(b.runId);
  if (a.seq && b.seq && a.seq !== b.seq) return a.seq - b.seq;
  if (a.createdAt && b.createdAt && a.createdAt !== b.createdAt) return a.createdAt - b.createdAt;
  return orchestrationEventKey(a).localeCompare(orchestrationEventKey(b));
}

function mergeOrchestrationEvents(current: OrchestrationEvent[], incoming: OrchestrationEvent[]) {
  const merged = new Map<string, OrchestrationEvent>();
  current.forEach((event, index) => merged.set(orchestrationEventKey(event, index), event));
  incoming.forEach((event, index) => {
    const key = orchestrationEventKey(event, current.length + index);
    const previous = merged.get(key);
    merged.set(key, previous ? { ...previous, ...event, data: event.data || previous.data } : event);
  });
  return Array.from(merged.values()).sort(compareOrchestrationEvents);
}

function upsertOrchestrationRun(current: OrchestrationRun[], next: OrchestrationRun) {
  const found = current.some((run) => run.id === next.id);
  const runs = found ? current.map((run) => run.id === next.id ? { ...run, ...next } : run) : [next, ...current];
  return runs.slice().sort((a, b) => (b.updatedAt || b.createdAt || 0) - (a.updatedAt || a.createdAt || 0));
}

function upsertApprovalItem(current: ApprovalItemState[], approval: ApprovalRequest): ApprovalItemState[] {
  const semanticKey = approvalSemanticKey(approval);
  const next: ApprovalItemState = { id: approval.requestId, approval, status: 'pending' };
  let replaced = false;
  const updated = current.map((item) => {
    if (item.approval.requestId === approval.requestId) {
      replaced = true;
      return { ...item, approval };
    }
    if (approvalSemanticKey(item.approval) === semanticKey) {
      replaced = true;
      return next;
    }
    return item;
  });
  return replaced ? updated : [...current, next];
}

function updateApprovalItemStatus(current: ApprovalItemState[], requestId: string, status: ApprovalStatus): ApprovalItemState[] {
  return current.map((item) => item.approval.requestId === requestId ? { ...item, status } : item);
}

function approvalSemanticKey(approval: ApprovalRequest) {
  const command = stringsTrim(approval.command).replace(/\s+/g, ' ');
  const cwd = stringsTrim(approval.cwd);
  const reason = stringsTrim(approval.reason).replace(/\s+/g, ' ');
  return [approval.runId || '', approval.turnId || approval.promptId || '', approval.kind || '', command, cwd, reason].join('\u001f');
}

function approvalStatusFromDecision(decision: 'accept' | 'decline' | 'cancel'): ApprovalStatus {
  return decision === 'accept' ? 'accepted' : decision === 'decline' ? 'declined' : 'canceled';
}

function orchestrationToolID(event: OrchestrationEvent) {
  return typeof event.data?.id === 'string' ? event.data.id : '';
}

function mergeOrchestrationToolEvents(events: OrchestrationEvent[]): OrchestrationEvent[] {
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
    const mergedData = mergeOrchestrationToolData(previous.data, event.data);
    if (typeof mergedData.startedAt !== 'number' && previous.createdAt) {
      mergedData.startedAt = previous.createdAt;
    }
    if (typeof mergedData.completedAt !== 'number' && event.createdAt && event.kind === 'command.end') {
      mergedData.completedAt = event.createdAt;
    }
    if (
      typeof mergedData.durationMs !== 'number' &&
      typeof mergedData.startedAt === 'number' &&
      typeof mergedData.completedAt === 'number'
    ) {
      mergedData.durationMs = Math.max(0, (mergedData.completedAt - mergedData.startedAt) * 1000);
    }
    merged[index] = {
      ...previous,
      ...event,
      data: mergedData,
      content: event.content || previous.content,
      error: event.error || previous.error,
      createdAt: event.createdAt || previous.createdAt,
      seq: event.seq || previous.seq,
    };
  });
  return merged;
}

function mergeOrchestrationToolData(previous?: Record<string, any>, next?: Record<string, any>) {
  const data = {
    ...(previous || {}),
    ...(next || {}),
  };
  for (const field of ['command', 'input', 'name']) {
    if (typeof data[field] === 'string' && !data[field].trim() && typeof previous?.[field] === 'string' && previous[field].trim()) {
      data[field] = previous[field];
    }
  }
  for (const field of ['startedAt', 'completedAt', 'durationMs']) {
    if (typeof data[field] !== 'number' && typeof previous?.[field] === 'number') {
      data[field] = previous[field];
    }
  }
  return data;
}

function orchestrationTurnKey(event: OrchestrationEvent) {
  const contentKind = typeof event.data?.contentKind === 'string' ? event.data.contentKind : '';
  if (contentKind === 'agent_text' || contentKind === 'agent_reply') {
    const eventId = typeof event.data?.eventId === 'string' ? event.data.eventId : '';
    const replyId = typeof event.data?.replyId === 'string' ? event.data.replyId : '';
    const jobId = typeof event.data?.jobId === 'string' ? event.data.jobId : '';
    const contentId = eventId || replyId || `${jobId}:${event.seq || event.createdAt || ''}`;
    if (contentId) return `${event.runId}:${event.turnId || ''}:${event.role || ''}:${event.cli || ''}:${contentId}`;
  }
  return `${event.runId}:${event.turnId || ''}:${event.role || ''}:${event.cli || ''}`;
}

function parseOrchestrationTurnInfo(turnId?: string): OrchestrationTurnInfo {
  const value = String(turnId || '');
  if (!value) return {};
  if (/(?:^|-)verifier$/.test(value)) return { verifier: true };
  const match = value.match(/-(\d{2,})$/);
  if (!match) return {};
  const ordinal = Number(match[1]);
  return Number.isFinite(ordinal) && ordinal > 0 ? { ordinal } : {};
}

function orchestrationTurnInfoFromEvents(events: OrchestrationEvent[], runId: string, maxTurns?: number, includeTotal = true): OrchestrationTurnInfo {
  let latest: OrchestrationEvent | null = null;
  for (const event of events) {
    if (event.runId !== runId || !event.turnId) continue;
    if (!latest || compareOrchestrationEvents(latest, event) <= 0) latest = event;
  }
  if (!latest) return {};
  const info = parseOrchestrationTurnInfo(latest.turnId);
  if (includeTotal && typeof info.ordinal === 'number' && maxTurns) {
    return { ...info, total: maxTurns };
  }
  return info;
}

function orchestrationTurnLabel(info: OrchestrationTurnInfo, t: UIText) {
  if (info.verifier) return t.verifierTurn;
  if (typeof info.ordinal !== 'number') return '';
  const suffix = info.total ? `/${info.total}` : '';
  if (t.turnPrefix === '第') return `${t.turnPrefix}${info.ordinal}${suffix}${t.turnSuffix}`;
  return `${t.turnPrefix} ${info.ordinal}${suffix}`;
}

function visibleOrchestrationEvents(events: OrchestrationEvent[], runId: string, run?: OrchestrationRun | PublicOrchestrationRun | null, t?: UIText): OrchestrationVisibleEvent[] {
  const terminalRun = terminalOrchestrationStatus(run?.status);
  const ordered = mergeOrchestrationDeltaEvents(
    mergeOrchestrationToolEvents(events.filter((event) => event.runId === runId).slice().sort(compareOrchestrationEvents))
      .filter((event) => !isEmptyPagesReadFailureEvent(event))
      .map((event) => terminalRun ? finalizeTerminalCommandEvent(event, run?.status) : event)
  );
  const contentfulTurnEnds = new Set(
    ordered
      .filter((event) => event.kind === 'turn.end' && stringsTrim(event.content))
      .map(orchestrationTurnKey)
  );
  const visible: OrchestrationVisibleEvent[] = [];
  let segmentStartIndex = 0;
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
        files: orchestrationEventFiles(event),
        commands: [],
      });
      return;
    }

    if (event.kind === 'turn.delta') {
      if (contentfulTurnEnds.has(orchestrationTurnKey(event))) return;
      const content = cleanOrchestrationDisplayContent(event.content);
      if (!content) return;
      if (isRawCCBObserverDump(content)) return;
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
        command: event,
      });
      if (event.status === 'error' || event.error) {
        visible.push(statusVisibleEvent(event, index));
      }
      return;
    }

    if (event.kind === 'turn.end') {
      const content = cleanOrchestrationDisplayContent(event.content);
      if (content && !isRawCCBObserverDump(content)) {
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
          commands: [],
        });
        return;
      }
      if (event.error) {
        visible.push(statusVisibleEvent(event, index));
      }
      return;
    }

    if (shouldShowOrchestrationStatus(event)) {
      visible.push(statusVisibleEvent(event, index));
    }

    if (event.kind === 'run.end') {
      const fallback = finalOrchestrationConclusionFallback(
        ordered.slice(segmentStartIndex, index + 1),
        visible.slice(segmentVisibleStart),
        runId,
        run,
        t
      );
      if (fallback) visible.push(fallback);
      segmentStartIndex = index + 1;
      segmentVisibleStart = visible.length;
    }
  });
  return visible;
}

function finalizeTerminalCommandEvent(event: OrchestrationEvent, runStatus?: string): OrchestrationEvent {
  if (!event.kind.startsWith('command.')) return event;
  const data = event.data || {};
  const status = typeof data.status === 'string' ? data.status : event.status || '';
  const active = event.kind === 'command.start' || status === 'running' || status === 'in_progress';
  if (!active || typeof data.completedAt === 'number') return event;
  const terminalStatus = runStatus === 'canceled' ? 'canceled' : 'interrupted';
  return {
    ...event,
    kind: 'command.end',
    status: terminalStatus,
    data: {
      ...data,
      status: terminalStatus,
      completedAt: event.createdAt || Math.floor(Date.now() / 1000),
    },
  };
}

function finalOrchestrationConclusionFallback(
  segmentEvents: OrchestrationEvent[],
  segmentVisible: OrchestrationVisibleEvent[],
  runId: string,
  run?: OrchestrationRun | PublicOrchestrationRun | null,
  t: UIText = uiText.en
): OrchestrationVisibleEvent | null {
  const runEvents = segmentEvents.filter((event) => event.runId === runId);
  if (!runEvents.length) return null;
  const last = runEvents[runEvents.length - 1];
  const completed = last.kind === 'run.end' && last.status === 'completed';
  if (!completed) return null;
  if (hasFreshFinalConclusion(runEvents)) return null;

  const failedCommands = runEvents.filter((event) => event.kind.startsWith('command.') && commandEventFailed(event)).length;
  const erroredTurns = runEvents.filter((event) => event.kind === 'turn.end' && (event.status === 'error' || Boolean(event.error))).length;
  const completedCommands = runEvents.filter((event) => event.kind === 'command.end' && !commandEventFailed(event)).length;
  const segmentFiles = orchestrationRunFilesFromEvents(runEvents, runId);
  const fileNames = mergeOrchestrationFiles(segmentFiles.length ? segmentFiles : run?.files).map((file) => file.name).filter(Boolean);
  const segmentPrompt = runEvents.find((event) => event.kind === 'user.message')?.content || run?.prompt;
  const zh = t === uiText.zh || t.currentTurn === uiText.zh.currentTurn || textLooksChinese(segmentPrompt);
  const issueCount = failedCommands + erroredTurns;
  const ccbSummary = ccbOrchestrationSummary(runEvents);
  const acceptanceIssue = unresolvedAcceptanceSummary(runEvents);
  const content = zh
    ? [
        acceptanceIssue
          ? '最终结论：本次编排已经结束，但当前记录显示用户的核心验收条件仍未满足，因此不能视为真正成功。'
          : '最终结论：本次编排已经结束，但最后一轮没有返回可直接阅读的总结，因此这里根据已记录事件生成兜底摘要。',
        ccbSummary ? `CCB 状态：${ccbSummary}` : '',
        `进展：${completedCommands > 0 ? `记录到 ${completedCommands} 个完成的命令事件。` : '没有可提炼的完成命令摘要，可展开命令详情审计原始事件。'}`,
        fileNames.length ? `相关文件：${fileNames.map((name) => `\`${name}\``).join('、')}。` : '',
        acceptanceIssue ? `未满足验收：${acceptanceIssue}` : '',
        issueCount > 0 ? `剩余风险：记录到 ${issueCount} 个失败命令或错误轮次；请展开命令详情查看原始输出。` : '剩余风险：未记录新的失败命令；请按需展开命令详情审计原始输出。',
      ].filter(Boolean).join('\n\n')
    : [
        acceptanceIssue
          ? 'Final conclusion: this orchestration ended, but the recorded state shows the user acceptance criterion is still unmet, so it must not be treated as a real success.'
          : 'Final conclusion: this orchestration completed, but the last turn did not return a directly readable summary, so this fallback was generated from recorded events.',
        ccbSummary ? `CCB state: ${ccbSummary}` : '',
        `Progress: ${completedCommands > 0 ? `${completedCommands} completed command event(s) were recorded.` : 'no concise completed command summary was available; expand command details to audit raw events.'}`,
        fileNames.length ? `Files: ${fileNames.map((name) => `\`${name}\``).join(', ')}.` : '',
        acceptanceIssue ? `Unmet acceptance: ${acceptanceIssue}` : '',
        issueCount > 0 ? `Remaining risk: ${issueCount} failed command or error turn event(s) were recorded; expand command details for raw output.` : 'Remaining risk: no new failed command was recorded; expand command details to audit raw output if needed.',
      ].filter(Boolean).join('\n\n');
  const lastTurn = findLastOrchestrationTurnEvent(runEvents);

  return {
    type: 'message',
    key: `fallback-final-summary:${runId}:${last.seq || last.createdAt || segmentVisible.length}`,
    runId,
    kind: 'run.end',
    role: 'summary',
    cli: '',
    turnId: lastTurn?.turnId,
    content,
    status: 'completed',
    createdAt: last.createdAt,
    commands: [],
  };
}

function hasFreshFinalConclusion(runEvents: OrchestrationEvent[]) {
  let latestCommandIndex = -1;
  let latestTurnIndex = -1;
  let latestTurn: OrchestrationEvent | null = null;
  runEvents.forEach((event, index) => {
    if (event.kind.startsWith('command.')) latestCommandIndex = index;
    if (event.kind === 'turn.start' || event.kind === 'turn.end') {
      latestTurnIndex = index;
      latestTurn = event;
    }
  });

  return runEvents.some((event, index) => {
    if (event.kind !== 'turn.delta' && event.kind !== 'turn.end') return false;
    if (!isReadableFinalConclusion(event.content)) return false;
    if (index < latestCommandIndex) return false;
    if (latestTurn && index < latestTurnIndex && orchestrationTurnKey(event) !== orchestrationTurnKey(latestTurn)) return false;
    return true;
  });
}

function findLastOrchestrationTurnEvent(runEvents: OrchestrationEvent[]) {
  for (let index = runEvents.length - 1; index >= 0; index -= 1) {
    const event = runEvents[index];
    if (event.kind === 'turn.start' || event.kind === 'turn.end' || event.kind === 'turn.delta') return event;
  }
  return null;
}

function ccbOrchestrationSummary(runEvents: OrchestrationEvent[]) {
  const agentState = new Map<string, Set<string>>();
  const callbackJobs = new Set<string>();
  runEvents.forEach((event) => {
    const data = event.data || {};
    let agent = stringsTrim(String(data.agent || data.target || event.role || event.cli || ''));
    if (!agent && event.cli === 'ccb') agent = 'ccb';
    const eventType = stringsTrim(String(data.eventType || ''));
    const content = event.content || '';
    if (!eventType && isRawCCBObserverDump(content)) {
      rawCCBObserverEventTypes(content).forEach((raw) => {
        if (!agent || agent === 'ccb') agent = raw.agent || agent || 'ccb';
        recordCCBAgentState(agentState, raw.agent || agent || 'ccb', raw.eventType);
      });
      rawCCBCallbackJobIds(content).forEach((jobId) => callbackJobs.add(jobId));
      return;
    }
    if (eventType) recordCCBAgentState(agentState, agent || 'ccb', eventType);
    const payload = data.payload && typeof data.payload === 'object' ? data.payload as Record<string, any> : {};
    for (const key of ['callback_child_job_id', 'child_job_id', 'continuation_job_id']) {
      const jobId = stringsTrim(String(payload[key] || ''));
      if (jobId) callbackJobs.add(jobId);
    }
    if (String(data.contentKind || '') === 'agent_console') {
      recordCCBAgentState(agentState, agent || 'ccb', 'agent_console');
    }
  });
  const parts: string[] = [];
  Array.from(agentState.keys()).sort().forEach((agent) => {
    const states = agentState.get(agent);
    if (!states?.size) return;
    parts.push(`${agent} ${Array.from(states).join('/')}`);
  });
  if (callbackJobs.size) parts.push(`callback ${Array.from(callbackJobs).join(', ')}`);
  return parts.join('; ');
}

function recordCCBAgentState(map: Map<string, Set<string>>, agent: string, eventType: string) {
  agent = stringsTrim(agent).toLowerCase() || 'ccb';
  const state = ccbEventStateLabel(eventType);
  if (!state) return;
  if (!map.has(agent)) map.set(agent, new Set());
  map.get(agent)?.add(state);
}

function ccbEventStateLabel(eventType: string) {
  const value = stringsTrim(eventType).toLowerCase();
  switch (value) {
    case 'job_accepted':
    case 'job_queued':
      return 'accepted';
    case 'job_started':
      return 'started';
    case 'completion_item':
      return 'streaming';
    case 'completion_terminal':
    case 'job_completed':
      return 'completed';
    case 'job_failed':
    case 'job_incomplete':
    case 'job_cancelled':
      return 'failed';
    case 'job_delegated_callback':
    case 'callback_edge_created':
    case 'callback_continuation_submitted':
      return 'callback';
    case 'agent_console':
      return 'console';
    default:
      return '';
  }
}

function isReadableFinalConclusion(content?: string) {
  const value = stringsTrim(content).toLowerCase();
  if (!value) return false;
  if (isRawCCBObserverDump(value)) return false;
  if (hasUnresolvedAcceptanceSignal(value)) return false;
  if (value.includes('最终结论') || value.includes('最终总结') || value.includes('final conclusion') || value.includes('final summary')) {
    return true;
  }
  const hasConclusion = value.includes('结论') || value.includes('总结') || value.includes('conclusion') || value.includes('summary');
  const hasCompletion = value.includes('完成') || value.includes('通过') || value.includes('验证') || value.includes('completed') || value.includes('verified') || value.includes('passed');
  const hasRisk = value.includes('剩余风险') || value.includes('remaining risk');
  return hasConclusion && (hasCompletion || hasRisk);
}

function unresolvedAcceptanceSummary(events: OrchestrationEvent[]) {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index];
    const candidates = [
      event.content || '',
      event.error || '',
      typeof event.data?.output === 'string' ? event.data.output : '',
      typeof event.data?.command === 'string' ? event.data.command : '',
    ];
    const hit = candidates.find(hasUnresolvedAcceptanceSignal);
    if (hit) return acceptanceIssueLine(hit);
  }
  return '';
}

function hasUnresolvedAcceptanceSignal(content?: string) {
  const value = stringsTrim(content).toLowerCase();
  if (!value) return false;
  if (hasResolvedSorrySignal(value) && !hasExplicitUnresolvedSorryRisk(value)) {
    return false;
  }
  if (includesAny(value, ['main theorem', '主定理', 'termination modify_lin', 'modify_lin'])
    && includesAny(value, ['sorry', '未消除', '没有消除', '还保留', 'placeholder', '占位'])) {
    return true;
  }
  return includesAny(value, [
    'sorry placeholder',
    'sorry placeholders',
    'still contains sorry',
    'contains sorry',
    'quick_and_dirty',
    '可编译的证明框架',
    '证明框架可编译',
    '不是完整证明',
    '不是完全无 sorry',
    '只是通过编译',
    '只能说通过编译',
    '没有实质上的进展',
  ]);
}

function hasExplicitUnresolvedSorryRisk(value: string) {
  return includesAny(value, [
    'sorry placeholder',
    'sorry placeholders',
    'still contains sorry',
    'contains sorry',
    'quick_and_dirty',
    '可编译的证明框架',
    '证明框架可编译',
    '不是完整证明',
    '不是完全无 sorry',
    'not without sorry',
    'not fully without sorry',
    'not a completed proof',
  ]);
}

function hasResolvedSorrySignal(value: string) {
  if (includesAny(value, [
    'without sorry',
    'without any sorry',
    'no sorry placeholders',
    'no remaining sorry',
    '无 sorry',
    '无sorry',
    '没有 sorry',
    'without quick_and_dirty',
    'quick_and_dirty = false',
  ])) {
    return true;
  }
  return /\bno\s+sorry\b/.test(value);
}

function acceptanceIssueLine(content?: string) {
  const lines = decodeEscapedLineBreaks(String(content || '')).split(/\r?\n/);
  const line = lines.find(hasUnresolvedAcceptanceSignal) || stringsTrim(content);
  return line ? trimMiddle(line.trim(), 220) : 'the requested acceptance criterion remains unresolved';
}

function includesAny(value: string, signals: string[]) {
  return signals.some((signal) => value.includes(signal.toLowerCase()));
}

function trimMiddle(value: string, max: number) {
  if (value.length <= max) return value;
  const left = Math.max(20, Math.floor((max - 3) * 0.6));
  const right = Math.max(20, max - 3 - left);
  return `${value.slice(0, left)}...${value.slice(-right)}`;
}

function textLooksChinese(content?: string) {
  return /[\u3400-\u9fff]/.test(content || '');
}

function commandEventFailed(event: OrchestrationEvent) {
  const status = String(event.data?.status || event.status || '').toLowerCase();
  const exitCode = event.data?.exitCode;
  return Boolean(event.error) || status === 'failed' || status === 'error' || (typeof exitCode === 'number' && exitCode !== 0);
}

function mergeOrchestrationDeltaEvents(events: OrchestrationEvent[]): OrchestrationEvent[] {
  const merged: OrchestrationEvent[] = [];
  const deltaIndexes = new Map<string, number>();
  events.forEach((event) => {
    if (event.kind !== 'turn.delta') {
      merged.push(event);
      return;
    }
    const content = decodeEscapedLineBreaks(String(event.content || ''));
    if (!stringsTrim(content)) return;
    const key = orchestrationTurnKey(event);
    const index = deltaIndexes.get(key);
    if (typeof index !== 'number') {
      deltaIndexes.set(key, merged.length);
      merged.push({ ...event, content });
      return;
    }
    const previous = merged[index];
    merged[index] = {
      ...previous,
      content: mergeDeltaContent(previous.content || '', content),
      status: event.status || previous.status,
      error: event.error || previous.error,
      createdAt: previous.createdAt || event.createdAt,
      seq: previous.seq || event.seq,
    };
  });
  return merged;
}

function mergeDeltaContent(previous: string, next: string) {
  if (!previous) return next;
  if (!next) return previous;
  if (next.startsWith(previous)) return next;
  if (previous.endsWith(next)) return previous;
  return previous + next;
}

function cleanOrchestrationDisplayContent(content?: string) {
  const value = stringsTrim(stripMachineContractLines(content));
  if (!value) return '';
  if (isRawCCBObserverDump(value)) return '';
  const index = conclusionDisplayTrimIndex(value);
  return index > 0 && shouldTrimConclusionDisplayPrefix(value.slice(0, index)) ? value.slice(index).trim() : value;
}

function isRawCCBObserverDump(content?: string) {
  const value = stringsTrim(content);
  if (!value) return false;
  const lines = value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  if (lines.length < 4) return false;
  const metadata = lines.filter((line) => isCCBObserverMetadataLine(line)).length;
  return metadata >= Math.max(4, Math.ceil(lines.length * 0.6));
}

function isCCBObserverMetadataLine(line: string) {
  const key = stringsTrim(line.split(':', 1)[0]).toLowerCase();
  return [
    'observer_view',
    'observer_authority',
    'observer_terminal',
    'observer_notice',
    'watch_status',
    'job_id',
    'agent_name',
    'target_name',
    'status',
    'reply',
    'event',
  ].includes(key);
}

function rawCCBObserverEventTypes(content?: string) {
  return stringsTrim(content).split(/\r?\n/).flatMap((line) => {
    const match = line.trim().match(/^event:\s+\S+\s+\S+\s+(\S+)\s+(\S+)/);
    if (!match) return [];
    return [{ agent: match[1], eventType: match[2] }];
  });
}

function rawCCBCallbackJobIds(content?: string) {
  return Array.from(new Set(stringsTrim(content).match(/\bjob_[A-Za-z0-9_-]+\b/g) || []));
}

function conclusionDisplayTrimIndex(value: string) {
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

function shouldTrimConclusionDisplayPrefix(prefix: string) {
  const value = stringsTrim(prefix).toLowerCase();
  if (!value) return false;
  const signals = ['我会', '我先', '我将', '接下来', '正在', '不展开新的', 'i will', "i'll", 'i am going to', 'next i'];
  const count = signals.reduce((sum, signal) => sum + value.split(signal).length - 1, 0);
  return count >= 2 || value.startsWith('我会') || value.startsWith('我先') || Array.from(value).length > 240;
}

function stringsTrim(value?: string) {
  return decodeEscapedLineBreaks(String(value || '')).trim();
}

function stripMachineContractLines(content?: string) {
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

function isMachineContractLine(line: string) {
  const value = line.trim();
  return /^Msg:\s*to=[^;]+;\s*intent=[^;]+;\s*need=/i.test(value)
    || /^Handoff:\s*status=(needs_next|blocked|resolved)\b/i.test(value);
}

function orchestrationEventFiles(event: OrchestrationEvent): OrchestrationFile[] {
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

function mergeOrchestrationFiles(...groups: Array<OrchestrationFile[] | undefined>): OrchestrationFile[] {
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

function orchestrationRunFilesFromEvents(events: OrchestrationEvent[], runId: string): OrchestrationFile[] {
  return mergeOrchestrationFiles(
    ...events
      .filter((event) => event.runId === runId && event.kind === 'user.message')
      .map(orchestrationEventFiles)
  );
}

function decodeEscapedLineBreaks(value: string) {
  if (/[\r\n]/.test(value)) return value;
  const escapedBreaks = value.match(/\\r\\n|\\n|\\r/g);
  if (!escapedBreaks || escapedBreaks.length < 2) return value;
  return value
    .replace(/\\r\\n/g, '\n')
    .replace(/\\n/g, '\n')
    .replace(/\\r/g, '\n')
    .replace(/\\t/g, '\t');
}

function orchestrationCommandSummary(event: OrchestrationEvent) {
  const data = event.data || {};
  const command = typeof data.command === 'string' ? data.command.trim() : '';
  const output = typeof data.output === 'string' ? data.output.trim() : '';
  const fallback = stringsTrim(event.error || event.content || event.status || event.kind);
  if (command && output) return `${command}\n\n${output}`;
  return command || output || fallback;
}

function isEmptyPagesReadFailureEvent(event: OrchestrationEvent) {
  if (!event.kind.startsWith('command.')) return false;
  const data = event.data || {};
  const command = typeof data.command === 'string' ? data.command.trim() : '';
  const output = typeof data.output === 'string' ? data.output : '';
  const status = typeof data.status === 'string' ? data.status : event.status || '';
  return (
    status.toLowerCase() === 'failed' &&
    command.startsWith('Read ') &&
    output.includes('Invalid pages parameter: ""') &&
    output.includes('Pages are 1-indexed')
  );
}

function shouldShowOrchestrationStatus(event: OrchestrationEvent) {
  if (event.kind === 'run.start' || event.kind === 'turn.start') return true;
  if (event.kind === 'run.end') {
    const content = stringsTrim(event.content);
    return Boolean(event.error || (content && content !== 'Orchestration completed.'));
  }
  return event.kind === 'run.error' || event.kind === 'run.cancelled' || event.kind === 'run.canceling' || Boolean(event.error);
}

function statusVisibleEvent(event: OrchestrationEvent, index: number): OrchestrationVisibleEvent {
  return {
    type: 'status',
    key: orchestrationEventKey(event, index),
    runId: event.runId,
    kind: event.kind,
    role: event.role,
    cli: event.cli,
    turnId: event.turnId,
    content: stringsTrim(event.error || event.content || event.status || event.kind),
    status: event.status,
    error: event.error,
    createdAt: event.createdAt,
  };
}

function applyOrchestrationEventToRun(run: OrchestrationRun, event: OrchestrationEvent): OrchestrationRun {
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

function isNearBottom(element: HTMLElement, threshold = 120) {
  return element.scrollHeight - element.scrollTop - element.clientHeight <= threshold;
}

async function copyText(value: string): Promise<boolean> {
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

function AgentSelector({
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

function startWSHeartbeat(ws: WebSocket, sid?: string) {
  const send = () => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'heartbeat', sid, payload: { ts: Date.now() } }));
    }
  };
  send();
  const id = window.setInterval(send, 15000);
  return () => window.clearInterval(id);
}

function defaultAgentID(agents: Agent[]) {
  return (agents.find((agent) => agent.online) || agents[0])?.id || '';
}

function preferredAgentID(agents: Agent[], current: string) {
  const selected = agents.find((agent) => agent.id === current);
  if (selected?.online) return selected.id;
  return defaultAgentID(agents) || selected?.id || '';
}

function orchestrationApprovalMode(agent?: Agent | null) {
  const caps = agent?.capabilities;
  if (!caps) return agent?.online ? 'unknown' : 'offline';
  if (caps.approvalPolicy === 'never' && caps.sandbox === 'danger-full-access') return 'auto-execute';
  if (caps.metadata?.approvalMode === 'auto-execute') return 'auto-execute';
  return 'review-required';
}

function orchestrationWorkerLabel(agent: Agent | null | undefined, t: UIText) {
  return t.manualOrchestration;
}

function orchestrationCapability(agent: Agent | null | undefined, cli: 'claude' | 'codex') {
  return agent?.capabilities?.orchestration?.[cli];
}

function orchestrationCapabilityProblems(agent: Agent | null | undefined, t: UIText) {
  if (!agent) return [t.noBridgeConnected];
  if (!agent.online) return [t.agentOffline];
  if (orchestrationApprovalMode(agent) !== 'review-required') return [];
  const problems: string[] = [];
  if (!orchestrationCapability(agent, 'claude')?.browserApproval) problems.push(t.claudeOrchestrationApprovalMissing);
  if (!orchestrationCapability(agent, 'codex')?.browserApproval) problems.push(t.codexOrchestrationApprovalMissing);
  return problems;
}

const activeSessionByAgentStorageKey = 'codexBridge.activeSessionByAgent';

function readActiveSessionByAgent(): Record<string, string> {
  try {
    const raw = localStorage.getItem(activeSessionByAgentStorageKey);
    const parsed = raw ? JSON.parse(raw) : {};
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, string> : {};
  } catch {
    return {};
  }
}

function rememberActiveSessionForAgent(agentId: string, sessionId: string) {
  if (!agentId || !sessionId) return;
  const current = readActiveSessionByAgent();
  current[agentId] = sessionId;
  localStorage.setItem(activeSessionByAgentStorageKey, JSON.stringify(current));
}

function forgetActiveSessionForAgent(agentId: string, sessionId?: string) {
  if (!agentId) return;
  const current = readActiveSessionByAgent();
  if (!sessionId || current[agentId] === sessionId) {
    delete current[agentId];
    localStorage.setItem(activeSessionByAgentStorageKey, JSON.stringify(current));
  }
}

function agentStatusClass(agent?: Agent) {
  return cn("h-2 w-2 rounded-full", agent?.online ? "bg-emerald-500" : "bg-muted-foreground");
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

function readUploadAttachment(file: File): Promise<UploadAttachment> {
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
        mimeType: file.type || 'application/octet-stream',
        size: file.size,
        data: comma === -1 ? value : value.slice(comma + 1),
      });
    };
    reader.readAsDataURL(file);
  });
}

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

function OrchestrationFileList({ files, label, compact = false }: { files: OrchestrationFile[]; label?: string; compact?: boolean }) {
  if (!files.length) return null;
  return (
    <div className={cn("space-y-1.5", label && "mt-2")}>
      {label && <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{label}</div>}
      <div className={cn(compact ? "space-y-1.5" : "flex flex-wrap gap-1.5")}>
        {files.map((file, index) => (
          <div key={`${file.name}-${file.size}-${index}`} className={cn(
            "min-w-0 rounded-md border border-border bg-muted/25 px-2 py-1.5 text-xs",
            compact ? "flex items-center gap-2" : "inline-flex max-w-full items-center gap-2"
          )}>
            <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 truncate">{file.name}</span>
            <span className="shrink-0 text-[10px] text-muted-foreground">{formatBytes(file.size)}</span>
            {file.mimeType && <span className="hidden shrink-0 rounded border border-border px-1 py-0.5 text-[10px] text-muted-foreground sm:inline">{file.mimeType}</span>}
          </div>
        ))}
      </div>
    </div>
  );
}

function initialLanguage(): Language {
  const saved = localStorage.getItem('codexBridge.language');
  if (saved === 'zh' || saved === 'en') return saved;
  return navigator.language?.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

function MessageContent({ content, stripMachineContracts = false }: { content: string; stripMachineContracts?: boolean }) {
  const visibleContent = useMemo(
    () => stripMachineContracts ? stripMachineContractLines(content) : String(content || ''),
    [content, stripMachineContracts]
  );
  const html = useMemo(() => {
    const chunks = String(visibleContent || '').split(/```([\s\S]*?)```/g);
    return chunks.map((chunk, index) => {
      if (index % 2 === 1) {
        return `<pre class="my-3 overflow-x-auto rounded-lg border border-border bg-muted/70 p-3 text-xs leading-relaxed text-foreground dark:bg-[#0f172a] dark:text-slate-200"><code>${escapeBasic(chunk.replace(/^\w+\n/, ''))}</code></pre>`;
      }
      return renderInlineMarkdown(chunk).replace(/\n/g, '<br />');
    }).join('');
  }, [visibleContent]);

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
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const [path, setPath] = useState(() => window.location.pathname);
  const t = uiText[language];
  const isSnapshotRoute = path.startsWith('/conversation-snapshot');
  const isShareRoute = path.startsWith('/share/');

  useEffect(() => {
    document.documentElement.classList.toggle('dark', isDarkMode);
    localStorage.setItem('codexBridge.theme', isDarkMode ? 'dark' : 'light');
  }, [isDarkMode]);

  useEffect(() => {
    document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en';
    localStorage.setItem('codexBridge.language', language);
  }, [language]);

  useEffect(() => {
    if (isShareRoute) {
      setBooting(false);
      return;
    }
    api<{ user: UserAccount }>('/api/me')
      .then((data) => setUser(data.user))
      .catch(() => setUser(null))
      .finally(() => setBooting(false));
  }, [isShareRoute]);

  useEffect(() => {
    const handlePop = () => setPath(window.location.pathname);
    window.addEventListener('popstate', handlePop);
    return () => window.removeEventListener('popstate', handlePop);
  }, []);

  useEffect(() => {
    if (user && !user.isAdmin && !path.startsWith('/orchestrate') && !path.startsWith('/conversation-snapshot') && !path.startsWith('/share/')) {
      window.history.replaceState({}, '', '/orchestrate');
      setPath('/orchestrate');
    }
  }, [path, user]);

  const navigate = useCallback((nextPath: string) => {
    if (user && !user.isAdmin && !nextPath.startsWith('/orchestrate') && !nextPath.startsWith('/conversation-snapshot') && !nextPath.startsWith('/share/')) {
      nextPath = '/orchestrate';
    }
    if (window.location.pathname !== nextPath) {
      window.history.pushState({}, '', nextPath);
      setPath(nextPath);
    }
  }, [user]);

  if (booting) {
    return (
      <div className="min-h-screen w-full flex items-center justify-center bg-background text-foreground">
        <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (isShareRoute) {
    return <PublicSharePage shareID={decodeURIComponent(path.replace(/^\/share\/?/, '').split('/')[0] || '')} t={t} />;
  }

  if (!user) {
    return <LoginScreen onLogin={setUser} language={language} setLanguage={setLanguage} t={t} />;
  }

  if (isSnapshotRoute) {
    return <ConversationSnapshotPage t={t} />;
  }

  if (!user.isAdmin || path.startsWith('/orchestrate')) {
    return (
      <OrchestrationWorkspace
        user={user}
        onLogout={() => setUser(null)}
        isDarkMode={isDarkMode}
        setIsDarkMode={setIsDarkMode}
        language={language}
        setLanguage={setLanguage}
        t={t}
        canOpenMain={Boolean(user.isAdmin)}
        navigate={navigate}
      />
    );
  }

  return (
    <Workspace
      user={user}
      onLogout={() => setUser(null)}
      isDarkMode={isDarkMode}
      setIsDarkMode={setIsDarkMode}
      language={language}
      setLanguage={setLanguage}
      t={t}
      navigate={navigate}
    />
  );
}

function LoginScreen({
  onLogin,
  language,
  setLanguage,
  t,
}: {
  onLogin: (user: UserAccount) => void;
  language: Language;
  setLanguage: (value: Language) => void;
  t: UIText;
}) {
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
      setError(err instanceof Error ? err.message : t.connectionFailed);
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
          <p className="text-sm text-muted-foreground">{t.secureConnection}</p>
        </div>

        <form onSubmit={handleLogin} className="flex flex-col gap-4">
          <div className="space-y-4">
            <div className="space-y-2">
              <label className="text-sm font-medium leading-none" htmlFor="username">
                {t.username}
              </label>
              <div className="relative">
                <User className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input id="username" name="username" placeholder="admin" className="pl-9" autoComplete="username" required />
              </div>
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium leading-none" htmlFor="password">
                {t.password}
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
            {loading ? <RefreshCw className="h-4 w-4 animate-spin" /> : t.connectToWorkspace}
          </Button>
        </form>

        <div className="flex justify-center mt-4">
          <Button variant="ghost" size="sm" className="text-muted-foreground gap-2" onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')}>
            <Globe className="h-4 w-4" />
            {language === 'zh' ? t.chinese : t.english}
            <ChevronDown className="h-3 w-3 opacity-50" />
          </Button>
        </div>
      </div>
    </div>
  );
}

function Workspace({
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
  const wsRef = useRef<WebSocket | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const messageScrollRef = useRef<HTMLDivElement | null>(null);
  const messageEndRef = useRef<HTMLDivElement | null>(null);
  const stickToBottomRef = useRef(true);
  const activeSessionIdRef = useRef('');
  const selectedAgentIdRef = useRef(selectedAgentId);
  const assistantItemIdRef = useRef<string | null>(null);
  const assistantTextRef = useRef('');

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
  }, [appendSystem, clearActiveChat, t.connected, t.error, t.ready, thread, touchSession]);

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
    const [loadedAgents, loadedSessions] = await Promise.all([loadAgents(), loadSessions()]);
    const savedAgentId = localStorage.getItem('codexBridge.selectedAgentId') || selectedAgentIdRef.current;
    const agentId = preferredAgentID(loadedAgents, savedAgentId);
    selectedAgentIdRef.current = agentId;
    const activeSession = loadedSessions.find((session) => session.id === activeSessionIdRef.current);
    if (activeSession && (!agentId || activeSession.agentId === agentId)) {
      return;
    }
    await switchAgentSession(agentId, loadedSessions);
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

            <Button variant="ghost" size="icon" className="text-muted-foreground rounded-full h-8 w-8" onClick={() => refreshAll().catch((err) => appendSystem(err.message))}>
              <RefreshCw className="h-4 w-4" />
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
          <AgentSelector
            agents={agents}
            selectedAgentId={selectedAgentId}
            onSelect={selectAgent}
            t={t}
            className="sm:hidden min-w-[220px]"
          />
        </div>

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

function OrchestrationWorkspace({
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
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const activeRunIdRef = useRef('');
  const selectedAgentIdRef = useRef(selectedAgentId);
  const stickToBottomRef = useRef(true);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const taskInputRef = useRef<HTMLTextAreaElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const endRef = useRef<HTMLDivElement | null>(null);

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
    setShowScrollBottom((visibleEvents.length + visibleApprovals.length) > 0 && !nearBottom);
  }, [visibleApprovals.length, visibleEvents.length]);

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
    setConnectionStatus(t.idle);
    setShowScrollBottom(false);
  }, [closeWS, runs, t.idle]);

  const applyEvent = useCallback((event: OrchestrationEvent) => {
    setEvents((current) => {
      if (activeRunIdRef.current !== event.runId) return current;
      return mergeOrchestrationEvents(current, [event]);
    });
    setRuns((current) => {
      if (!current.some((run) => run.id === event.runId)) return current;
      return current
        .map((run) => run.id === event.runId ? applyOrchestrationEventToRun(run, event) : run)
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
            setApprovals((current) => upsertApprovalItem(current, approval));
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
      setShowScrollBottom((visibleEvents.length + visibleApprovals.length) > 0 && !isNearBottom(container));
    });
    return () => window.cancelAnimationFrame(id);
  }, [activeRunId, visibleApprovals.length, visibleEvents, scrollTimelineToBottom]);

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
            <Button variant="ghost" size="icon" className="text-muted-foreground rounded-full h-8 w-8" onClick={() => refreshOrchestration().catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration))}>
              <RefreshCw className="h-4 w-4" />
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
              {!visibleEvents.length && !visibleApprovals.length ? (
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
                  {visibleEvents.map((event) => <OrchestrationEventItem key={event.key} item={event} t={t} />)}
                  {visibleApprovals.map((item) => <ApprovalCard key={item.id} item={item} t={t} onDecision={respondOrchestrationApproval} />)}
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

          <aside className="min-h-0 border-t lg:border-t-0 lg:border-l border-border bg-background/95 p-4 overflow-y-auto lg:overflow-hidden elegant-scrollbar">
            <div className="flex min-h-full flex-col gap-3 lg:h-full lg:min-h-0">
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

                <label className="block space-y-2">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.turns}</span>
                  <Input type="number" min={2} max={12} value={maxTurns} onChange={(event) => setMaxTurns(Number(event.target.value) || 4)} disabled={creating || isRunning} />
                </label>
              </div>

              <div className="min-h-0 flex flex-1 flex-col gap-2">
                <div className="flex shrink-0 items-center justify-between">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.files}</span>
                  <Button variant="ghost" size="sm" className="h-7 gap-1.5" onClick={() => fileInputRef.current?.click()} disabled={creating || isRunning}>
                    <FileUp className="h-3.5 w-3.5" />
                    {t.add}
                  </Button>
                </div>
                <input ref={fileInputRef} type="file" multiple className="hidden" onChange={(event) => addFiles(event.target.files).catch((err) => setError(err.message))} />
                <div className="min-h-0 flex-1 space-y-1.5 overflow-y-auto pr-1 elegant-scrollbar">
                  {files.length === 0 ? (
                    activeRunFiles.length > 0 ? (
                      <OrchestrationFileList files={activeRunFiles} label={t.currentRunFiles} compact />
                    ) : (
                      <div className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">{t.uploadProofFiles}</div>
                    )
                  ) : files.map((file) => (
                    <div key={file.id} className="flex items-center gap-2 rounded-md border border-border bg-muted/20 px-2 py-1.5">
                      <FileText className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                      <span className="min-w-0 flex-1 truncate text-xs">{file.name}</span>
                      <span className="text-[10px] text-muted-foreground">{formatBytes(file.size)}</span>
                      <button className="text-muted-foreground hover:text-foreground" onClick={() => removeFile(file.id)} aria-label={`${t.removeFile} ${file.name}`}>
                        <X className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  ))}
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

function CapabilityMatrix({ agent, t }: { agent: Agent | null; t: UIText }) {
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

function OrchestrationEventItem({ item, t }: { item: OrchestrationVisibleEvent, t: UIText }) {
  const isUser = item.kind === 'user.message';
  const isRun = item.kind.startsWith('run.');
  const avatar = orchestrationAvatar(item, t);
  const title = isUser ? t.user : isRun ? t.run : item.type === 'command' ? t.commands : `${item.role || t.agent}${item.cli ? ` · ${avatar.label}` : ''}`;
  const rawContent = item.error || item.content || '';
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

function CommandEvent({ event, t, open = false }: { event: OrchestrationEvent, t: UIText, open?: boolean }) {
  const [, setClockTick] = useState(0);
  const data = event.data || {};
  const command = typeof data.command === 'string' ? data.command : '';
  const output = typeof data.output === 'string' ? data.output : '';
  const status = typeof data.status === 'string' ? data.status : event.status || '';
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

function SidebarContent({
  groupedSessions,
  activeSession,
  setActiveSession,
  createSession,
  shareSession,
  renameSession,
  deleteSession,
  search,
  setSearch,
  openSettings,
  agentOnline,
  openOrchestration,
  shareCopiedSessionId,
  sharingSessionId,
  t,
}: {
  groupedSessions: Record<string, Session[]>;
  activeSession: string;
  setActiveSession: (id: string) => void;
  createSession: () => void;
  shareSession: (session: Session) => void;
  renameSession: (session: Session) => void;
  deleteSession: (session: Session) => void;
  search: string;
  setSearch: (value: string) => void;
  openSettings: () => void;
  agentOnline: boolean;
  openOrchestration: () => void;
  shareCopiedSessionId: string;
  sharingSessionId: string;
  t: UIText;
}) {
  return (
    <>
      <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
        <div className="flex items-center gap-2 font-medium">
          <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
            <Terminal className="h-3.5 w-3.5" />
          </div>
          <span className="text-sm">{t.codexBridge}</span>
        </div>
      </div>

      <div className="p-3">
        <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm" onClick={createSession}>
          <Plus className="h-4 w-4" />
          {t.newSession}
        </Button>
        <Button variant="ghost" className="mt-2 w-full justify-start gap-2 h-9 rounded-lg text-muted-foreground" onClick={openOrchestration}>
          <GitBranch className="h-4 w-4" />
          {t.orchestration}
        </Button>
      </div>

      <div className="px-3 pb-2">
        <div className="relative">
          <Search className="absolute left-2.5 top-2 h-3.5 w-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder={t.searchSessions}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="w-full h-8 pl-8 pr-3 text-xs bg-sidebar-accent/50 border border-sidebar-border rounded-md focus:outline-none focus:ring-1 focus:ring-ring transition-all"
          />
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-2 space-y-4 elegant-scrollbar">
        {Object.keys(groupedSessions).length === 0 ? (
          <div className="px-2 py-1.5 text-xs text-muted-foreground">{t.noSessions}</div>
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
                  <span className="truncate">{displaySessionTitle(session, t)}</span>

                  <div className="ml-auto flex items-center gap-1">
                    <span
                      className={cn(
                        "h-5 w-5 rounded flex items-center justify-center hover:bg-sidebar-border text-muted-foreground",
                        shareCopiedSessionId === session.id && "text-emerald-600 dark:text-emerald-400"
                      )}
                      onClick={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                        shareSession(session);
                      }}
                      title={shareCopiedSessionId === session.id ? t.copied : t.shareConversation}
                    >
                      {sharingSessionId === session.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : shareCopiedSessionId === session.id ? <Check className="h-3 w-3" /> : <Share2 className="h-3 w-3" />}
                    </span>
                    {activeSession === session.id && (
                      <>
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
                      </>
                    )}
                  </div>
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
          <span className="flex-1 text-left">{t.settings}</span>
          <div className={cn("h-1.5 w-1.5 rounded-full", agentOnline ? "bg-emerald-500" : "bg-muted-foreground")} title={agentOnline ? t.agentOnline : t.agentOffline} />
        </button>
      </div>
    </>
  );
}

function PublicSharePage({ shareID, t }: { shareID: string; t: UIText }) {
  const [payload, setPayload] = useState<PublicSharePayload | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const visibleEvents = useMemo(() => {
    if (!payload?.run) return [];
    return visibleOrchestrationEvents(payload.events || [], payload.run.id, payload.run, t);
  }, [payload, t]);

  useEffect(() => {
    let stopped = false;
    if (!shareID) {
      setError(t.failedLoadShare);
      setLoading(false);
      return () => {
        stopped = true;
      };
    }
    setLoading(true);
    setError('');
    api<PublicSharePayload>(`/api/public/shares/${encodeURIComponent(shareID)}`)
      .then((data) => {
        if (!stopped) setPayload(data);
      })
      .catch((err) => {
        if (!stopped) setError(err instanceof Error ? err.message : t.failedLoadShare);
      })
      .finally(() => {
        if (!stopped) setLoading(false);
      });
    return () => {
      stopped = true;
    };
  }, [shareID, t.failedLoadShare]);

  const title = payload?.share.title || payload?.session?.title || payload?.run?.title || t.publicShare;
  const isOrchestration = payload?.share.kind === 'orchestration';
  const messages = payload?.messages || [];
  const goToLogin = () => {
    window.location.href = '/';
  };

  return (
    <div className="h-screen w-full flex bg-background text-foreground overflow-hidden font-sans">
      <aside className="hidden md:flex w-[260px] flex-col border-r border-sidebar-border bg-sidebar">
        <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
          <div className="flex items-center gap-2 font-medium">
            <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
              <Share2 className="h-3.5 w-3.5" />
            </div>
            <span className="text-sm">{t.publicShare}</span>
          </div>
        </div>

        <div className="p-3 space-y-2">
          <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm pointer-events-none" disabled>
            {isOrchestration ? <GitBranch className="h-4 w-4" /> : <MessageSquare className="h-4 w-4" />}
            <span className="truncate">{title}</span>
          </Button>
          <Button variant="ghost" className="w-full justify-start gap-2 h-9 rounded-lg" onClick={goToLogin}>
            <Lock className="h-4 w-4" />
            {t.signInToContinue}
          </Button>
        </div>

        <div className="flex-1 overflow-y-auto px-3 py-2 elegant-scrollbar">
          <div>
            <h4 className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider mb-1.5 px-2">
              {payload?.share.updatedAt ? sessionDateLabel(payload.share.updatedAt, t) : t.readOnlySnapshot}
            </h4>
            <div className="w-full text-left px-2 py-1.5 rounded-md text-sm flex items-center gap-2 bg-sidebar-accent text-sidebar-accent-foreground font-medium">
              {isOrchestration ? <GitBranch className="h-3.5 w-3.5 opacity-70 shrink-0" /> : <MessageSquare className="h-3.5 w-3.5 opacity-70 shrink-0" />}
              <span className="truncate">{title}</span>
            </div>
          </div>
        </div>

        <div className="p-3 border-t border-sidebar-border shrink-0 mt-auto bg-sidebar">
          <div className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-sidebar-foreground">
            <div className="h-6 w-6 rounded-full bg-sidebar-primary/10 flex items-center justify-center">
              <Check className="h-3.5 w-3.5" />
            </div>
            <span className="flex-1 text-left">{t.readOnlySnapshot}</span>
            <div className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
          </div>
        </div>
      </aside>

      <main className="flex-1 flex flex-col min-w-0 h-full">
        <header className="h-14 shrink-0 border-b border-border flex items-center justify-between px-3 md:px-4 bg-background z-10">
          <div className="flex items-center gap-2 min-w-0">
            <Share2 className="h-4 w-4 text-muted-foreground shrink-0" />
            <span className="text-sm font-medium truncate">{title}</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden sm:inline-flex rounded-full border border-border bg-muted/40 px-2.5 py-1 text-xs text-muted-foreground">
              {t.readOnlySnapshot}
            </span>
            <Button variant="secondary" size="sm" className="h-8 gap-1.5 rounded-lg" onClick={goToLogin}>
              <Lock className="h-3.5 w-3.5" />
              {t.signInToContinue}
            </Button>
          </div>
        </header>

        <div className="bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          <div className="flex items-center gap-1.5">
            {isOrchestration ? <GitBranch className="h-3.5 w-3.5" /> : <MessageSquare className="h-3.5 w-3.5" />}
            <span>{t.publicShare}: {payload?.share.kind || '-'}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            <span>{t.status}: {payload?.run?.status || t.ready}</span>
          </div>
          {payload?.share.updatedAt && (
            <div className="flex items-center gap-1.5">
              <Command className="h-3.5 w-3.5" />
              <span>{t.thread}: {sessionDateLabel(payload.share.updatedAt, t)}</span>
            </div>
          )}
        </div>

        <div className="relative flex-1 min-h-0">
          <div className="h-full overflow-y-auto p-4 md:p-6 space-y-4 elegant-scrollbar">
            {loading ? (
              <div className="h-full flex items-center justify-center">
                <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : error ? (
              <div className="mx-auto mt-10 flex max-w-lg items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>{error}</span>
              </div>
            ) : isOrchestration ? (
              visibleEvents.length > 0 ? (
                visibleEvents.map((event) => <OrchestrationEventItem key={event.key} item={event} t={t} />)
              ) : payload?.run?.prompt ? (
                <MessageItem
                  msg={{ id: `${payload.run.id}:prompt`, type: 'message', role: 'user', content: payload.run.prompt, createdAt: payload.run.createdAt }}
                  t={t}
                  readOnly
                />
              ) : null
            ) : (
              messages.map((message) => (
                <MessageItem
                  key={message.id}
                  msg={{ id: message.id, type: 'message', role: message.role, content: message.content, createdAt: message.createdAt }}
                  t={t}
                  readOnly
                />
              ))
            )}
            <div className="h-4" />
          </div>
        </div>

        <div className="shrink-0 p-4 border-t border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
          <div className="max-w-4xl mx-auto flex flex-col bg-card border border-border rounded-xl shadow-sm">
            <textarea
              className="w-full bg-transparent border-0 resize-none p-3 text-sm focus:outline-none focus:ring-0 min-h-[60px] max-h-[120px] text-muted-foreground"
              value=""
              placeholder={t.askCodex}
              disabled
              readOnly
            />
            <div className="flex items-center justify-between p-2 pt-0">
              <Button variant="ghost" size="icon" type="button" className="h-8 w-8 text-muted-foreground rounded-lg" disabled>
                <ImagePlus className="h-4 w-4" />
              </Button>
              <Button size="sm" type="button" className="h-8 px-3 rounded-lg gap-1.5 text-xs font-medium" disabled>
                {t.send}
                <Send className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
          <div className="text-center mt-2">
            <span className="text-[10px] text-muted-foreground/60 font-medium">{t.verifyNotice}</span>
          </div>
        </div>
      </main>
    </div>
  );
}

type SnapshotItem =
  | { id: string; type: 'message'; role: 'user' | 'assistant' | 'system'; content: string; createdAt: number }
  | { id: string; type: 'tool'; title: string; command: string; output: string; createdAt: number; status?: string; kind?: string; defaultOpen?: boolean };

const conversationSnapshotItems: SnapshotItem[] = [
  {
    id: 'snapshot-user-task',
    type: 'message',
    role: 'user',
    createdAt: 1779634200,
    content: [
      '已上传了三个文件。Model.thy是HWQueue的Isabelle模型，现在想证明Termination.thy中的termination modify_lin。请你填上证明中的sorry。',
      '',
      'modify_lin的可终止性证明会非常复杂，建议你们先讨论出一个可用的证明框架，里面可以用sorry填充。但是不能更改原来的模型，也不能自行引入公理。',
      '',
      '我建议你们还是一步一步来，先生成一个可以编译通过的证明框架。checker可以接受证明框架里存在sorry。关于这个任务要求你们先商量出用户的真实意图再去做，放到该路径下新建的一个文件夹中',
      '',
      '上传文件：`Model.thy`、`Termination.thy`、`ROOT`',
    ].join('\n'),
  },
  {
    id: 'snapshot-assistant-implementer',
    type: 'message',
    role: 'assistant',
    createdAt: 1779641618,
    content: [
      '最终编译成功、应该以它为准的是：',
      '',
      '`/home/zy/os/termination_framework`',
      '',
      '验证命令是：',
      '',
      '```bash',
      'isabelle build -c -D /home/zy/os/termination_framework',
      '```',
      '',
      '结果已经成功通过。',
      '',
      '补充：隐藏目录 `/home/zy/os/.codex-bridge/orchestrations/orc_abd603dc6f82ab5f2a90c16f415979a3/termination_framework` 也曾经构建成功，但那是 orchestration 运行中的中间副本。用户可见、最终应查看和提交的是 `/home/zy/os/termination_framework`。',
      '',
      'Msg: to=user; intent=final; need=none',
      'Handoff: status=resolved; changed=none; verified=isabelle build -c -D /home/zy/os/termination_framework; next=none; risks=proof framework still contains sorry placeholders',
    ].join('\n'),
  },
  {
    id: 'snapshot-tool-files',
    type: 'tool',
    title: '命令',
    kind: 'command.end',
    command: '/bin/bash -lc "find /home/zy/os -maxdepth 3 -type f \\( -name ROOT -o -name Termination.thy -o -name Model.thy \\) | sort"',
    output: [
      '/home/zy/os/termination_framework/Model.thy',
      '/home/zy/os/termination_framework/ROOT',
      '/home/zy/os/termination_framework/Termination.thy',
      '',
    ].join('\n'),
    createdAt: 1779641636,
    status: 'completed',
    defaultOpen: false,
  },
  {
    id: 'snapshot-tool-risks',
    type: 'tool',
    title: '命令',
    kind: 'command.end',
    command: '/bin/bash -lc \'rg -n "termination modify_lin|sorry|modify_lin_.*decreases|quick_and_dirty" /home/zy/os/termination_framework\'',
    output: [
      '/home/zy/os/termination_framework/Termination.thy:24:lemma modify_lin_last_enq_branch_decreases:',
      '/home/zy/os/termination_framework/Termination.thy:38:  sorry',
      '/home/zy/os/termination_framework/Termination.thy:40:lemma modify_lin_o1_before_bt_branch_decreases:',
      '/home/zy/os/termination_framework/Termination.thy:57:  sorry',
      '/home/zy/os/termination_framework/Termination.thy:59:lemma modify_lin_b_before_o1_branch_decreases:',
      '/home/zy/os/termination_framework/Termination.thy:76:  sorry',
      '/home/zy/os/termination_framework/Termination.thy:78:lemma modify_lin_unordered_branch_decreases:',
      '/home/zy/os/termination_framework/Termination.thy:96:  sorry',
      '/home/zy/os/termination_framework/Termination.thy:98:termination modify_lin',
      '/home/zy/os/termination_framework/Termination.thy:102:  sorry',
      '/home/zy/os/termination_framework/ROOT:2:  options [document = false, quick_and_dirty = true]',
      '',
    ].join('\n'),
    createdAt: 1779641636,
    status: 'completed',
    defaultOpen: false,
  },
  {
    id: 'snapshot-tool-build',
    type: 'tool',
    title: '命令',
    kind: 'command.end',
    command: "/bin/bash -lc 'isabelle build -c -D /home/zy/os/termination_framework'",
    output: [
      'Cleaned Termination_Framework',
      'Running Termination_Framework ...',
      'Finished Termination_Framework (0:00:06 elapsed time, 0:00:06 cpu time, factor 1.04)',
      '0:00:10 elapsed time, 0:00:06 cpu time, factor 0.60',
      '',
    ].join('\n'),
    createdAt: 1779641653,
    status: 'completed',
    defaultOpen: true,
  },
  {
    id: 'snapshot-assistant-final',
    type: 'message',
    role: 'assistant',
    createdAt: 1779641669,
    content: [
      '最终成功构建的是：',
      '',
      '`/home/zy/os/termination_framework`',
      '',
      '我刚重新验证了这个目录：',
      '',
      '```bash',
      'isabelle build -c -D /home/zy/os/termination_framework',
      '```',
      '',
      '结果 exit 0，`Termination_Framework` 构建通过。',
      '',
      '注意：这表示“证明框架可编译通过”，不是完整证明已经完成。当前 `Termination.thy` 里仍有 5 个 `sorry`，并且 `ROOT` 使用了 `quick_and_dirty = true` 来允许带 `sorry` 的批量构建。',
      '',
      'Msg: to=user; intent=final; need=none',
      'Handoff: status=resolved; changed=none; verified=isabelle build -c -D /home/zy/os/termination_framework; next=prove remaining sorry placeholders; risks=framework passes with quick_and_dirty but termination proof is not complete',
    ].join('\n'),
  },
  {
    id: 'snapshot-tool-glob',
    type: 'tool',
    title: '命令',
    kind: 'command.end',
    command: 'Glob termination_framework/**',
    output: [
      'termination_framework/Model.thy',
      'termination_framework/Termination.thy',
      'termination_framework/ROOT',
    ].join('\n'),
    createdAt: 1779641678,
    status: 'completed',
    defaultOpen: false,
  },
  {
    id: 'snapshot-assistant-verifier',
    type: 'message',
    role: 'assistant',
    createdAt: 1779641684,
    content: [
      '最终成功编译的是：',
      '',
      '`/home/zy/os/termination_framework`',
      '',
      '里面关键文件是：',
      '',
      '- `/home/zy/os/termination_framework/Termination.thy`',
      '- `/home/zy/os/termination_framework/Model.thy`',
      '- `/home/zy/os/termination_framework/ROOT`',
      '',
      '已报告通过的命令是：',
      '',
      '```bash',
      'isabelle build -c -D /home/zy/os/termination_framework',
      '```',
      '',
      '注意：这是**可编译的证明框架**，不是完全无 `sorry` 的最终证明；风险仍是 `Termination.thy` 中还保留了若干 `sorry` 占位。',
      '',
      'Msg: to=user; intent=final; need=none',
      'Handoff: status=resolved; changed=none; verified=isabelle build -c -D /home/zy/os/termination_framework; next=prove remaining sorry placeholders; risks=proof framework still contains sorry placeholders',
    ].join('\n'),
  },
];
const conversationSnapshotDateLabel = '2026-05-24';

function ConversationSnapshotPage({ t }: { t: UIText }) {
  return (
    <div className="h-screen w-full flex bg-background text-foreground overflow-hidden font-sans">
      <aside className="hidden md:flex w-[260px] flex-col border-r border-sidebar-border bg-sidebar">
        <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
          <div className="flex items-center gap-2 font-medium">
            <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
              <FileText className="h-3.5 w-3.5" />
            </div>
            <span className="text-sm">{t.conversationSnapshot}</span>
          </div>
        </div>

        <div className="p-3 space-y-2">
          <Button variant="ghost" className="w-full justify-start gap-2 h-9 rounded-lg pointer-events-none opacity-60" disabled>
            <ArrowLeft className="h-4 w-4" />
            {t.codexBridge}
          </Button>
          <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm pointer-events-none" disabled>
            <MessageSquare className="h-4 w-4" />
            termination framework
          </Button>
        </div>

        <div className="flex-1 overflow-y-auto px-3 py-2 elegant-scrollbar">
          <div>
            <h4 className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider mb-1.5 px-2">
              {conversationSnapshotDateLabel}
            </h4>
            <div className="w-full text-left px-2 py-1.5 rounded-md text-sm flex items-center gap-2 bg-sidebar-accent text-sidebar-accent-foreground font-medium">
              <MessageSquare className="h-3.5 w-3.5 opacity-70 shrink-0" />
              <span className="truncate">termination framework</span>
            </div>
          </div>
        </div>

        <div className="p-3 border-t border-sidebar-border shrink-0 mt-auto bg-sidebar">
          <div className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-sidebar-foreground">
            <div className="h-6 w-6 rounded-full bg-sidebar-primary/10 flex items-center justify-center">
              <Check className="h-3.5 w-3.5" />
            </div>
            <span className="flex-1 text-left">{t.readOnlySnapshot}</span>
            <div className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
          </div>
        </div>
      </aside>

      <main className="flex-1 flex flex-col min-w-0 h-full">
        <header className="h-14 shrink-0 border-b border-border flex items-center justify-between px-3 md:px-4 bg-background z-10">
          <div className="flex items-center gap-2 min-w-0">
            <Button variant="ghost" size="icon" className="text-muted-foreground pointer-events-none opacity-60" disabled>
              <ArrowLeft className="h-5 w-5" />
            </Button>
            <div className="h-4 w-px bg-border mx-1 hidden md:block" />
            <FileText className="h-4 w-4 text-muted-foreground shrink-0" />
            <span className="text-sm font-medium truncate">termination framework</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden sm:inline-flex rounded-full border border-border bg-muted/40 px-2.5 py-1 text-xs text-muted-foreground">
              {t.readOnlySnapshot}
            </span>
          </div>
        </header>

        <div className="bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          <div className="flex items-center gap-1.5">
            <Server className="h-3.5 w-3.5" />
            <span>{t.runner}: orchestration snapshot</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            <span>{t.thread}: /home/zy/os/termination_framework</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Command className="h-3.5 w-3.5" />
            <span>{t.status}: {t.ready}</span>
          </div>
        </div>

        <div className="relative flex-1 min-h-0">
          <div className="h-full overflow-y-auto p-4 md:p-6 space-y-4 elegant-scrollbar">
            {conversationSnapshotItems.map((item) => item.type === 'message'
              ? <SnapshotMessageItem key={item.id} item={item} t={t} />
              : <SnapshotToolBlock key={item.id} item={item} t={t} />
            )}
            <div className="h-4" />
          </div>
        </div>

        <div className="shrink-0 p-4 border-t border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
          <div className="max-w-4xl mx-auto flex flex-col bg-card border border-border rounded-xl shadow-sm">
            <textarea
              className="w-full bg-transparent border-0 resize-none p-3 text-sm focus:outline-none focus:ring-0 min-h-[60px] max-h-[120px] text-muted-foreground"
              value=""
              placeholder={t.askCodex}
              disabled
              readOnly
            />
            <div className="flex items-center justify-between p-2 pt-0">
              <div className="flex items-center gap-1">
                <Button variant="ghost" size="icon" type="button" className="h-8 w-8 text-muted-foreground rounded-lg" disabled>
                  <ImagePlus className="h-4 w-4" />
                </Button>
              </div>
              <Button size="sm" type="button" className="h-8 px-3 rounded-lg gap-1.5 text-xs font-medium" disabled>
                {t.send}
                <Send className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
          <div className="text-center mt-2">
            <span className="text-[10px] text-muted-foreground/60 font-medium">{t.verifyNotice}</span>
          </div>
        </div>
      </main>
    </div>
  );
}

function SnapshotMessageItem({ item, t }: { item: Extract<SnapshotItem, { type: 'message' }>; t: UIText }) {
  const msg: Extract<ChatItem, { type: 'message' }> = {
    id: item.id,
    type: 'message',
    role: item.role,
    content: item.content,
    createdAt: item.createdAt,
  };
  return <MessageItem msg={msg} t={t} readOnly />;
}

function SnapshotToolBlock({ item, t }: { item: Extract<SnapshotItem, { type: 'tool' }>; t: UIText }) {
  return (
    <details className="w-full max-w-4xl mx-auto mt-2 bg-muted/30 border border-border rounded-lg overflow-hidden text-[13px] group/tool" open={item.defaultOpen}>
      <summary className="flex cursor-pointer items-center gap-2 px-3 py-1.5 bg-muted/50 border-b border-border marker:content-none">
        <Terminal className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="font-medium text-xs">{item.title}</span>
        {item.kind && <span className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">{item.kind}</span>}
        <span className="ml-auto text-xs text-muted-foreground font-mono truncate max-w-[260px]">{item.command}</span>
        {item.status && <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">{item.status}</span>}
        <span className="shrink-0 text-[10px] text-muted-foreground">{formatTime(item.createdAt)}</span>
        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground opacity-50 transition-transform group-open/tool:rotate-180" />
      </summary>
      <div className="grid gap-2 p-3 bg-background/50">
        <pre className="overflow-x-auto whitespace-pre-wrap rounded-md border border-border bg-muted/40 p-3 font-mono text-[11px] leading-relaxed text-foreground/80 elegant-scrollbar">
          {item.command}
        </pre>
        <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-md border border-border bg-muted/20 p-3 font-mono text-[11px] leading-relaxed text-muted-foreground elegant-scrollbar">
          {item.output}
        </pre>
      </div>
    </details>
  );
}

function MessageItem({ msg, t, readOnly = false }: { msg: Extract<ChatItem, { type: 'message' }>, t: UIText, readOnly?: boolean }) {
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

function ToolItem({ tool, t }: { tool: ToolEvent, t: UIText }) {
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

function SettingsModal({
  user,
  agents,
  selectedAgentId,
  onSelectAgent,
  onAgentsChanged,
  onLogout,
  isDarkMode,
  setIsDarkMode,
  language,
  setLanguage,
  t,
  initialFocus,
  close,
}: {
  user: UserAccount;
  agents: Agent[];
  selectedAgentId: string;
  onSelectAgent: (agentId: string) => void;
  onAgentsChanged: () => Promise<void>;
  onLogout: () => void;
  isDarkMode: boolean;
  setIsDarkMode: (value: boolean) => void;
  language: Language;
  setLanguage: (value: Language) => void;
  t: UIText;
  initialFocus: 'cli' | '';
  close: () => void;
}) {
  const [label, setLabel] = useState('');
  const [permissionProfile, setPermissionProfile] = useState<PermissionProfileId>('review-required');
  const [tokenInfo, setTokenInfo] = useState<BridgeTokenResponse | null>(null);
  const [tokenError, setTokenError] = useState('');
  const [generatingToken, setGeneratingToken] = useState(false);
  const [deletingAgentId, setDeletingAgentId] = useState('');
  const [expandedAgentId, setExpandedAgentId] = useState(selectedAgentId || '');
  const [repairingAgentId, setRepairingAgentId] = useState('');
  const [repairTokens, setRepairTokens] = useState<Record<string, BridgeTokenResponse>>({});
  const [repairErrorByAgent, setRepairErrorByAgent] = useState<Record<string, string>>({});
  const [copiedCommand, setCopiedCommand] = useState('');
  const cliSectionRef = useRef<HTMLDivElement | null>(null);
  const generateToken = async () => {
    setGeneratingToken(true);
    setTokenError('');
    try {
      const data = await api<BridgeTokenResponse>('/api/bridge-tokens', {
        method: 'POST',
        body: JSON.stringify({ label: label.trim() || 'wsl2-cli', permissionProfile }),
      });
      setTokenInfo(data);
      await onAgentsChanged();
    } catch (err) {
      setTokenError(err instanceof Error ? err.message : t.failedCreateBridgeToken);
    } finally {
      setGeneratingToken(false);
    }
  };
  const permissionOptions: Array<{ id: PermissionProfileId; title: string; description: string }> = [
    { id: 'review-required', title: t.reviewRequired, description: t.reviewRequiredDescription },
    { id: 'auto-execute', title: t.autoExecute, description: t.autoExecuteDescription },
  ];
  const profileCommand = (profileId: PermissionProfileId) =>
    tokenInfo?.permissionProfiles?.find((profile) => profile.id === profileId)?.setupCommand || '';
  const profileConnectCommand = (profileId: PermissionProfileId) =>
    tokenInfo?.permissionProfiles?.find((profile) => profile.id === profileId)?.connectCommand || '';
  const profileSudoConnectCommand = (profileId: PermissionProfileId) =>
    tokenInfo?.permissionProfiles?.find((profile) => profile.id === profileId)?.sudoConnectCommand || '';
  const selectedSetupCommand =
    (tokenInfo && profileCommand(tokenInfo.permissionProfile)) ||
    tokenInfo?.setupCommand ||
    tokenInfo?.commands?.[0] ||
    (tokenInfo ? `${tokenInfo.installCommand} && ${tokenInfo.connectCommand}` : '');
  const installCommand = tokenInfo?.installCommand || tokenInfo?.commands?.[0] || '';
  const sudoInstallCommand = tokenInfo?.sudoInstallCommand || tokenInfo?.sudoCommands?.[0] || '';
  const selectedLinkCommand =
    (tokenInfo && profileConnectCommand(tokenInfo.permissionProfile)) ||
    tokenInfo?.connectCommand ||
    tokenInfo?.commands?.[1] ||
    selectedSetupCommand;
  const selectedSudoLinkCommand =
    (tokenInfo && profileSudoConnectCommand(tokenInfo.permissionProfile)) ||
    tokenInfo?.sudoConnectCommand ||
    tokenInfo?.sudoCommands?.[1] ||
    '';
  const alternateProfile = tokenInfo?.permissionProfile === 'auto-execute' ? 'review-required' : 'auto-execute';
  const alternateSetupCommand = tokenInfo ? profileConnectCommand(alternateProfile) || profileCommand(alternateProfile) : '';
  const alternateSudoCommand = tokenInfo ? profileSudoConnectCommand(alternateProfile) : '';
  const repairProfileCommand = (info: BridgeTokenResponse | undefined, profileId: PermissionProfileId) =>
    info?.permissionProfiles?.find((profile) => profile.id === profileId)?.setupCommand || '';
  const repairProfileConnectCommand = (info: BridgeTokenResponse | undefined, profileId: PermissionProfileId) =>
    info?.permissionProfiles?.find((profile) => profile.id === profileId)?.connectCommand || '';
  const repairProfileSudoConnectCommand = (info: BridgeTokenResponse | undefined, profileId: PermissionProfileId) =>
    info?.permissionProfiles?.find((profile) => profile.id === profileId)?.sudoConnectCommand || '';
  const copyCommand = async (value: string, key: string) => {
    await copyText(value);
    setCopiedCommand(key);
    window.setTimeout(() => setCopiedCommand(''), 1200);
  };
  const deleteAgent = async (agent: Agent) => {
    if (!window.confirm(t.deleteCliEndpointConfirm)) return;
    setDeletingAgentId(agent.id);
    setTokenError('');
    try {
      await api(`/api/agents/${encodeURIComponent(agent.id)}`, { method: 'DELETE' });
      if (selectedAgentId === agent.id) {
        localStorage.removeItem('codexBridge.selectedAgentId');
        onSelectAgent('');
      }
      if (expandedAgentId === agent.id) setExpandedAgentId('');
      await onAgentsChanged();
    } catch (err) {
      setTokenError(err instanceof Error ? err.message : t.failedDeleteAgent);
    } finally {
      setDeletingAgentId('');
    }
  };
  const generateRepairToken = async (agent: Agent) => {
    setRepairingAgentId(agent.id);
    setRepairErrorByAgent((prev) => ({ ...prev, [agent.id]: '' }));
    try {
      const data = await api<BridgeTokenResponse>(`/api/agents/${encodeURIComponent(agent.id)}/repair-token`, {
        method: 'POST',
        body: JSON.stringify({ permissionProfile: 'review-required' }),
      });
      setRepairTokens((prev) => ({ ...prev, [agent.id]: data }));
      await onAgentsChanged();
    } catch (err) {
      setRepairErrorByAgent((prev) => ({
        ...prev,
        [agent.id]: err instanceof Error ? err.message : t.failedCreateRepairToken,
      }));
    } finally {
      setRepairingAgentId('');
    }
  };

  useEffect(() => {
    if (initialFocus !== 'cli') return;
    const id = window.setTimeout(() => cliSectionRef.current?.scrollIntoView({ block: 'start', behavior: 'smooth' }), 0);
    return () => window.clearTimeout(id);
  }, [initialFocus]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm animate-in fade-in">
      <div className="bg-card w-full max-w-md rounded-xl border border-border shadow-lg flex flex-col overflow-hidden animate-in zoom-in-95">
        <div className="px-4 py-3 border-b border-border flex items-center justify-between bg-muted/30">
          <h2 className="font-medium">{t.settings}</h2>
          <Button variant="ghost" size="icon" className="h-7 w-7 rounded-md" onClick={close}>
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="p-4 space-y-6 overflow-y-auto max-h-[70vh] elegant-scrollbar">
          <div className="space-y-3">
            <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.account}</h3>
            <div className="flex items-center justify-between p-3 rounded-lg border border-border bg-muted/20">
              <div className="flex items-center gap-3">
                <div className="h-9 w-9 rounded-full bg-primary/10 flex items-center justify-center text-primary font-medium">
                  {initials(user.username)}
                </div>
                <div>
                  <div className="text-sm font-medium">{user.username}</div>
                  <div className="text-xs text-muted-foreground">{t.localAdministrator}</div>
                </div>
              </div>
              <Button variant="ghost" size="sm" className="h-8 text-destructive hover:bg-destructive/10 hover:text-destructive" onClick={onLogout}>
                <LogOut className="h-4 w-4 mr-1.5" />
                {t.logout}
              </Button>
            </div>
          </div>

          <div className="space-y-3">
            <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.appearance}</h3>
            <div className="space-y-2">
              <div className="flex items-center justify-between py-2">
                <span className="text-sm">{t.theme}</span>
                <div className="flex items-center gap-1 bg-muted p-1 rounded-lg border border-border/50">
                  <button
                    className={cn("px-2.5 py-1 text-xs rounded-md font-medium transition-colors", !isDarkMode ? "bg-background shadow-sm border border-border/50" : "text-muted-foreground hover:text-foreground")}
                    onClick={() => setIsDarkMode(false)}
                  >
                    {t.light}
                  </button>
                  <button
                    className={cn("px-2.5 py-1 text-xs rounded-md font-medium transition-colors", isDarkMode ? "bg-background shadow-sm border border-border/50" : "text-muted-foreground hover:text-foreground")}
                    onClick={() => setIsDarkMode(true)}
                  >
                    {t.dark}
                  </button>
                </div>
              </div>
              <div className="flex items-center justify-between py-2">
                <span className="text-sm">{t.language}</span>
                <div className="flex items-center gap-1 bg-muted p-1 rounded-lg border border-border/50">
                  <button
                    className={cn("px-2.5 py-1 text-xs rounded-md font-medium transition-colors", language === 'en' ? "bg-background shadow-sm border border-border/50" : "text-muted-foreground hover:text-foreground")}
                    onClick={() => setLanguage('en')}
                  >
                    {t.english}
                  </button>
                  <button
                    className={cn("px-2.5 py-1 text-xs rounded-md font-medium transition-colors", language === 'zh' ? "bg-background shadow-sm border border-border/50" : "text-muted-foreground hover:text-foreground")}
                    onClick={() => setLanguage('zh')}
                  >
                    {t.chinese}
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div className="space-y-3">
            <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.agentsRuntime}</h3>
            <div className="space-y-2">
              {agents.length ? agents.map((agent) => {
                const expanded = expandedAgentId === agent.id;
                const repairInfo = repairTokens[agent.id];
                const selectedRepairCommand =
                  (repairInfo && repairProfileConnectCommand(repairInfo, repairInfo.permissionProfile)) ||
                  repairInfo?.connectCommand ||
                  repairInfo?.commands?.[1] ||
                  repairProfileCommand(repairInfo, repairInfo?.permissionProfile || 'review-required') ||
                  '';
                const selectedRepairSudoCommand =
                  (repairInfo && repairProfileSudoConnectCommand(repairInfo, repairInfo.permissionProfile)) ||
                  repairInfo?.sudoConnectCommand ||
                  repairInfo?.sudoCommands?.[1] ||
                  '';
                const alternateRepairProfile = repairInfo?.permissionProfile === 'auto-execute' ? 'review-required' : 'auto-execute';
                const alternateRepairCommand = repairInfo ? repairProfileConnectCommand(repairInfo, alternateRepairProfile) || repairProfileCommand(repairInfo, alternateRepairProfile) : '';
                const alternateRepairSudoCommand = repairInfo ? repairProfileSudoConnectCommand(repairInfo, alternateRepairProfile) : '';
                return (
                  <div
                    key={agent.id}
                    className={cn(
                      "rounded-lg border bg-muted/20 transition-colors",
                      selectedAgentId === agent.id ? "border-primary/40 bg-primary/5" : "border-border"
                    )}
                  >
                    <button
                      type="button"
                      onClick={() => {
                        onSelectAgent(agent.id);
                        setExpandedAgentId(expanded ? '' : agent.id);
                      }}
                      className="w-full flex cursor-pointer items-center justify-between gap-2 p-2.5 text-left"
                    >
                      <span className="flex flex-col min-w-0 flex-1 text-left">
                        <span className="text-sm font-medium truncate">{agent.name}</span>
                        <span className="text-xs text-muted-foreground font-mono mt-0.5 truncate">{agent.hostname || agent.machineId}</span>
                        <span className="mt-1 text-[10px] text-muted-foreground truncate">
                          {t.browserApproval}: {orchestrationApprovalMode(agent) === 'auto-execute' ? t.autoExecute : orchestrationCapabilityProblems(agent, t).length ? t.notAvailable : t.available}
                        </span>
                      </span>
                      <div className="flex items-center gap-2 shrink-0">
                        {selectedAgentId === agent.id && <Check className="h-3.5 w-3.5 text-primary" />}
                        <div className={cn(
                          "px-2 py-0.5 rounded-full text-[10px] font-medium border uppercase tracking-wide",
                          agent.online
                            ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-400"
                            : "bg-muted text-muted-foreground border-border"
                        )}>
                          {agent.online ? t.online : t.offline}
                        </div>
                        <ChevronDown className={cn("h-3.5 w-3.5 text-muted-foreground transition-transform", expanded && "rotate-180")} />
                      </div>
                    </button>
                    {expanded && (
                      <div className="space-y-3 border-t border-border px-2.5 py-3">
                        <div className="grid gap-1.5 text-xs text-muted-foreground">
                          <div className="flex items-center justify-between gap-2">
                            <span>{t.machineId}</span>
                            <span className="truncate font-mono">{agent.machineId}</span>
                          </div>
                          <div className="flex items-center justify-between gap-2">
                            <span>{t.runner}</span>
                            <span className="truncate font-mono">{agent.capabilities?.runner || t.notAvailable}</span>
                          </div>
                          <div className="flex items-center justify-between gap-2">
                            <span>{t.workingDirectory}</span>
                            <span className="truncate font-mono">{agent.workingDirs?.[0] || t.noWorkingDirs}</span>
                          </div>
                        </div>
                        {!agent.capabilities && (
                          <div className="flex items-start gap-2 rounded-md border border-amber-500/20 bg-amber-500/10 px-2.5 py-2 text-xs text-amber-700 dark:text-amber-300">
                            <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                            <span>{t.noCapabilitiesReported}</span>
                          </div>
                        )}
                        {agent.capabilities && <CapabilityMatrix agent={agent} t={t} />}
                        <div className="flex items-center justify-between gap-2">
                          <Button
                            size="sm"
                            variant="secondary"
                            className="h-8 gap-1.5"
                            onClick={() => generateRepairToken(agent)}
                            disabled={repairingAgentId === agent.id}
                          >
                            {repairingAgentId === agent.id ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : <Wrench className="h-3.5 w-3.5" />}
                            {repairingAgentId === agent.id ? t.generating : t.generateRepairCommand}
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            type="button"
                            className="h-8 w-8 rounded-md text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                            onClick={() => deleteAgent(agent)}
                            disabled={deletingAgentId === agent.id}
                            aria-label={t.deleteCliEndpoint}
                            title={t.deleteCliEndpoint}
                          >
                            {deletingAgentId === agent.id ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
                          </Button>
                        </div>
                        {repairErrorByAgent[agent.id] && (
                          <div className="flex items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                            <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                            <span>{repairErrorByAgent[agent.id]}</span>
                          </div>
                        )}
                        {repairInfo && (
                          <div className="space-y-2">
                            <p className="text-xs leading-relaxed text-muted-foreground">{t.repairCommandHint}</p>
                            <div className="space-y-2">
                              <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{t.normalUserCommands}</div>
                              <CommandBlock
                                label={`${t.repairConnectionCommand} · ${t.selectedProfileCommand}`}
                                value={selectedRepairCommand}
                                copied={copiedCommand === `repair-${agent.id}`}
                                onCopy={() => copyCommand(selectedRepairCommand, `repair-${agent.id}`).catch(() => undefined)}
                                t={t}
                              />
                              {alternateRepairCommand && (
                                <CommandBlock
                                  label={`${t.repairConnectionCommand} · ${t.alternateProfileCommand}`}
                                  value={alternateRepairCommand}
                                  copied={copiedCommand === `repair-alt-${agent.id}`}
                                  onCopy={() => copyCommand(alternateRepairCommand, `repair-alt-${agent.id}`).catch(() => undefined)}
                                  t={t}
                                />
                              )}
                            </div>
                            {selectedRepairSudoCommand && (
                              <div className="space-y-2">
                                <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{t.sudoRootCommands}</div>
                                <CommandBlock
                                  label={`${t.repairConnectionCommand} · ${t.selectedProfileCommand}`}
                                  value={selectedRepairSudoCommand}
                                  copied={copiedCommand === `repair-sudo-${agent.id}`}
                                  onCopy={() => copyCommand(selectedRepairSudoCommand, `repair-sudo-${agent.id}`).catch(() => undefined)}
                                  t={t}
                                />
                                {alternateRepairSudoCommand && (
                                  <CommandBlock
                                    label={`${t.repairConnectionCommand} · ${t.alternateProfileCommand}`}
                                    value={alternateRepairSudoCommand}
                                    copied={copiedCommand === `repair-sudo-alt-${agent.id}`}
                                    onCopy={() => copyCommand(alternateRepairSudoCommand, `repair-sudo-alt-${agent.id}`).catch(() => undefined)}
                                    t={t}
                                  />
                                )}
                              </div>
                            )}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                );
              }) : (
                <div className="text-sm text-muted-foreground p-2.5 rounded-lg border border-border bg-muted/20">{t.noAgentsEnrolled}</div>
              )}
            </div>
            <div ref={cliSectionRef} className="rounded-lg border border-border bg-muted/20 p-3 space-y-3">
              <div className="flex items-center justify-between gap-2">
                <div className="min-w-0">
                  <div className="text-sm font-medium">{t.addCliEndpoint}</div>
                  <div className="text-xs text-muted-foreground">{t.expiresIn24h}</div>
                </div>
                <Button size="sm" className="h-8 gap-1.5 shrink-0" onClick={() => generateToken()} disabled={generatingToken}>
                  {generatingToken ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
                  {generatingToken ? t.generating : t.add}
                </Button>
              </div>
              <label className="space-y-1.5 block">
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.endpointLabel}</span>
                <Input value={label} onChange={(event) => setLabel(event.target.value)} placeholder="wsl2-cli" className="h-8 bg-background/60" />
              </label>
              <div className="space-y-2">
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.permissionProfile}</span>
                <div className="grid gap-2">
                  {permissionOptions.map((option) => (
                    <button
                      key={option.id}
                      type="button"
                      onClick={() => setPermissionProfile(option.id)}
                      className={cn(
                        "w-full rounded-lg border p-2.5 text-left transition-colors",
                        permissionProfile === option.id ? "border-primary/50 bg-primary/5" : "border-border bg-background/50 hover:bg-muted/40"
                      )}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="text-sm font-medium">{option.title}</span>
                        {permissionProfile === option.id && <Check className="h-3.5 w-3.5 text-primary" />}
                      </div>
                      <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{option.description}</p>
                    </button>
                  ))}
                </div>
              </div>
              {tokenError && (
                <div className="flex items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{tokenError}</span>
                </div>
              )}
              {tokenInfo && (
                <div className="space-y-2">
                  <CommandBlock
                    label={t.enrollToken}
                    value={tokenInfo.token}
                    copied={copiedCommand === 'token'}
                    onCopy={() => copyCommand(tokenInfo.token, 'token').catch(() => undefined)}
                    t={t}
                  />
                  <div className="space-y-2">
                    <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{t.normalUserCommands}</div>
                    <CommandBlock
                      label={t.installCommand}
                      value={installCommand}
                      copied={copiedCommand === 'install'}
                      onCopy={() => copyCommand(installCommand, 'install').catch(() => undefined)}
                      t={t}
                    />
                    <CommandBlock
                      label={`${t.linkCommand} · ${t.selectedProfileCommand}`}
                      value={selectedLinkCommand}
                      copied={copiedCommand === 'link'}
                      onCopy={() => copyCommand(selectedLinkCommand, 'link').catch(() => undefined)}
                      t={t}
                    />
                    {alternateSetupCommand && (
                      <CommandBlock
                        label={`${t.linkCommand} · ${t.alternateProfileCommand}`}
                        value={alternateSetupCommand}
                        copied={copiedCommand === 'link-alt'}
                        onCopy={() => copyCommand(alternateSetupCommand, 'link-alt').catch(() => undefined)}
                        t={t}
                      />
                    )}
                  </div>
                  {sudoInstallCommand && selectedSudoLinkCommand && (
                    <div className="space-y-2">
                      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{t.sudoRootCommands}</div>
                      <CommandBlock
                        label={t.installCommand}
                        value={sudoInstallCommand}
                        copied={copiedCommand === 'sudo-install'}
                        onCopy={() => copyCommand(sudoInstallCommand, 'sudo-install').catch(() => undefined)}
                        t={t}
                      />
                      <CommandBlock
                        label={`${t.linkCommand} · ${t.selectedProfileCommand}`}
                        value={selectedSudoLinkCommand}
                        copied={copiedCommand === 'sudo-link'}
                        onCopy={() => copyCommand(selectedSudoLinkCommand, 'sudo-link').catch(() => undefined)}
                        t={t}
                      />
                      {alternateSudoCommand && (
                        <CommandBlock
                          label={`${t.linkCommand} · ${t.alternateProfileCommand}`}
                          value={alternateSudoCommand}
                          copied={copiedCommand === 'sudo-link-alt'}
                          onCopy={() => copyCommand(alternateSudoCommand, 'sudo-link-alt').catch(() => undefined)}
                          t={t}
                        />
                      )}
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="p-4 border-t border-border flex justify-end gap-2 bg-muted/30">
          <Button variant="ghost" size="sm" onClick={close}>{t.cancel}</Button>
          <Button size="sm" onClick={close}>{t.savePreferences}</Button>
        </div>
      </div>
    </div>
  );
}

function ApprovalCard({
  item,
  t,
  onDecision,
}: {
  item: { approval: ApprovalRequest; status?: ApprovalStatus };
  t: UIText;
  onDecision: (requestId: string, decision: 'accept' | 'decline' | 'cancel') => void;
}) {
  const pending = !item.status || item.status === 'pending';
  const statusText =
    item.status === 'accepted' ? t.approved :
      item.status === 'declined' ? t.denied :
        item.status === 'canceled' ? t.approvalCanceled :
          t.approvalRequired;
  const approvalTitle = item.approval.kind === 'ccb.terminal_prompt' ? t.browserApproval : t.approvalRequired;
  const detail = [item.approval.command, item.approval.cwd, item.approval.reason].filter(Boolean).join('\n');

  return (
    <div className="w-full max-w-4xl mx-auto rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-3">
      <div className="flex items-start gap-3">
        <div className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300">
          <ShieldQuestion className="h-4 w-4" />
        </div>
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">{approvalTitle}</span>
            <span className="rounded border border-border bg-background/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">{statusText}</span>
          </div>
          {detail && (
            <pre className="max-h-32 overflow-x-auto whitespace-pre-wrap rounded-md border border-border bg-background/70 p-2 font-mono text-[11px] text-muted-foreground elegant-scrollbar">
              {detail}
            </pre>
          )}
          {pending && (
            <div className="flex gap-2">
              <Button size="sm" type="button" className="h-8" onClick={() => onDecision(item.approval.requestId, 'accept')}>
                <Check className="mr-1.5 h-3.5 w-3.5" />
                {t.approve}
              </Button>
              <Button variant="secondary" size="sm" type="button" className="h-8" onClick={() => onDecision(item.approval.requestId, 'decline')}>
                <X className="mr-1.5 h-3.5 w-3.5" />
                {t.deny}
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function CommandBlock({
  label,
  value,
  copied,
  onCopy,
  t,
}: {
  label: string;
  value: string;
  copied: boolean;
  onCopy: () => void;
  t: UIText;
}) {
  return (
    <div className="overflow-hidden rounded-md border border-border bg-background/70">
      <div className="flex items-center gap-2 border-b border-border bg-muted/40 px-2 py-1.5">
        <Terminal className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-xs font-medium">{label}</span>
        <Button variant="ghost" size="icon" type="button" className="ml-auto h-6 w-6 rounded-md text-muted-foreground" onClick={onCopy} aria-label={t.copy} title={copied ? t.copied : t.copy}>
          {copied ? <Check className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" /> : <Clipboard className="h-3.5 w-3.5" />}
        </Button>
      </div>
      <pre className="max-h-28 overflow-x-auto whitespace-pre-wrap p-2 font-mono text-[11px] leading-relaxed text-muted-foreground elegant-scrollbar">{value}</pre>
    </div>
  );
}

function RenameSessionModal({
  title,
  error,
  saving,
  onChange,
  onClose,
  onSave,
  t,
}: {
  title: string;
  error: string;
  saving: boolean;
  onChange: (value: string) => void;
  onClose: () => void;
  onSave: () => void;
  t: UIText;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    const id = window.setTimeout(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    }, 0);
    return () => window.clearTimeout(id);
  }, []);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm animate-in fade-in"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
      onKeyDown={(event) => {
        if (event.key === 'Escape') onClose();
      }}
    >
      <form
        className="bg-card w-full max-w-sm rounded-xl border border-border shadow-lg flex flex-col overflow-hidden animate-in zoom-in-95"
        onSubmit={(event) => {
          event.preventDefault();
          onSave();
        }}
      >
        <div className="px-4 py-3 border-b border-border flex items-center justify-between bg-muted/30">
          <div className="flex items-center gap-2">
            <div className="h-7 w-7 rounded-md bg-primary/10 text-primary flex items-center justify-center">
              <Edit2 className="h-3.5 w-3.5" />
            </div>
            <h2 className="font-medium">{t.renameSession}</h2>
          </div>
          <Button variant="ghost" size="icon" type="button" className="h-7 w-7 rounded-md" onClick={onClose} disabled={saving}>
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="p-4 space-y-3">
          <label className="space-y-1.5 block">
            <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.sessionName}</span>
            <Input
              ref={inputRef}
              value={title}
              onChange={(event) => onChange(event.target.value)}
              maxLength={80}
              disabled={saving}
              className="h-10 bg-background border-border"
            />
          </label>

          {error && (
            <div className="flex items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{error}</span>
            </div>
          )}
        </div>

        <div className="p-4 border-t border-border flex justify-end gap-2 bg-muted/30">
          <Button variant="ghost" size="sm" type="button" onClick={onClose} disabled={saving}>{t.cancel}</Button>
          <Button size="sm" type="submit" disabled={saving || !title.trim()}>
            {saving ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : t.save}
          </Button>
        </div>
      </form>
    </div>
  );
}
