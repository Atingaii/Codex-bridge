import React, { useEffect, useRef, useState } from 'react';
import { AlertCircle, Check, ChevronDown, Edit2, LogOut, Plus, RefreshCw, Trash2, Wrench, X } from 'lucide-react';
import { api } from '../lib/api';
import type { Agent, BridgeTokenResponse, PermissionProfileId, UserAccount } from '../lib/types';
import type { Language, UIText } from '../lib/i18n';
import { cn, copyText, initials, orchestrationApprovalMode, orchestrationCapabilityProblems } from '../lib/utils';
import { CapabilityMatrix } from './OrchestrationComponents';
import { CommandBlock } from './chat/CommandBlock';
import { Button, Input } from './ui';

export function SettingsModal({
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
  const selectedSetupCommand =
    (tokenInfo && profileCommand(tokenInfo.permissionProfile)) ||
    tokenInfo?.setupCommand ||
    tokenInfo?.commands?.[0] ||
    (tokenInfo ? `${tokenInfo.installCommand} && ${tokenInfo.connectCommand}` : '');
  const installCommand = tokenInfo?.installCommand || tokenInfo?.commands?.[0] || '';
  const selectedLinkCommand =
    (tokenInfo && profileConnectCommand(tokenInfo.permissionProfile)) ||
    tokenInfo?.connectCommand ||
    tokenInfo?.commands?.[1] ||
    selectedSetupCommand;
  const alternateProfile = tokenInfo?.permissionProfile === 'auto-execute' ? 'review-required' : 'auto-execute';
  const alternateSetupCommand = tokenInfo ? profileConnectCommand(alternateProfile) || profileCommand(alternateProfile) : '';
  const repairProfileCommand = (info: BridgeTokenResponse | undefined, profileId: PermissionProfileId) =>
    info?.permissionProfiles?.find((profile) => profile.id === profileId)?.setupCommand || '';
  const repairProfileConnectCommand = (info: BridgeTokenResponse | undefined, profileId: PermissionProfileId) =>
    info?.permissionProfiles?.find((profile) => profile.id === profileId)?.connectCommand || '';
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
                const alternateRepairProfile = repairInfo?.permissionProfile === 'auto-execute' ? 'review-required' : 'auto-execute';
                const alternateRepairCommand = repairInfo ? repairProfileConnectCommand(repairInfo, alternateRepairProfile) || repairProfileCommand(repairInfo, alternateRepairProfile) : '';
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

export function RenameSessionModal({
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
