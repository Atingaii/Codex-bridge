import React, { useEffect, useMemo, useState } from 'react';
import { ArrowLeft, BarChart3, Clock3, Coins, Database, RefreshCw, Ticket } from 'lucide-react';
import { api } from '../lib/api';
import type { OrchestrationRun, OrchestrationRunStats } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { Button } from '../components/ui';
import { formatDuration, formatTime } from '../lib/utils';

export function OrchestrationStatsPage({ t, navigate, runId }: { t: UIText; navigate: (path: string) => void; runId: string }) {
  const [run, setRun] = useState<OrchestrationRun | null>(null);
  const [stats, setStats] = useState<OrchestrationRunStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const load = async () => {
    if (!runId) { setLoading(false); return; }
    setLoading(true); setError('');
    try {
      const [runData, statsData] = await Promise.all([
        api<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(runId)}`),
        api<{ stats: OrchestrationRunStats }>(`/api/orchestrations/${encodeURIComponent(runId)}/stats`),
      ]);
      setRun(runData.run); setStats(statsData.stats);
    } catch (err) { setError(err instanceof Error ? err.message : t.failedLoadOrchestration); }
    finally { setLoading(false); }
  };
  useEffect(() => { load(); }, [runId]);
  const runtime = useMemo(() => stats ? formatDuration(stats.runtimeSeconds * 1000) || '0s' : '', [stats]);
  const cards = stats ? [
    { label: t.runtime, value: runtime, icon: Clock3 },
    { label: t.inputTokens, value: stats.inputTokens.toLocaleString(), icon: Ticket },
    { label: t.outputTokens, value: stats.outputTokens.toLocaleString(), icon: BarChart3 },
    { label: `${t.cacheReadTokens} / ${t.cacheWriteTokens}`, value: `${stats.cacheReadTokens.toLocaleString()} / ${stats.cacheWriteTokens.toLocaleString()}`, icon: Database },
    { label: t.estimatedCost, value: stats.costKnown ? `$${stats.estimatedCostUsd.toFixed(6)}` : '—', icon: Coins },
  ] : [];
  return <div className="min-h-screen bg-background text-foreground">
    <header className="flex h-14 items-center justify-between border-b border-border px-4 md:px-8">
      <div className="flex min-w-0 items-center gap-3"><BarChart3 className="h-5 w-5 text-primary" /><div className="min-w-0"><h1 className="truncate text-sm font-semibold">{t.runStatistics}</h1><p className="truncate text-xs text-muted-foreground">{run?.title || runId || t.noUsageYet}</p></div></div>
      <div className="flex items-center gap-2"><Button variant="ghost" size="icon" onClick={() => load()} disabled={loading} aria-label={t.refresh} title={t.refresh}><RefreshCw className={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /></Button><Button variant="secondary" size="sm" onClick={() => navigate(runId ? `/orchestrate/runs/${encodeURIComponent(runId)}` : '/orchestrate')}><ArrowLeft className="mr-1.5 h-3.5 w-3.5" />{t.backToRun}</Button></div>
    </header>
    <main className="mx-auto max-w-5xl space-y-6 p-4 md:p-8">
      {error && <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>}
      {!runId ? <div className="rounded-lg border border-dashed border-border p-12 text-center text-sm text-muted-foreground">{t.noUsageYet}</div> : loading && !stats ? <div className="flex justify-center p-16"><RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" /></div> : stats && <>
        <section className="rounded-lg border border-border bg-card p-5"><div className="flex flex-wrap items-center justify-between gap-3"><div><p className="text-xs uppercase tracking-wider text-muted-foreground">{t.status}</p><p className="mt-1 text-lg font-semibold">{run?.status || t.idle}</p></div><div className="text-right text-xs text-muted-foreground"><div>{stats.startedAt ? formatTime(stats.startedAt) : ''}{stats.finishedAt ? ` -> ${formatTime(stats.finishedAt)}` : ''}</div><div className="mt-1">{stats.native ? t.nativeUsage : t.estimatedUsageNotice}{stats.costKnown ? ` · ${stats.costSource || 'catalog'}` : ` · ${t.costUnavailable}`}</div></div></div></section>
        <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">{cards.map(({ label, value, icon: Icon }) => <div key={label} className="rounded-lg border border-border bg-card p-4"><Icon className="h-4 w-4 text-muted-foreground" /><p className="mt-3 text-xs text-muted-foreground">{label}</p><p className="mt-1 text-xl font-semibold tabular-nums">{value}</p></div>)}</section>
        <section className="rounded-lg border border-border bg-card"><div className="border-b border-border px-5 py-3 text-sm font-semibold">{t.perCli}</div><div className="divide-y divide-border">{stats.byCli.length ? stats.byCli.map((item) => <div key={`${item.cli}:${item.model}`} className="grid gap-3 px-5 py-4 sm:grid-cols-[1fr_repeat(4,auto)] sm:items-center"><div><div className="font-medium capitalize">{item.cli}</div><div className="text-xs text-muted-foreground">{item.model || 'default'} · {item.native ? t.nativeUsage : t.unavailableUsage}</div></div><div className="text-xs"><span className="text-muted-foreground">{t.inputTokens}</span><div className="font-medium tabular-nums">{item.inputTokens.toLocaleString()}</div></div><div className="text-xs"><span className="text-muted-foreground">{t.outputTokens}</span><div className="font-medium tabular-nums">{item.outputTokens.toLocaleString()}</div></div><div className="text-xs"><span className="text-muted-foreground">{t.cacheReadTokens}</span><div className="font-medium tabular-nums">{item.cacheReadTokens.toLocaleString()}</div></div><div className="text-xs"><span className="text-muted-foreground">{t.estimatedCost}</span><div className="font-medium tabular-nums">{item.costKnown ? `$${item.estimatedCostUsd.toFixed(6)}` : '—'}</div></div></div>) : <div className="p-5 text-sm text-muted-foreground">{t.noUsageYet}</div>}</div></section>
      </>}
    </main>
  </div>;
}
