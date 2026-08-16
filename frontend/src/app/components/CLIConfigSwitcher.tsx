import React, { useEffect, useMemo, useState } from 'react';
import { AlertCircle, Check, KeyRound, Loader2, Pencil, Search, ServerCog, Trash2, X } from 'lucide-react';
import { api } from '../lib/api';
import type { Agent, CLIConfigPreset, CLIConfigResult } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { cn } from '../lib/utils';
import { Button, Input } from './ui';

type CLI = 'codex' | 'claude';

export function CLIConfigSwitcher({ agents, preferredAgentId, t, close, onPresetsChanged }: { agents: Agent[]; preferredAgentId: string; t: UIText; close: () => void; onPresetsChanged: () => Promise<void> | void }) {
	const resetAgents = useMemo(() => agents.filter((agent) => agent.online && agent.capabilities?.configSwitcher), [agents]);
	const [resetAgentId, setResetAgentId] = useState('');
	const [cli, setCLI] = useState<CLI>('codex');
	const [presets, setPresets] = useState<CLIConfigPreset[]>([]);
	const [name, setName] = useState('');
	const [baseUrl, setBaseUrl] = useState('');
	const [apiKey, setAPIKey] = useState('');
	const [model, setModel] = useState('');
	const [reasoningEffort, setReasoningEffort] = useState('');
	const [reasoningLevels, setReasoningLevels] = useState<string[]>([]);
	const [reasoningDefault, setReasoningDefault] = useState('');
	const [models, setModels] = useState<string[]>([]);
	const [modelSearch, setModelSearch] = useState('');
	const [tested, setTested] = useState(false);
	const [busy, setBusy] = useState('');
	const [error, setError] = useState('');
	const [message, setMessage] = useState('');
	const [editingPresetId, setEditingPresetId] = useState('');

	const filteredPresets = presets.filter((preset) => preset.cli === cli);
	const filteredModels = useMemo(() => {
		const query = modelSearch.trim().toLowerCase();
		return query ? models.filter((item) => item.toLowerCase().includes(query)) : models;
	}, [modelSearch, models]);

	const loadPresets = async () => {
		const data = await api<{ presets: CLIConfigPreset[] }>('/api/cli-config/presets');
		setPresets(data.presets || []);
	};

	useEffect(() => {
		loadPresets().catch((err) => setError(err instanceof Error ? err.message : String(err)));
	}, []);

	useEffect(() => {
		if (resetAgents.some((agent) => agent.id === resetAgentId)) return;
		setResetAgentId(resetAgents.find((agent) => agent.id === preferredAgentId)?.id || resetAgents[0]?.id || '');
	}, [preferredAgentId, resetAgentId, resetAgents]);

	useEffect(() => {
		setName('');
		setBaseUrl('');
		setAPIKey('');
		setModel('');
		setReasoningEffort('');
		setReasoningLevels([]);
		setReasoningDefault('');
		setModels([]);
		setModelSearch('');
		setTested(false);
		setEditingPresetId('');
		setError('');
		setMessage('');
	}, [cli]);

	const applyTestResult = (result: CLIConfigResult, fallbackBaseURL: string) => {
		setBaseUrl(result.baseUrl || fallbackBaseURL);
		setModels(result.models || []);
		const metadata = result.modelMetadata;
		setReasoningLevels(metadata?.reviewed ? (metadata.supportedReasoningLevels || []) : []);
		setReasoningDefault(metadata?.reviewed ? (metadata.defaultReasoningLevel || '') : '');
		if (metadata?.reviewed && metadata.defaultReasoningLevel && !(metadata.supportedReasoningLevels || []).includes(reasoningEffort)) setReasoningEffort(metadata.defaultReasoningLevel);
		if (!metadata?.reviewed) setReasoningEffort('');
	};

	const testConnection = async () => {
		if ((!apiKey.trim() && !editingPresetId) || !baseUrl.trim()) {
			setError(t.modelLibraryAPIKeyRequired);
			return;
		}
		setBusy('test');
		setError('');
		setMessage('');
		try {
			const data = await api<{ result: CLIConfigResult }>('/api/cli-config/test', {
				method: 'POST',
				body: JSON.stringify({ cli, presetId: editingPresetId || undefined, baseUrl: baseUrl.trim(), model: model.trim(), reasoningEffort, apiKey: apiKey.trim() || undefined }),
			});
			applyTestResult(data.result, baseUrl.trim());
			setTested(true);
			setMessage(t.connectionPassed);
		} catch (err) {
			setTested(false);
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy('');
		}
	};

	const savePreset = async () => {
		if ((!apiKey.trim() && !editingPresetId) || !tested || !name.trim() || !model.trim()) {
			setError(t.modelLibraryAPIKeyRequired);
			return;
		}
		setBusy('save');
		setError('');
		try {
			const editing = !!editingPresetId;
			const path = editing ? `/api/cli-config/presets/${encodeURIComponent(editingPresetId)}` : '/api/cli-config/presets';
			await api(path, {
				method: editing ? 'PUT' : 'POST',
				body: JSON.stringify({ cli, name: name.trim(), baseUrl: baseUrl.trim(), model: model.trim(), reasoningEffort, apiKey: apiKey.trim() || undefined }),
			});
			await loadPresets();
			await onPresetsChanged();
			clearEditor();
			setMessage(editing ? t.presetUpdated : t.presetSaved);
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy('');
		}
	};

	const editPreset = (preset: CLIConfigPreset) => {
		setEditingPresetId(preset.id);
		setName(preset.name);
		setBaseUrl(preset.baseUrl);
		setModel(preset.model);
		setReasoningEffort(preset.reasoningEffort || preset.reasoningDefault || '');
		setReasoningLevels(preset.reasoningLevels || []);
		setReasoningDefault(preset.reasoningDefault || '');
		setAPIKey('');
		setModels([]);
		setModelSearch('');
		setTested(false);
		setError('');
		setMessage('');
	};

	const clearEditor = () => {
		setEditingPresetId('');
		setName('');
		setBaseUrl('');
		setAPIKey('');
		setModel('');
		setReasoningEffort('');
		setReasoningLevels([]);
		setReasoningDefault('');
		setModels([]);
		setModelSearch('');
		setTested(false);
	};

	const deletePreset = async (preset: CLIConfigPreset) => {
		if (!window.confirm(t.deletePresetConfirm)) return;
		setBusy(`delete:${preset.id}`);
		setError('');
		try {
			await api(`/api/cli-config/presets/${encodeURIComponent(preset.id)}`, { method: 'DELETE' });
			await loadPresets();
			await onPresetsChanged();
			if (editingPresetId === preset.id) clearEditor();
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy('');
		}
	};

	const testSavedPreset = async (preset: CLIConfigPreset) => {
		setBusy(`test:${preset.id}`);
		setError('');
		setMessage('');
		try {
			const data = await api<{ result: CLIConfigResult }>('/api/cli-config/test', { method: 'POST', body: JSON.stringify({ cli: preset.cli, presetId: preset.id, baseUrl: preset.baseUrl, model: preset.model }) });
			setMessage(t.connectionPassed);
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy('');
		}
	};

	const resetOfficial = async () => {
		if (!window.confirm(t.resetOfficialConfirm)) return;
		const resetAgent = resetAgents.find((agent) => agent.id === resetAgentId);
		if (!resetAgent) {
			setError(t.modelLibraryNoResetTarget);
			return;
		}
		setBusy('reset');
		setError('');
		setMessage('');
		try {
			await api(`/api/agents/${encodeURIComponent(resetAgent.id)}/cli-config/official-reset`, { method: 'POST', body: JSON.stringify({ cli }) });
			await loadPresets();
			setMessage(t.officialSettingsRestored);
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy('');
		}
	};

	return (
		<div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/45 p-3 backdrop-blur-sm">
			<div className="flex max-h-[88vh] w-full max-w-2xl flex-col overflow-hidden rounded-lg border border-border bg-card shadow-xl">
				<div className="flex items-center justify-between border-b border-border px-4 py-3">
					<div className="min-w-0">
						<h2 className="text-sm font-semibold">{t.modelLibrary}</h2>
						<p className="text-xs text-muted-foreground">{t.modelLibraryHint}</p>
					</div>
					<Button variant="ghost" size="icon" className="h-7 w-7 rounded-md" onClick={close} aria-label={t.cancel}><X className="h-4 w-4" /></Button>
				</div>
				<div className="overflow-y-auto p-4 elegant-scrollbar">
					<div className="mb-4 grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
						{(['codex', 'claude'] as CLI[]).map((item) => (
							<button key={item} onClick={() => setCLI(item)} className={cn('h-8 rounded-md text-xs font-medium', cli === item ? 'bg-background shadow-sm' : 'text-muted-foreground')}>{item === 'codex' ? 'Codex' : 'Claude Code'}</button>
						))}
					</div>

					<div className="space-y-5">
						<section className="space-y-2">
							<div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t.providerPresets}</div>
							{filteredPresets.length ? filteredPresets.map((preset) => (
								<div key={preset.id} className="flex flex-col gap-2 rounded-md border border-border px-3 py-2.5 sm:flex-row sm:items-center sm:gap-3">
									<div className="min-w-0 flex-1">
										<div className="flex items-center gap-2"><span className="truncate text-sm font-medium">{preset.name}</span></div>
									<div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">{preset.model} · {preset.reasoningEffort || t.defaultReasoningEffort} · {preset.baseUrl}</div>
									</div>
									<div className="flex shrink-0 items-center gap-1.5">
										<Button size="sm" variant="ghost" className="h-7 gap-1.5 text-muted-foreground" onClick={() => testSavedPreset(preset)} disabled={!!busy} aria-label={t.testConnection}>{busy === `test:${preset.id}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ServerCog className="h-3.5 w-3.5" />}{t.testConnection}</Button>
										<Button size="sm" variant="ghost" className="h-7 gap-1.5 text-muted-foreground" onClick={() => editPreset(preset)} disabled={!!busy} aria-label={t.editPreset}><Pencil className="h-3.5 w-3.5" />{t.editPreset}</Button>
										<Button size="icon" variant="ghost" className="h-7 w-7 rounded-md text-muted-foreground hover:text-destructive" onClick={() => deletePreset(preset)} disabled={!!busy} aria-label={t.deletePresetConfirm}><Trash2 className="h-3.5 w-3.5" /></Button>
									</div>
								</div>
							)) : <div className="rounded-md border border-dashed border-border px-3 py-4 text-center text-xs text-muted-foreground">{t.noProviderPresets}</div>}
						</section>

						<section className="space-y-3 border-t border-border pt-4">
							{editingPresetId && <div className="flex items-center justify-between gap-3"><div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t.editPreset}</div><Button size="sm" variant="ghost" className="h-7" onClick={clearEditor} disabled={!!busy}>{t.cancelEdit}</Button></div>}
							<div className="grid gap-3 sm:grid-cols-2">
								<Field label={t.providerName}><Input value={name} onChange={(event) => setName(event.target.value)} placeholder={cli === 'codex' ? 'DeepSeek Codex' : 'Claude proxy'} /></Field>
									<Field label={t.baseUrl}><Input value={baseUrl} onChange={(event) => { setBaseUrl(event.target.value); setTested(false); }} placeholder="https://api.example.com/v1" /></Field>
									<Field label={t.apiKey}><Input type="password" autoComplete="new-password" value={apiKey} onChange={(event) => { setAPIKey(event.target.value); setTested(false); }} placeholder={editingPresetId ? t.keepExistingAPIKey : 'sk-...'} /></Field>
									<Field label={t.modelName}><Input value={model} onChange={(event) => { setModel(event.target.value); setReasoningEffort(''); setReasoningLevels([]); setReasoningDefault(''); setTested(false); }} placeholder={cli === 'codex' ? 'model-name' : 'claude-compatible-model'} /></Field>
									<Field label={t.reasoningEffort}><select value={reasoningEffort} onChange={(event) => setReasoningEffort(event.target.value)} disabled={!reasoningLevels.length} className="flex h-9 w-full rounded-md border border-input bg-transparent px-2 py-1 text-xs"><option value="">{reasoningLevels.length ? t.defaultReasoningEffort : t.modelCatalogUnavailable}</option>{reasoningLevels.map((level) => <option key={level} value={level}>{level}{level === reasoningDefault ? ` (${t.defaultReasoningEffort})` : ''}</option>)}</select></Field>
							</div>
							<div className="flex items-start gap-2 text-[11px] leading-relaxed text-muted-foreground"><KeyRound className="mt-0.5 h-3.5 w-3.5 shrink-0" />{editingPresetId ? t.editAPIKeyHint : t.apiKeyEncryptedHint}</div>
							<div className="flex flex-wrap gap-2">
									<Button size="sm" variant="secondary" className="gap-1.5" onClick={testConnection} disabled={!!busy || (!apiKey.trim() && !editingPresetId) || !baseUrl.trim()}>{busy === 'test' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ServerCog className="h-3.5 w-3.5" />}{busy === 'test' ? t.testingConnection : t.testConnection}</Button>
								<Button size="sm" onClick={savePreset} disabled={!!busy || !tested || (!apiKey.trim() && !editingPresetId) || !name.trim() || !model.trim()}>{busy === 'save' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : editingPresetId ? t.updatePreset : t.savePreset}</Button>
							</div>
								{tested && <div className="space-y-2 rounded-md border border-emerald-500/25 bg-emerald-500/5 p-3">
								<div className="flex items-center gap-2 text-xs font-medium text-emerald-700 dark:text-emerald-300"><Check className="h-3.5 w-3.5" />{t.connectionPassed}</div>
								<div className="truncate font-mono text-[11px] text-muted-foreground">{t.normalizedBaseUrl}: {baseUrl}</div>
									{models.length ? <><div className="relative"><Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" /><Input className="pl-8" value={modelSearch} onChange={(event) => setModelSearch(event.target.value)} placeholder={t.searchModels} /></div><div className="max-h-36 overflow-y-auto rounded-md border border-border bg-background p-1 elegant-scrollbar">{filteredModels.slice(0, 200).map((item) => <button key={item} className={cn('block w-full truncate rounded px-2 py-1.5 text-left font-mono text-xs hover:bg-muted', model === item && 'bg-primary/10 text-primary')} onClick={() => { setModel(item); setReasoningEffort(''); setReasoningLevels([]); setReasoningDefault(''); setTested(false); setMessage(''); }}>{item}</button>)}</div></> : <div className="text-xs text-muted-foreground">{t.modelListUnavailable}</div>}
									{tested && !reasoningLevels.length && <div className="text-[11px] text-muted-foreground">{t.modelCatalogUnavailable}</div>}
							</div>}
						</section>

							<section className="space-y-3 border-t border-border pt-4">
								<div className="min-w-0"><div className="text-sm font-medium">{t.modelLibraryNativeMaintenance}</div><p className="text-xs leading-relaxed text-muted-foreground">{t.modelLibraryNativeMaintenanceHint}</p></div>
								<div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
									<select value={resetAgentId} onChange={(event) => setResetAgentId(event.target.value)} disabled={!resetAgents.length || !!busy} className="h-8 min-w-0 flex-1 rounded-md border border-input bg-transparent px-2 text-xs">
										{resetAgents.length ? resetAgents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>) : <option value="">{t.modelLibraryNoResetTarget}</option>}
									</select>
									<Button size="sm" variant="secondary" className="shrink-0" onClick={resetOfficial} disabled={!!busy || !resetAgentId}>{busy === 'reset' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : t.officialLogin}</Button>
								</div>
							</section>
						{error && <div className="flex items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive"><AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />{error}</div>}
						{message && <div className="flex items-start gap-2 rounded-md border border-emerald-500/25 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-700 dark:text-emerald-300"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0" />{message}</div>}
					</div>
				</div>
			</div>
		</div>
	);
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
	return <label className="block space-y-1.5"><span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{label}</span>{children}</label>;
}
