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

export type OrchestrationRun = {
  id: string;
  agentId: string;
  title: string;
  mode: 'collaboration' | 'debate';
  firstCli?: 'claude' | 'codex';
  profile?: 'default' | 'formal-proof';
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
  runEndData?: RunEndData;
  bridgeNoteData?: BridgeNoteData;
  runConclusion?: RunConclusion;
  data?: Record<string, any>;
  createdAt?: number;
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
  firstCli?: string;
  maxTurnsRequested?: number;
  maxTurnsApplied?: number;
  promptSeq?: number;
  profile?: string;
};

export type TurnStartData = {
  cli?: string;
  turn?: number;
  maxTurns?: number;
  promptText?: string;
  profile?: string;
  resumeMode?: string;
};

export type RunEndData = {
  codexThreadId?: string;
  claudeSessionId?: string;
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

export type OrchestrationVisibleEvent =
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
  firstCli?: 'claude' | 'codex';
  profile?: 'default' | 'formal-proof';
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
