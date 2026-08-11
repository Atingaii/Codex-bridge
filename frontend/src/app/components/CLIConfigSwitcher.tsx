import React, { useEffect, useMemo, useState } from 'react';
import { AlertCircle, Check, KeyRound, Loader2, Pencil, Search, ServerCog, Trash2, X } from 'lucide-react';
import { api } from '../lib/api';
import type { Agent, CLIConfigPreset, CLIConfigResult, EncryptedSecret } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { cn } from '../lib/utils';
import { Button, Input } from './ui';

type CLI = 'codex' | 'claude';

export function CLIConfigSwitcher({ agent, t, close }: { agent: Agent; t: UIText; close: () => void }) {
	const capability = agent.capabilities?.configSwitcher;
	const [cli, setCLI] = useState<CLI>('codex');
	const [presets, setPresets] = useState<CLIConfigPreset[]>([]);
	const [name, setName] = useState('');
	const [baseUrl, setBaseUrl] = useState('');
	const [apiKey, setAPIKey] = useState('');
	const [model, setModel] = useState('');
	const [models, setModels] = useState<string[]>([]);
	const [modelSearch, setModelSearch] = useState('');
	const [testedSecret, setTestedSecret] = useState<EncryptedSecret | null>(null);
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
		const data = await api<{ presets: CLIConfigPreset[] }>(`/api/agents/${encodeURIComponent(agent.id)}/cli-config/presets`);
		setPresets(data.presets || []);
	};

	useEffect(() => {
		loadPresets().catch((err) => setError(err instanceof Error ? err.message : String(err)));
	}, [agent.id]);

	useEffect(() => {
		setName('');
		setBaseUrl('');
		setAPIKey('');
		setModel('');
		setModels([]);
		setModelSearch('');
		setTested(false);
		setTestedSecret(null);
		setEditingPresetId('');
		setError('');
		setMessage('');
	}, [cli]);

	const testConnection = async () => {
		if (!capability || (!apiKey.trim() && !testedSecret && !editingPresetId) || !baseUrl.trim()) return;
		setBusy('test');
		setError('');
		setMessage('');
		try {
			const secret = apiKey.trim() ? await encryptForBridge(apiKey, capability.publicKey) : testedSecret || undefined;
			const data = await api<{ result: CLIConfigResult }>(`/api/agents/${encodeURIComponent(agent.id)}/cli-config/test`, {
				method: 'POST',
				body: JSON.stringify({ cli, presetId: editingPresetId || undefined, baseUrl: baseUrl.trim(), model: model.trim(), secret, keyId: capability.keyId }),
			});
			setBaseUrl(data.result.baseUrl || baseUrl.trim());
			setModels(data.result.models || []);
			setTestedSecret(secret || null);
			setTested(true);
			setAPIKey('');
			setMessage(t.connectionPassed);
		} catch (err) {
			setTested(false);
			setTestedSecret(null);
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy('');
		}
	};

	const savePreset = async () => {
		if (!capability || (!testedSecret && !editingPresetId) || !tested || !name.trim() || !model.trim()) return;
		setBusy('save');
		setError('');
		try {
			const editing = !!editingPresetId;
			const path = editing
				? `/api/agents/${encodeURIComponent(agent.id)}/cli-config/presets/${encodeURIComponent(editingPresetId)}`
				: `/api/agents/${encodeURIComponent(agent.id)}/cli-config/presets`;
			await api(path, {
				method: editing ? 'PUT' : 'POST',
				body: JSON.stringify({ cli, name: name.trim(), baseUrl: baseUrl.trim(), model: model.trim(), secret: testedSecret || undefined, keyId: capability.keyId }),
			});
			await loadPresets();
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
		setAPIKey('');
		setModels([]);
		setModelSearch('');
		setTested(false);
		setTestedSecret(null);
		setError('');
		setMessage('');
	};

	const clearEditor = () => {
		setEditingPresetId('');
		setName('');
		setBaseUrl('');
		setAPIKey('');
		setModel('');
		setModels([]);
		setModelSearch('');
		setTested(false);
		setTestedSecret(null);
	};

	const applyPreset = async (preset: CLIConfigPreset) => {
		setBusy(`apply:${preset.id}`);
		setError('');
		setMessage('');
		try {
			await api(`/api/agents/${encodeURIComponent(agent.id)}/cli-config/presets/${encodeURIComponent(preset.id)}/apply`, { method: 'POST', body: '{}' });
			await loadPresets();
			setMessage(t.configurationApplied);
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy('');
		}
	};

	const deletePreset = async (preset: CLIConfigPreset) => {
		if (!window.confirm(t.deletePresetConfirm)) return;
		setBusy(`delete:${preset.id}`);
		setError('');
		try {
			await api(`/api/agents/${encodeURIComponent(agent.id)}/cli-config/presets/${encodeURIComponent(preset.id)}`, { method: 'DELETE' });
			await loadPresets();
			if (editingPresetId === preset.id) clearEditor();
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy('');
		}
	};

	const resetOfficial = async () => {
		if (!window.confirm(t.resetOfficialConfirm)) return;
		setBusy('reset');
		setError('');
		setMessage('');
		try {
			await api(`/api/agents/${encodeURIComponent(agent.id)}/cli-config/official-reset`, { method: 'POST', body: JSON.stringify({ cli }) });
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
						<h2 className="text-sm font-semibold">{t.modelConfiguration}</h2>
						<p className="truncate text-xs text-muted-foreground">{agent.name} · {t.modelConfigurationHint}</p>
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
								<div key={preset.id} className="flex items-center gap-3 rounded-md border border-border px-3 py-2.5">
									<div className="min-w-0 flex-1">
										<div className="flex items-center gap-2"><span className="truncate text-sm font-medium">{preset.name}</span>{preset.active && <span className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400">{t.activePreset}</span>}</div>
										<div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">{preset.model} · {preset.baseUrl}</div>
									</div>
									<Button size="sm" variant="secondary" className="h-7" onClick={() => applyPreset(preset)} disabled={!!busy}>{busy === `apply:${preset.id}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : t.applyPreset}</Button>
									<Button size="icon" variant="ghost" className="h-7 w-7 rounded-md text-muted-foreground" onClick={() => editPreset(preset)} disabled={!!busy} aria-label={t.editPreset}><Pencil className="h-3.5 w-3.5" /></Button>
									<Button size="icon" variant="ghost" className="h-7 w-7 rounded-md text-muted-foreground hover:text-destructive" onClick={() => deletePreset(preset)} disabled={!!busy} aria-label={t.deletePresetConfirm}><Trash2 className="h-3.5 w-3.5" /></Button>
								</div>
							)) : <div className="rounded-md border border-dashed border-border px-3 py-4 text-center text-xs text-muted-foreground">{t.noProviderPresets}</div>}
						</section>

						<section className="space-y-3 border-t border-border pt-4">
							{editingPresetId && <div className="flex items-center justify-between gap-3"><div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t.editPreset}</div><Button size="sm" variant="ghost" className="h-7" onClick={clearEditor} disabled={!!busy}>{t.cancelEdit}</Button></div>}
							<div className="grid gap-3 sm:grid-cols-2">
								<Field label={t.providerName}><Input value={name} onChange={(event) => setName(event.target.value)} placeholder={cli === 'codex' ? 'DeepSeek Codex' : 'Claude proxy'} /></Field>
									<Field label={t.baseUrl}><Input value={baseUrl} onChange={(event) => { setBaseUrl(event.target.value); setTested(false); }} placeholder="https://api.example.com/v1" /></Field>
									<Field label={t.apiKey}><Input type="password" autoComplete="new-password" value={apiKey} onChange={(event) => { setAPIKey(event.target.value); setTested(false); setTestedSecret(null); }} placeholder={editingPresetId ? t.keepExistingAPIKey : 'sk-...'} /></Field>
									<Field label={t.modelName}><Input value={model} onChange={(event) => { setModel(event.target.value); setTested(false); }} placeholder={cli === 'codex' ? 'model-name' : 'claude-compatible-model'} /></Field>
							</div>
							<div className="flex items-start gap-2 text-[11px] leading-relaxed text-muted-foreground"><KeyRound className="mt-0.5 h-3.5 w-3.5 shrink-0" />{editingPresetId ? t.editAPIKeyHint : t.apiKeyEncryptedHint}</div>
							<div className="flex flex-wrap gap-2">
									<Button size="sm" variant="secondary" className="gap-1.5" onClick={testConnection} disabled={!!busy || (!apiKey.trim() && !testedSecret && !editingPresetId) || !baseUrl.trim()}>{busy === 'test' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ServerCog className="h-3.5 w-3.5" />}{busy === 'test' ? t.testingConnection : t.testConnection}</Button>
								<Button size="sm" onClick={savePreset} disabled={!!busy || !tested || (!testedSecret && !editingPresetId) || !name.trim() || !model.trim()}>{busy === 'save' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : editingPresetId ? t.updatePreset : t.savePreset}</Button>
							</div>
							{tested && <div className="space-y-2 rounded-md border border-emerald-500/25 bg-emerald-500/5 p-3">
								<div className="flex items-center gap-2 text-xs font-medium text-emerald-700 dark:text-emerald-300"><Check className="h-3.5 w-3.5" />{t.connectionPassed}</div>
								<div className="truncate font-mono text-[11px] text-muted-foreground">{t.normalizedBaseUrl}: {baseUrl}</div>
									{models.length ? <><div className="relative"><Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-muted-foreground" /><Input className="pl-8" value={modelSearch} onChange={(event) => setModelSearch(event.target.value)} placeholder={t.searchModels} /></div><div className="max-h-36 overflow-y-auto rounded-md border border-border bg-background p-1 elegant-scrollbar">{filteredModels.slice(0, 200).map((item) => <button key={item} className={cn('block w-full truncate rounded px-2 py-1.5 text-left font-mono text-xs hover:bg-muted', model === item && 'bg-primary/10 text-primary')} onClick={() => { setModel(item); setTested(false); setMessage(''); }}>{item}</button>)}</div></> : <div className="text-xs text-muted-foreground">{t.modelListUnavailable}</div>}
							</div>}
						</section>

						<section className="flex items-center justify-between gap-3 border-t border-border pt-4">
							<div className="min-w-0"><div className="text-sm font-medium">{t.officialLogin}</div><p className="text-xs leading-relaxed text-muted-foreground">{t.officialLoginHint}</p></div>
							<Button size="sm" variant="secondary" className="shrink-0" onClick={resetOfficial} disabled={!!busy}>{busy === 'reset' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : t.officialLogin}</Button>
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

async function encryptForBridge(secret: string, bridgePublicKey: string): Promise<EncryptedSecret> {
	if (!window.crypto?.subtle) throw new Error('WebCrypto is unavailable; use HTTPS or localhost.');
	const bridgeKey = await crypto.subtle.importKey('raw', fromBase64(bridgePublicKey), { name: 'ECDH', namedCurve: 'P-256' }, false, []);
	const ephemeral = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']);
	const shared = await crypto.subtle.deriveBits({ name: 'ECDH', public: bridgeKey }, ephemeral.privateKey, 256);
	const salt = crypto.getRandomValues(new Uint8Array(16));
	const iv = crypto.getRandomValues(new Uint8Array(12));
	const keyMaterial = await crypto.subtle.importKey('raw', shared, 'HKDF', false, ['deriveKey']);
	const aesKey = await crypto.subtle.deriveKey({ name: 'HKDF', hash: 'SHA-256', salt, info: new TextEncoder().encode('codex-bridge-cli-config-v1') }, keyMaterial, { name: 'AES-GCM', length: 256 }, false, ['encrypt']);
	const ciphertext = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, aesKey, new TextEncoder().encode(secret));
	const publicKey = await crypto.subtle.exportKey('raw', ephemeral.publicKey);
	return { ephemeralPublicKey: toBase64(publicKey), salt: toBase64(salt), iv: toBase64(iv), ciphertext: toBase64(ciphertext) };
}

function fromBase64(value: string): ArrayBuffer {
	const normalized = value.replace(/-/g, '+').replace(/_/g, '/');
	const raw = atob(normalized + '='.repeat((4 - normalized.length % 4) % 4));
	const bytes = Uint8Array.from(raw, (char) => char.charCodeAt(0));
	return bytes.buffer;
}

function toBase64(value: ArrayBuffer | ArrayBufferView): string {
	const bytes = value instanceof ArrayBuffer ? new Uint8Array(value) : new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
	let raw = '';
	for (const byte of bytes) raw += String.fromCharCode(byte);
	return btoa(raw).replace(/=+$/, '');
}
