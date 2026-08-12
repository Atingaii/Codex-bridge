export type UserAccount = {
  id: string;
  username: string;
  createdAt: number;
  isAdmin?: boolean;
};

export type Agent = {
  id: string;
  userId?: string;
  name: string;
  machineId: string;
  hostname: string;
  instance?: string;
  workingDirs?: string[];
  lastSeenAt: number;
  online: boolean;
  version?: string;
  connectedAt?: number;
  capabilities?: BridgeCapabilities;
};

export type BridgeCapabilities = {
  runner?: string;
  sandbox?: string;
  approvalPolicy?: string;
  chat?: Record<string, BridgeCLICapability | undefined>;
  orchestration?: Record<string, BridgeCLICapability | undefined>;
  acp?: ACPCapability;
  metadata?: Record<string, string | undefined>;
  durableTaskGraph?: boolean;
	usageLedger?: boolean;
	configSwitcher?: CLIConfigSwitcherCapability;
};

export type CLIConfigSwitcherCapability = {
	version: number;
	publicKey: string;
	keyId: string;
	clis: Array<'codex' | 'claude'>;
};

export type EncryptedSecret = {
	ephemeralPublicKey: string;
	salt: string;
	iv: string;
	ciphertext: string;
};

export type CLIConfigPreset = {
	id: string;
	agentId: string;
	cli: 'codex' | 'claude';
	name: string;
	baseUrl: string;
	model: string;
	keyHint?: string;
	active: boolean;
	createdAt: number;
	updatedAt: number;
};

export type CLIConfigResult = {
	ok: boolean;
	cli: 'codex' | 'claude';
	baseUrl?: string;
	protocol?: string;
	models?: string[];
	modelsListed?: boolean;
	appliedModel?: string;
	message?: string;
};

// Mirrors internal/protocol/envelope.go:ACPCapability.
export type ACPCapability = {
  available?: boolean;
  loadSession?: boolean;
  nativeResume?: boolean;
};

export type BridgeCLICapability = {
  available?: boolean;
  execution?: string;
  browserApproval?: boolean;
  approvalMode?: string;
};

export type Session = {
  id: string;
  agentId: string;
  userId: string;
  title: string;
  remoteThreadId?: string;
  // Native CLI resume id for local takeover (target B). Client-side only.
  nativeResumeId?: string;
  createdAt: number;
  updatedAt: number;
};

export type Message = {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  createdAt: number;
};

export type Run = {
  id: string;
  promptId: string;
  status: string;
};

export type OrchestrationFile = {
  name: string;
  mimeType: string;
  size: number;
};

export type NativeContextCompaction = 'off' | 'after-turn';
export type WorkerPair = 'claude-codex' | 'codex-codex';

export type OrchestrationRun = {
  id: string;
  agentId: string;
  title: string;
  mode: 'collaboration' | 'debate';
  workerPair?: WorkerPair;
  firstCli?: 'claude' | 'codex';
  profile?: 'default' | 'formal-proof';
  nativeContextCompaction?: NativeContextCompaction;
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

export type OrchestrationUsageStats = {
  cli: string;
  model?: string;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  estimatedCostUsd: number;
  estimated: boolean;
  native: boolean;
  costKnown: boolean;
  costSource?: string;
	pricingModel?: string;
  reasoningTokens?: number;
	callCount?: number;
};

export type OrchestrationRoundStats = {
  round: number;
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  callCount: number;
};

export type OrchestrationRunStats = {
  runId: string;
  startedAt?: number;
  finishedAt?: number;
  runtimeSeconds: number;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  estimatedCostUsd: number;
  estimated: boolean;
  native: boolean;
  costKnown: boolean;
  costSource?: string;
	pricingModels?: string[];
  reasoningTokens?: number;
  callCount?: number;
  accountingStatus: 'complete' | 'partial' | 'legacy-incomplete' | string;
  accountingSource: 'local-cli-ledger' | 'turn-snapshots' | string;
  scannedAt?: number;
  byCli: OrchestrationUsageStats[];
  cacheTokens: number;
  totalTokens: number;
  rounds?: OrchestrationRoundStats[];
};

export type OrchestrationUsageOverviewItem = {
  runId: string;
  title: string;
  machineId: string;
  machineName: string;
  hostname?: string;
  createdAt: number;
  status: string;
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  callCount: number;
  costKnown: boolean;
  byCli: OrchestrationUsageStats[];
};

export type OrchestrationUsageTrendPoint = {
  date: string;
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  callCount: number;
  costKnown: boolean;
};

export type OrchestrationUsageOverview = {
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  callCount: number;
  runs: number;
  machines: number;
  days: number;
  timezoneOffset: number;
  costKnown: boolean;
  trend: OrchestrationUsageTrendPoint[];
  items: OrchestrationUsageOverviewItem[];
};

export type AdminUsageTrendPoint = {
  date: string;
  activeUsers: number;
  chatMessages: number;
  orchestrationRuns: number;
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  costKnown: boolean;
  callCount: number;
};

export type AdminUserUsage = {
  userId: string;
  username: string;
  createdAt: number;
  lastActiveAt: number;
  activityStatus: 'online' | 'active' | 'idle' | 'inactive';
  onlineAgents: number;
  totalAgents: number;
  chatSessions: number;
  orchestrationRuns: number;
  runningRuns: number;
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  costKnown: boolean;
  callCount: number;
};

export type AdminUsageOverview = {
  days: number;
  timezoneOffset: number;
  users: number;
  activeUsers: number;
  onlineUsers: number;
  onlineAgents: number;
  totalAgents: number;
  chatSessions: number;
  orchestrationRuns: number;
  runningRuns: number;
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  costKnown: boolean;
  callCount: number;
  trend: AdminUsageTrendPoint[];
  items: AdminUserUsage[];
};

export type AdminConversationUsage = {
  id: string;
  kind: 'chat' | 'orchestration';
  title: string;
  agentName: string;
  status: string;
  mode?: string;
  maxTurns?: number;
  activityCount: number;
  createdAt: number;
  updatedAt: number;
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  costKnown: boolean;
  callCount: number;
};

export type AdminUserUsageDetail = {
  userId: string;
  username: string;
  createdAt: number;
  days: number;
  timezoneOffset: number;
  inputTokens: number;
  outputTokens: number;
  cacheTokens: number;
  totalTokens: number;
  estimatedCostUsd: number;
  costKnown: boolean;
  callCount: number;
  conversations: AdminConversationUsage[];
};

export type AdminConversationContentItem = {
  role?: string;
  source?: string;
  kind: string;
  content: string;
  createdAt: number;
};

export type AdminConversationContent = {
  id: string;
  kind: AdminConversationUsage['kind'];
  title: string;
  prompt?: string;
  items: AdminConversationContentItem[];
};

export type OrchestrationEvent = {
  id?: string;
  runId: string;
  seq?: number;
  timelineOrder?: number;
  kind: string;
  source?: 'cli' | 'bridge' | 'user';
  severity?: 'info' | 'warning' | 'error';
  role?: string;
  cli?: string;
  turnId?: string;
  content?: string;
  status?: string;
  error?: string;
  commandData?: CommandData;
  runStartData?: RunStartData;
  turnStartData?: TurnStartData;
  turnEndData?: TurnEndData;
  runEndData?: RunEndData;
  bridgeNoteData?: BridgeNoteData;
  runConclusion?: RunConclusion;
  task?: TaskAttemptRef;
  data?: Record<string, any>;
  createdAt?: number;
};

export type TaskAttemptRef = {
  graphId: string;
  taskId: string;
  attemptId: string;
  name?: string;
  role?: string;
  workerSlot?: string;
  round?: number;
  maxRounds?: number;
  payloadDigest: string;
};

export type CommandData = {
  id?: string;
  command?: string;
  input?: string;
  output?: string;
  name?: string;
  status?: string;
  exitCode?: number;
  startedAt?: number;
  completedAt?: number;
  durationMs?: number;
  pid?: number;
  pgid?: number;
  willSuppressOnFailure?: boolean;
};

export type RunStartData = {
  cwd?: string;
  mode?: string;
  workerPair?: WorkerPair;
  firstCli?: string;
  maxTurnsRequested?: number;
  maxTurnsApplied?: number;
  round?: number;
  maxRounds?: number;
  promptSeq?: number;
  profile?: string;
  nativeContextCompaction?: NativeContextCompaction;
};

export type TurnStartData = {
  startedAt?: number;
  cli?: string;
  workerSlot?: string;
  turn?: number;
  maxTurns?: number;
  round?: number;
  maxRounds?: number;
  promptText?: string;
  profile?: string;
  resumeMode?: string;
};

export type TurnEndData = {
  startedAt?: number;
  completedAt?: number;
  durationMs?: number;
};

export type RunEndData = {
  codexThreadId?: string;
  codexThreadIds?: Record<string, string>;
  claudeSessionId?: string;
  workerPair?: WorkerPair;
  nativeResume?: NativeResumeInfo[];
  codexNativeResume?: NativeResumeInfo;
  claudeNativeResume?: NativeResumeInfo;
};

export type NativeResumeInfo = {
  cli?: 'codex' | 'claude' | string;
  id?: string;
  command?: string;
  cwd?: string;
  transcriptPath?: string;
  visible?: boolean;
  visibilityReason?: string;
};

export type BridgeNoteData = {
  category?: string;
  command?: string;
  afterSeconds?: number;
  injectedText?: string;
};

export type RunConclusion = {
  outcome: 'satisfied' | 'unsatisfied' | 'blocked' | 'canceled' | 'errored' | string;
  summary: string;
  buildOrAuditCommands?: string[];
  unmetObligations?: string[];
  evidenceRefs?: string[];
};

export type OrchestrationTurnInfo = {
  ordinal?: number;
  total?: number;
  verifier?: boolean;
};

export type ToolEvent = {
  id?: string;
  name?: string;
  command?: string;
  input?: string;
  output?: string;
  status?: string;
  exitCode?: number;
};

export type ApprovalRequest = {
  requestId: string;
  kind: string;
  command?: string;
  cwd?: string;
  reason?: string;
  runId?: string;
  turnId?: string;
  promptId?: string;
};

export type ApprovalStatus = 'pending' | 'accepted' | 'declined' | 'canceled';

export type ChatItem =
  | { id: string; type: 'message'; role: 'user' | 'assistant' | 'system'; content: string; createdAt?: number }
  | { id: string; type: 'tool'; tool: ToolEvent }
  | { id: string; type: 'approval'; approval: ApprovalRequest; status?: ApprovalStatus };

export type ApprovalItemState = {
  id: string;
  approval: ApprovalRequest;
  status?: ApprovalStatus;
  timelineOrder?: number;
  createdAt?: number;
};

export type OrchestrationTimelineItem =
  | { type: 'event'; key: string; event: OrchestrationVisibleEvent; sortIndex: number; timelineOrder?: number; createdAt?: number }
  | { type: 'approval'; key: string; approval: ApprovalItemState; sortIndex: number; timelineOrder?: number; createdAt?: number };

export type OrchestrationTimelineGroup = {
  type: 'turn' | 'standalone';
  key: string;
  runId?: string;
  turnId?: string;
  role?: string;
  cli?: string;
  taskName?: string;
  workerSlot?: string;
  turnInfo?: OrchestrationTurnInfo;
  items: OrchestrationTimelineItem[];
  messageCount: number;
  commandCount: number;
  approvalCount: number;
  statusCount: number;
  createdAt?: number;
  timelineOrder?: number;
  complete: boolean;
  active: boolean;
  incomplete: boolean;
  hasError: boolean;
  durationMs?: number;
  durationLive?: boolean;
};

export type OrchestrationVisibleEvent =
  | {
      type: 'message';
      key: string;
      runId: string;
      kind: string;
      role?: string;
      cli?: string;
      taskName?: string;
      workerSlot?: string;
      turnInfo?: OrchestrationTurnInfo;
      turnId?: string;
      content: string;
      status?: string;
      error?: string;
      createdAt?: number;
      timelineOrder?: number;
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
      taskName?: string;
      workerSlot?: string;
      turnInfo?: OrchestrationTurnInfo;
      turnId?: string;
      content: string;
      status?: string;
      error?: string;
      createdAt?: number;
      timelineOrder?: number;
      command: OrchestrationEvent;
    }
  | {
      type: 'status';
      key: string;
      runId: string;
      kind: string;
      role?: string;
      cli?: string;
      taskName?: string;
      workerSlot?: string;
      turnInfo?: OrchestrationTurnInfo;
      turnId?: string;
      content: string;
      status?: string;
      error?: string;
      createdAt?: number;
      timelineOrder?: number;
    };

export type Envelope = {
  type: string;
  sid?: string;
  payload?: any;
};

export type BridgeTokenResponse = {
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
  commands: string[];
  agentId?: string;
  machineId?: string;
};

export type PermissionProfileId = 'review-required' | 'auto-execute';

export type BridgePermissionProfile = {
  id: PermissionProfileId;
  setupCommand: string;
  connectCommand: string;
};

export type ShareInfo = {
  id: string;
  kind: 'chat' | 'orchestration';
  title?: string;
  url?: string;
  createdAt: number;
  updatedAt: number;
};

export type PublicSession = {
  id: string;
  title?: string;
  createdAt: number;
  updatedAt: number;
};

export type PublicMessage = {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  createdAt: number;
};

export type PublicOrchestrationRun = {
  id: string;
  title: string;
  mode: 'collaboration' | 'debate';
  workerPair?: WorkerPair;
  firstCli?: 'claude' | 'codex';
  profile?: 'default' | 'formal-proof';
  nativeContextCompaction?: NativeContextCompaction;
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

export type PublicSharePayload = {
  share: ShareInfo;
  session?: PublicSession;
  messages?: PublicMessage[];
  run?: PublicOrchestrationRun;
  events?: OrchestrationEvent[];
};

export type ImageAttachment = {
  id: string;
  name: string;
  mimeType: string;
  size: number;
  data: string;
  previewUrl: string;
};

export type UploadAttachment = {
  id: string;
  name: string;
  mimeType: string;
  size: number;
  data: string;
};

export type Language = 'en' | 'zh';
