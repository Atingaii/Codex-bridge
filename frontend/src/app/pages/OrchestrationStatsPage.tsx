import React, { useEffect, useMemo, useState } from 'react';
import { ArrowLeft, BarChart3, Clock3, Coins, Database, RefreshCw, Ticket, Layers3, Globe2, Monitor, Route, Activity } from 'lucide-react';
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { api } from '../lib/api';
import type { OrchestrationRun, OrchestrationRunStats, OrchestrationUsageOverview } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { Button } from '../components/ui';
import { formatDuration, formatTime } from '../lib/utils';

type OverviewMetric = 'totalTokens' | 'inputTokens' | 'outputTokens' | 'cacheTokens' | 'estimatedCostUsd';
type OverviewRange = 7 | 30 | 90 | 0;

function formatCompact(value: number, digits = 1) {
  const absolute = Math.abs(value);
  const units = [{ value: 1e12, suffix: 'T' }, { value: 1e9, suffix: 'B' }, { value: 1e6, suffix: 'M' }, { value: 1e3, suffix: 'K' }];
  const unit = units.find((item) => absolute >= item.value);
  if (!unit) return value.toLocaleString();
  return `${(value / unit.value).toFixed(absolute >= unit.value * 100 ? 0 : digits).replace(/\.0$/, '')}${unit.suffix}`;
}

function exactMetric(value: number, metric: OverviewMetric) {
  return metric === 'estimatedCostUsd' ? `$${value.toFixed(6)}` : value.toLocaleString();
}

function AxisTooltip({ active, payload, label, metric, t }: any) {
  if (!active || !payload?.length) return null;
  return <div className="rounded-md border border-border bg-popover px-3 py-2 text-xs shadow-sm"><div className="mb-1 text-muted-foreground">{label}</div>{payload.map((item: any) => <div key={item.dataKey} className="flex min-w-44 items-center justify-between gap-5 py-0.5"><span style={{ color: item.color }}>{item.name}</span><span className="font-medium tabular-nums">{metric === 'estimatedCostUsd' ? `$${Number(item.value).toFixed(6)}` : Number(item.value).toLocaleString()}</span></div>)}{metric === 'estimatedCostUsd' && payload[0]?.payload?.costKnown === false ? <div className="mt-1 border-t border-border pt-1 text-muted-foreground">{t.knownCostOnly}</div> : null}</div>;
}

function UsageOverviewPage({ t, navigate }: { t: UIText; navigate: (path: string) => void }) {
  const [overview, setOverview] = useState<OrchestrationUsageOverview | null>(null);
  const [range, setRange] = useState<OverviewRange>(30);
  const [metric, setMetric] = useState<OverviewMetric>('totalTokens');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const load = async () => {
    setLoading(true); setError('');
    try {
      const timezoneOffset = new Date().getTimezoneOffset();
      const data = await api<{ overview: OrchestrationUsageOverview }>(`/api/usage/overview?days=${range}&timezoneOffset=${timezoneOffset}`);
      setOverview(data.overview);
    } catch (err) { setError(err instanceof Error ? err.message : t.failedLoadOrchestration); }
    finally { setLoading(false); }
  };
  useEffect(() => { void load(); }, [range]);
  const metricOptions: { value: OverviewMetric; label: string; color: string }[] = [
    { value: 'totalTokens', label: t.totalTokens, color: '#a78bfa' },
    { value: 'inputTokens', label: t.inputTokens, color: '#38bdf8' },
    { value: 'outputTokens', label: t.outputTokens, color: '#34d399' },
    { value: 'cacheTokens', label: t.cacheTokens, color: '#f59e0b' },
    { value: 'estimatedCostUsd', label: t.costMetric, color: '#f472b6' },
  ];
  const selectedMetric = metricOptions.find((item) => item.value === metric) || metricOptions[0];
  const machineGroups = useMemo(() => {
    const groups = new Map<string, { id: string; name: string; hostname?: string; items: OrchestrationUsageOverview['items'] }>();
    for (const item of overview?.items || []) {
      const group = groups.get(item.machineId) || { id: item.machineId, name: item.machineName, hostname: item.hostname, items: [] };
      group.items.push(item); groups.set(item.machineId, group);
    }
    return Array.from(groups.values());
  }, [overview]);
  const ranges: { value: OverviewRange; label: string }[] = [{ value: 7, label: t.last7Days }, { value: 30, label: t.last30Days }, { value: 90, label: t.last90Days }, { value: 0, label: t.allTime }];
  return <div className="min-h-screen bg-background text-foreground">
    <header className="flex min-h-14 items-center justify-between gap-3 border-b border-border px-4 py-2 md:px-8">
      <div className="flex min-w-0 items-center gap-3"><Globe2 className="h-5 w-5 shrink-0 text-primary" /><div className="min-w-0"><h1 className="truncate text-sm font-semibold">{t.usageOverview}</h1><p className="truncate text-xs text-muted-foreground">{t.byMachineAndTask}</p></div></div>
      <div className="flex shrink-0 items-center gap-2"><Button variant="ghost" size="icon" onClick={() => void load()} disabled={loading} aria-label={t.refresh} title={t.refresh}><RefreshCw className={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /></Button><Button variant="secondary" size="sm" onClick={() => navigate('/orchestrate')}><ArrowLeft className="mr-1.5 h-3.5 w-3.5" />{t.backToRun}</Button></div>
    </header>
    <main className="mx-auto max-w-6xl space-y-5 p-4 md:p-8">
      {error && <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>}
      <section className="flex flex-col gap-4 border-b border-border pb-5 lg:flex-row lg:items-end lg:justify-between">
        <div><p className="text-xs font-medium uppercase text-muted-foreground">{t.usageOverview}</p><h2 className="mt-1 text-xl font-semibold">{selectedMetric.label}</h2><p className="mt-1 text-sm text-muted-foreground">{t.byMachineAndTask}</p></div>
        <div className="flex flex-col gap-3 sm:flex-row">
          <div><div className="mb-1.5 text-[11px] text-muted-foreground">{t.timeRange}</div><div className="inline-flex max-w-full overflow-x-auto rounded-md border border-border bg-muted/30 p-0.5">{ranges.map((item) => <button key={item.value} type="button" onClick={() => setRange(item.value)} className={`h-7 whitespace-nowrap rounded px-2.5 text-xs transition-colors ${range === item.value ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{item.label}</button>)}</div></div>
          <div><div className="mb-1.5 text-[11px] text-muted-foreground">{t.metric}</div><div className="flex max-w-full overflow-x-auto rounded-md border border-border bg-muted/30 p-0.5">{metricOptions.map((item) => <button key={item.value} type="button" onClick={() => setMetric(item.value)} className={`h-7 whitespace-nowrap rounded px-2.5 text-xs transition-colors ${metric === item.value ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{item.label}</button>)}</div></div>
        </div>
      </section>
      {loading && !overview ? <div className="flex justify-center p-16"><RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" /></div> : overview ? <>
        <section className="grid gap-px overflow-hidden rounded-lg border border-border bg-border sm:grid-cols-2 lg:grid-cols-4">
          {[
            { label: t.totalTokens, value: overview.totalTokens, icon: Layers3, display: formatCompact(overview.totalTokens) },
            { label: t.estimatedCost, value: overview.estimatedCostUsd, icon: Coins, display: overview.costKnown ? `$${overview.estimatedCostUsd.toFixed(2)}` : `$${overview.estimatedCostUsd.toFixed(2)}*` },
            { label: t.callsMetric, value: overview.callCount, icon: Activity, display: formatCompact(overview.callCount) },
            { label: `${t.machinesMetric} / ${t.tasksMetric}`, value: overview.runs, icon: Monitor, display: `${overview.machines} / ${overview.runs}` },
          ].map(({ label, value, icon: Icon, display }) => <div key={label} className="bg-card p-4" title={Number(value).toLocaleString()}><div className="flex items-center justify-between"><p className="text-xs text-muted-foreground">{label}</p><Icon className="h-3.5 w-3.5 text-muted-foreground" /></div><p className="mt-2 text-xl font-semibold tabular-nums">{display}</p></div>)}
        </section>
        <section className="rounded-lg border border-border bg-card p-4 md:p-5">
          <div className="mb-4 flex items-center justify-between"><div><h3 className="text-sm font-semibold">{t.usageTrend}</h3><p className="mt-1 text-xs text-muted-foreground">{selectedMetric.label}{metric === 'estimatedCostUsd' && !overview.costKnown ? ` · ${t.knownCostOnly}` : ''}</p></div><BarChart3 className="h-4 w-4 text-muted-foreground" /></div>
          <div className="h-72 w-full">{overview.trend?.length ? <ResponsiveContainer width="100%" height="100%"><LineChart data={overview.trend} margin={{ top: 8, right: 10, left: 0, bottom: 4 }}><XAxis dataKey="date" tick={{ fontSize: 11 }} minTickGap={24} tickFormatter={(value) => String(value).slice(5)} /><YAxis width={54} tick={{ fontSize: 11 }} tickFormatter={(value) => metric === 'estimatedCostUsd' ? `$${formatCompact(Number(value))}` : formatCompact(Number(value))} /><Tooltip content={<AxisTooltip metric={metric} t={t} />} /><Line type="monotone" dataKey={metric} name={selectedMetric.label} stroke={selectedMetric.color} strokeWidth={2.5} dot={overview.trend.length <= 31 ? { r: 2.5 } : false} activeDot={{ r: 4 }} /></LineChart></ResponsiveContainer> : <div className="flex h-full items-center justify-center text-sm text-muted-foreground">{t.noOverviewUsage}</div>}</div>
        </section>
        <section className="space-y-3"><div className="flex items-center justify-between"><div><h3 className="text-sm font-semibold">{t.byMachineAndTask}</h3><p className="mt-1 text-xs text-muted-foreground">{overview.machines} {t.machinesMetric} · {overview.runs} {t.tasksMetric}</p></div><Monitor className="h-4 w-4 text-muted-foreground" /></div>{machineGroups.map((machine) => <div key={machine.id} className="overflow-hidden rounded-lg border border-border bg-card"><div className="flex items-center gap-3 border-b border-border bg-muted/20 px-4 py-3"><div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-border bg-background"><Monitor className="h-4 w-4" /></div><div className="min-w-0"><div className="truncate text-sm font-medium">{machine.name}</div><div className="truncate text-xs text-muted-foreground">{machine.hostname || machine.id} · {machine.items.length} {t.tasksMetric}</div></div></div><div className="divide-y divide-border">{machine.items.map((item) => <button key={item.runId} type="button" onClick={() => navigate(`/orchestrate/stats?run=${encodeURIComponent(item.runId)}`)} className="grid w-full gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/20 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center"><div className="min-w-0"><div className="truncate text-sm font-medium">{item.title || item.runId}</div><div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground"><Route className="h-3 w-3" /><span>{formatTime(item.createdAt)} · {item.status}</span></div></div><div className="grid grid-cols-2 gap-x-5 text-xs sm:text-right"><div><span className="text-muted-foreground">{selectedMetric.label}</span><div className="mt-0.5 font-medium tabular-nums" title={exactMetric(item[metric], metric)}>{metric === 'estimatedCostUsd' ? (item.costKnown ? `$${item.estimatedCostUsd.toFixed(2)}` : '—') : formatCompact(item[metric])}</div></div><div><span className="text-muted-foreground">{t.modelCalls}</span><div className="mt-0.5 font-medium tabular-nums">{item.callCount.toLocaleString()}</div></div></div><ArrowLeft className="hidden h-4 w-4 rotate-180 text-muted-foreground sm:block" /></button>)}</div></div>)}{machineGroups.length === 0 ? <div className="rounded-lg border border-dashed border-border p-12 text-center text-sm text-muted-foreground">{t.noOverviewUsage}</div> : null}</section>
      </> : null}
    </main>
  </div>;
}

function RunStatsPage({ t, navigate, runId }: { t: UIText; navigate: (path: string) => void; runId: string }) {
  const [run, setRun] = useState<OrchestrationRun | null>(null);
  const [stats, setStats] = useState<OrchestrationRunStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [syncing, setSyncing] = useState(false);
  const [selectedTaskNumber, setSelectedTaskNumber] = useState<number | 'all'>('all');
  const load = async () => {
    setLoading(true); setError('');
    try {
      const [runData, statsData] = await Promise.all([api<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(runId)}`), api<{ stats: OrchestrationRunStats }>(`/api/orchestrations/${encodeURIComponent(runId)}/stats`)]);
      setRun(runData.run); setStats(statsData.stats);
    } catch (err) { setError(err instanceof Error ? err.message : t.failedLoadOrchestration); }
    finally { setLoading(false); }
  };
  useEffect(() => { void load(); }, [runId]);
  const syncUsage = async () => { setSyncing(true); setError(''); try { await api(`/api/orchestrations/${encodeURIComponent(runId)}/usage-sync`, { method: 'POST' }); window.setTimeout(() => { void load(); }, 1200); } catch (err) { setError(err instanceof Error ? err.message : t.failedLoadOrchestration); } finally { setSyncing(false); } };
  useEffect(() => {
    if (selectedTaskNumber === 'all') return;
    if (!stats?.tasks?.some((task) => task.taskNumber === selectedTaskNumber)) setSelectedTaskNumber('all');
  }, [selectedTaskNumber, stats?.tasks]);
  const displayStats = selectedTaskNumber === 'all' ? stats : stats?.tasks?.find((task) => task.taskNumber === selectedTaskNumber) || stats;
  const runtime = useMemo(() => displayStats ? formatDuration(displayStats.runtimeSeconds * 1000) || '0s' : '', [displayStats]);
  const roundChartMinWidth = Math.max(520, (displayStats?.rounds?.length || 0) * 56);
  const costDescription = displayStats?.costSource === 'provider' ? t.providerCost : displayStats?.costSource === 'official-catalog' ? t.officialCatalog : displayStats?.costSource || '';
  const cards = displayStats ? [
    { label: t.runtime, value: runtime, icon: Clock3 },
    { label: t.inputTokens, value: displayStats.inputTokens.toLocaleString(), icon: Ticket },
    { label: t.outputTokens, value: displayStats.outputTokens.toLocaleString(), icon: BarChart3 },
    { label: `${t.cacheReadTokens} / ${t.cacheWriteTokens}`, value: `${displayStats.cacheReadTokens.toLocaleString()} / ${displayStats.cacheWriteTokens.toLocaleString()}`, icon: Database },
    { label: t.cacheTokens, value: displayStats.cacheTokens.toLocaleString(), icon: Layers3 },
    { label: t.totalTokens, value: displayStats.totalTokens.toLocaleString(), icon: Globe2 },
    { label: t.reasoningTokens, value: (displayStats.reasoningTokens || 0).toLocaleString(), icon: BarChart3 },
    { label: t.modelCalls, value: (displayStats.callCount || 0).toLocaleString(), icon: Ticket },
    { label: t.estimatedCost, value: displayStats.costKnown ? `$${displayStats.estimatedCostUsd.toFixed(6)}` : '—', icon: Coins },
  ] : [];
  const isZh = t.task === '任务';
  return <div className="min-h-screen bg-background text-foreground"><header className="flex min-h-14 items-center justify-between gap-3 border-b border-border px-4 py-2 md:px-8"><div className="flex min-w-0 items-center gap-3"><BarChart3 className="h-5 w-5 shrink-0 text-primary" /><div className="min-w-0"><h1 className="truncate text-sm font-semibold">{t.runStatistics}</h1><p className="truncate text-xs text-muted-foreground">{run?.title || runId}</p></div></div><div className="flex shrink-0 items-center gap-2"><Button variant="ghost" size="icon" onClick={() => void load()} disabled={loading} aria-label={t.refresh} title={t.refresh}><RefreshCw className={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /></Button><Button variant="secondary" size="sm" onClick={() => navigate('/orchestrate/usage')}><Globe2 className="mr-1.5 h-3.5 w-3.5" />{t.overview}</Button><Button variant="secondary" size="sm" onClick={() => navigate(`/orchestrate/runs/${encodeURIComponent(runId)}`)}><ArrowLeft className="mr-1.5 h-3.5 w-3.5" />{t.backToRun}</Button></div></header><main className="mx-auto max-w-5xl space-y-6 p-4 md:p-8">
    {error && <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>}
    {loading && !stats ? <div className="flex justify-center p-16"><RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" /></div> : stats ? <>
      <section className="rounded-lg border border-border bg-card p-5"><div className="flex flex-wrap items-center justify-between gap-3"><div><p className="text-xs uppercase text-muted-foreground">{t.status}</p><p className="mt-1 text-lg font-semibold">{run?.status || t.idle}</p><p className="mt-1 text-xs text-muted-foreground">{displayStats.accountingSource === 'local-cli-ledger' ? t.localLedgerUsage : t.usageLegacyIncomplete} · {displayStats.accountingStatus === 'complete' ? t.usageComplete : displayStats.accountingStatus === 'partial' ? t.usagePartial : t.usageLegacyIncomplete}</p></div><div className="text-right text-xs text-muted-foreground"><div>{displayStats.startedAt ? formatTime(displayStats.startedAt) : ''}{displayStats.finishedAt ? ` -> ${formatTime(displayStats.finishedAt)}` : ''}</div><div className="mt-1">{displayStats.native ? t.nativeUsage : t.estimatedUsageNotice}{displayStats.costKnown ? ` · ${costDescription}` : ` · ${t.costUnavailable}`}</div>{displayStats.pricingModels?.length ? <div className="mt-1">{t.pricingAnchor}: {displayStats.pricingModels.join(', ')}</div> : null}<Button className="mt-3" variant="secondary" size="sm" onClick={() => void syncUsage()} disabled={syncing}>{syncing ? t.syncingUsage : t.syncUsage}</Button></div></div></section>
      {(stats.tasks?.length || 0) > 0 && <section><div className="mb-1.5 text-[11px] font-medium text-muted-foreground">{isZh ? '统计范围' : 'Usage scope'}</div><div className="flex max-w-full gap-1 overflow-x-auto rounded-md border border-border bg-muted/30 p-1 elegant-scrollbar"><button type="button" onClick={() => setSelectedTaskNumber('all')} className={`h-8 shrink-0 rounded px-3 text-xs transition-colors ${selectedTaskNumber === 'all' ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{isZh ? '全部任务' : 'All tasks'}</button>{stats.tasks?.map((task) => <button key={task.taskNumber} type="button" onClick={() => setSelectedTaskNumber(task.taskNumber)} className={`h-8 shrink-0 rounded px-3 text-xs transition-colors ${selectedTaskNumber === task.taskNumber ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{isZh ? `任务 ${task.taskNumber}` : `Task ${task.taskNumber}`}</button>)}</div></section>}
      <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">{cards.map(({ label, value, icon: Icon }) => <div key={label} className="rounded-lg border border-border bg-card p-4"><Icon className="h-4 w-4 text-muted-foreground" /><p className="mt-3 text-xs text-muted-foreground">{label}</p><p className="mt-1 break-words text-xl font-semibold tabular-nums">{value}</p></div>)}</section>
      <section className="rounded-lg border border-border bg-card p-5"><div className="mb-4 flex items-center justify-between"><div><h2 className="text-sm font-semibold">{t.perRound}</h2><p className="mt-1 text-xs text-muted-foreground">{t.inputTokens} / {t.outputTokens} / {t.cacheTokens} / {t.totalTokens}</p></div><BarChart3 className="h-4 w-4 text-muted-foreground" /></div><div className="w-full overflow-x-auto"><div className="h-64" style={{ minWidth: roundChartMinWidth }}>{displayStats.rounds?.length ? <ResponsiveContainer width="100%" height="100%"><LineChart data={displayStats.rounds} margin={{ top: 8, right: 8, left: 0, bottom: 4 }}><XAxis dataKey="round" interval={0} tickFormatter={(value) => `#${value}`} /><YAxis tickFormatter={(value) => formatCompact(Number(value))} width={54} /><Tooltip content={<AxisTooltip metric="totalTokens" t={t} />} labelFormatter={(value) => `${t.turns} ${value}`} /><Line type="monotone" dataKey="inputTokens" name={t.inputTokens} stroke="#38bdf8" strokeWidth={2} dot={{ r: 3 }} /><Line type="monotone" dataKey="outputTokens" name={t.outputTokens} stroke="#34d399" strokeWidth={2} dot={{ r: 3 }} /><Line type="monotone" dataKey="cacheTokens" name={t.cacheTokens} stroke="#f59e0b" strokeWidth={2} dot={{ r: 3 }} /><Line type="monotone" dataKey="totalTokens" name={t.totalTokens} stroke="#a78bfa" strokeWidth={2.5} dot={{ r: 3 }} /></LineChart></ResponsiveContainer> : <div className="flex h-full items-center justify-center text-sm text-muted-foreground">{t.noUsageYet}</div>}</div></div></section>
      <section className="rounded-lg border border-border bg-card"><div className="border-b border-border px-5 py-3 text-sm font-semibold">{t.perCli}</div><div className="divide-y divide-border">{displayStats.byCli.length ? displayStats.byCli.map((item) => <div key={`${item.cli}:${item.model}`} className="grid gap-3 px-5 py-4 sm:grid-cols-[1fr_repeat(5,auto)] sm:items-center"><div><div className="font-medium capitalize">{item.cli}</div><div className="text-xs text-muted-foreground">{item.model || 'default'} · {item.native ? t.nativeUsage : t.unavailableUsage}</div>{item.pricingModel ? <div className="mt-1 text-xs text-muted-foreground">{t.pricingAnchor}: {item.pricingModel}</div> : null}</div>{[[t.modelCalls, item.callCount || 0], [t.inputTokens, item.inputTokens], [t.outputTokens, item.outputTokens], [t.cacheReadTokens, item.cacheReadTokens]].map(([label, value]) => <div key={String(label)} className="text-xs"><span className="text-muted-foreground">{label}</span><div className="font-medium tabular-nums">{Number(value).toLocaleString()}</div></div>)}<div className="text-xs"><span className="text-muted-foreground">{t.estimatedCost}</span><div className="font-medium tabular-nums">{item.costKnown ? `$${item.estimatedCostUsd.toFixed(6)}` : '—'}</div></div></div>) : <div className="p-5 text-sm text-muted-foreground">{t.noUsageYet}</div>}</div></section>
    </> : null}
  </main></div>;
}

export function OrchestrationStatsPage({ t, navigate, runId }: { t: UIText; navigate: (path: string) => void; runId: string }) {
  return runId ? <RunStatsPage t={t} navigate={navigate} runId={runId} /> : <UsageOverviewPage t={t} navigate={navigate} />;
}
