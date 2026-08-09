import React, { useEffect, useMemo, useState } from 'react';
import { Activity, ArrowDown, ArrowLeft, ArrowUp, BarChart3, ChevronRight, Coins, GitBranch, Layers3, Monitor, RefreshCw, Search, ShieldCheck, Users } from 'lucide-react';
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { api } from '../lib/api';
import type { AdminUsageOverview, AdminUserUsage } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { Button, Input } from '../components/ui';

type Range = 7 | 30 | 90 | 0;
type Metric = 'totalTokens' | 'inputTokens' | 'outputTokens' | 'cacheTokens' | 'estimatedCostUsd' | 'activeUsers';
type SortKey = 'lastActiveAt' | 'totalTokens' | 'estimatedCostUsd' | 'username';

function compact(value: number, digits = 1) {
  const unit = [{ n: 1e12, s: 'T' }, { n: 1e9, s: 'B' }, { n: 1e6, s: 'M' }, { n: 1e3, s: 'K' }].find((item) => Math.abs(value) >= item.n);
  if (!unit) return value.toLocaleString();
  return `${(value / unit.n).toFixed(Math.abs(value) >= unit.n * 100 ? 0 : digits).replace(/\.0$/, '')}${unit.s}`;
}

function relativeTime(timestamp: number, t: UIText) {
  const seconds = Math.max(0, Math.floor(Date.now() / 1000) - timestamp);
  if (seconds < 60) return t.activeStatus;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  if (seconds < 86400 * 30) return `${Math.floor(seconds / 86400)}d`;
  return new Date(timestamp * 1000).toLocaleDateString();
}

function StatusBadge({ status, t }: { status: AdminUserUsage['activityStatus']; t: UIText }) {
  const styles = {
    online: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    active: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    idle: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400',
    inactive: 'border-border bg-muted/40 text-muted-foreground',
  }[status];
  const label = { online: t.onlineStatus, active: t.activeStatus, idle: t.idleStatus, inactive: t.inactiveStatus }[status];
  return <span className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${styles}`}><span className="h-1.5 w-1.5 rounded-full bg-current" />{label}</span>;
}

function UsageTooltip({ active, payload, label, metric }: any) {
  if (!active || !payload?.length) return null;
  return <div className="min-w-48 rounded-md border border-border bg-popover px-3 py-2 text-xs shadow-sm"><div className="mb-1 text-muted-foreground">{label}</div>{payload.map((item: any) => <div key={item.dataKey} className="flex justify-between gap-5 py-0.5"><span style={{ color: item.color }}>{item.name}</span><span className="font-medium tabular-nums">{metric === 'estimatedCostUsd' ? `$${Number(item.value).toFixed(6)}` : Number(item.value).toLocaleString()}</span></div>)}</div>;
}

export function AdminUsagePage({ t, navigate }: { t: UIText; navigate: (path: string) => void }) {
  const [overview, setOverview] = useState<AdminUsageOverview | null>(null);
  const [range, setRange] = useState<Range>(30);
  const [metric, setMetric] = useState<Metric>('totalTokens');
  const [query, setQuery] = useState('');
  const [sortKey, setSortKey] = useState<SortKey>('lastActiveAt');
  const [descending, setDescending] = useState(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = async () => {
    setLoading(true); setError('');
    try {
      const timezoneOffset = new Date().getTimezoneOffset();
      const data = await api<{ overview: AdminUsageOverview }>(`/api/admin/usage?days=${range}&timezoneOffset=${timezoneOffset}`);
      setOverview(data.overview);
    } catch (err) { setError(err instanceof Error ? err.message : t.failedLoadOrchestration); }
    finally { setLoading(false); }
  };
  useEffect(() => { void load(); }, [range]);

  const users = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return (overview?.items || []).filter((item) => !needle || item.username.toLowerCase().includes(needle)).sort((a, b) => {
      let result = 0;
      if (sortKey === 'username') result = a.username.localeCompare(b.username);
      else result = a[sortKey] - b[sortKey];
      return descending ? -result : result;
    });
  }, [descending, overview, query, sortKey]);

  const metrics: { value: Metric; label: string; color: string }[] = [
    { value: 'totalTokens', label: t.totalTokens, color: '#a78bfa' },
    { value: 'inputTokens', label: t.inputTokens, color: '#38bdf8' },
    { value: 'outputTokens', label: t.outputTokens, color: '#34d399' },
    { value: 'cacheTokens', label: t.cacheTokens, color: '#f59e0b' },
    { value: 'estimatedCostUsd', label: t.costMetric, color: '#f472b6' },
    { value: 'activeUsers', label: t.activeUsersMetric, color: '#22c55e' },
  ];
  const selectedMetric = metrics.find((item) => item.value === metric) || metrics[0];
  const ranges: { value: Range; label: string }[] = [{ value: 7, label: t.last7Days }, { value: 30, label: t.last30Days }, { value: 90, label: t.last90Days }, { value: 0, label: t.allTime }];
  const setSort = (next: SortKey) => { if (sortKey === next) setDescending((value) => !value); else { setSortKey(next); setDescending(next !== 'username'); } };
  const SortIcon = descending ? ArrowDown : ArrowUp;

  return <div className="min-h-screen overflow-x-hidden bg-background text-foreground">
    <header className="flex min-h-14 items-center justify-between gap-3 border-b border-border px-4 py-2 md:px-8">
      <div className="flex min-w-0 items-center gap-3"><ShieldCheck className="h-5 w-5 shrink-0 text-primary" /><div className="min-w-0"><h1 className="truncate text-sm font-semibold">{t.adminDashboard}</h1><p className="truncate text-xs text-muted-foreground">{t.adminUsageSubtitle}</p></div></div>
      <div className="flex shrink-0 items-center gap-2"><Button variant="ghost" size="icon" onClick={() => void load()} disabled={loading} aria-label={t.refresh} title={t.refresh}><RefreshCw className={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /></Button><Button variant="secondary" size="sm" onClick={() => navigate('/')}><ArrowLeft className="mr-1.5 h-3.5 w-3.5" /><span className="hidden sm:inline">{t.backToWorkspace}</span></Button></div>
    </header>
    <main className="mx-auto max-w-7xl space-y-5 p-4 md:p-8">
      {error && <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>}
      <section className="flex flex-col gap-4 border-b border-border pb-5 lg:flex-row lg:items-end lg:justify-between">
        <div><p className="text-xs font-medium uppercase text-muted-foreground">{t.adminDashboard}</p><h2 className="mt-1 text-xl font-semibold">{selectedMetric.label}</h2><p className="mt-1 text-sm text-muted-foreground">{t.adminUsageSubtitle}</p></div>
        <div className="flex flex-col gap-3 sm:flex-row"><Control label={t.timeRange}>{ranges.map((item) => <Choice key={item.value} active={range === item.value} onClick={() => setRange(item.value)}>{item.label}</Choice>)}</Control><Control label={t.metric}>{metrics.map((item) => <Choice key={item.value} active={metric === item.value} onClick={() => setMetric(item.value)}>{item.label}</Choice>)}</Control></div>
      </section>
      {loading && !overview ? <div className="flex justify-center p-16"><RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" /></div> : overview ? <>
        <section className="grid gap-px overflow-hidden rounded-lg border border-border bg-border sm:grid-cols-2 lg:grid-cols-4">
          <Summary icon={Users} label={t.usersMetric} value={overview.users.toLocaleString()} detail={`${overview.activeUsers} ${t.activeUsersMetric} · ${overview.onlineUsers} ${t.onlineUsersMetric}`} />
          <Summary icon={Monitor} label={t.onlineEndpointsMetric} value={`${overview.onlineAgents} / ${overview.totalAgents}`} detail={`${t.sessionsMetric}: ${overview.chatSessions.toLocaleString()}`} />
          <Summary icon={Layers3} label={t.totalTokens} value={compact(overview.totalTokens)} title={overview.totalTokens.toLocaleString()} detail={`${overview.callCount.toLocaleString()} ${t.callsMetric}`} />
          <Summary icon={Coins} label={t.estimatedCost} value={`$${overview.estimatedCostUsd.toFixed(2)}${overview.costKnown ? '' : '*'}`} detail={overview.costKnown ? t.officialCatalog : t.costIncomplete} />
        </section>
        <section className="rounded-lg border border-border bg-card p-4 md:p-5">
          <div className="mb-4 flex items-center justify-between"><div><h3 className="text-sm font-semibold">{t.activityAndUsageTrend}</h3><p className="mt-1 text-xs text-muted-foreground">{selectedMetric.label}</p></div><BarChart3 className="h-4 w-4 text-muted-foreground" /></div>
          <div className="h-72 w-full">{overview.trend.length ? <ResponsiveContainer width="100%" height="100%"><LineChart data={overview.trend} margin={{ top: 8, right: 10, left: 0, bottom: 4 }}><XAxis dataKey="date" tick={{ fontSize: 11 }} minTickGap={24} tickFormatter={(value) => String(value).slice(5)} /><YAxis width={54} tick={{ fontSize: 11 }} allowDecimals={metric !== 'activeUsers'} tickFormatter={(value) => metric === 'estimatedCostUsd' ? `$${compact(Number(value))}` : compact(Number(value))} /><Tooltip content={<UsageTooltip metric={metric} />} /><Line type="monotone" dataKey={metric} name={selectedMetric.label} stroke={selectedMetric.color} strokeWidth={2.5} dot={overview.trend.length <= 31 ? { r: 2.5 } : false} activeDot={{ r: 4 }} /></LineChart></ResponsiveContainer> : <div className="flex h-full items-center justify-center text-sm text-muted-foreground">{t.noOverviewUsage}</div>}</div>
        </section>
        <section>
          <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><div><h3 className="text-sm font-semibold">{t.userUsageMetric}</h3><p className="mt-1 text-xs text-muted-foreground">{users.length} / {overview.users} {t.usersMetric}</p></div><div className="relative w-full sm:w-64"><Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" /><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t.searchUsers} className="h-9 pl-9" /></div></div>
          <div className="hidden overflow-hidden rounded-lg border border-border bg-card md:block"><table className="w-full table-fixed text-left text-xs"><thead className="border-b border-border bg-muted/20 text-muted-foreground"><tr><Header width="w-[22%]" onClick={() => setSort('username')} active={sortKey === 'username'} icon={SortIcon}>{t.username}</Header><Header width="w-[14%]" onClick={() => setSort('lastActiveAt')} active={sortKey === 'lastActiveAt'} icon={SortIcon}>{t.activityMetric}</Header><th className="w-[13%] px-4 py-3 font-medium">{t.endpointsMetric}</th><th className="w-[16%] px-4 py-3 font-medium">{t.workloadMetric}</th><Header width="w-[18%]" onClick={() => setSort('totalTokens')} active={sortKey === 'totalTokens'} icon={SortIcon}>{t.totalTokens}</Header><Header width="w-[17%]" onClick={() => setSort('estimatedCostUsd')} active={sortKey === 'estimatedCostUsd'} icon={SortIcon}>{t.estimatedCost}</Header></tr></thead><tbody className="divide-y divide-border">{users.map((item) => <tr key={item.userId} className="transition-colors hover:bg-muted/20"><td className="px-4 py-3"><button type="button" onClick={() => navigate(`/admin/usage/users/${encodeURIComponent(item.userId)}`)} className="group flex w-full min-w-0 items-center justify-between gap-2 text-left" aria-label={`${t.viewUserDetails}: ${item.username}`}><span className="min-w-0"><span className="block truncate text-sm font-medium group-hover:text-primary">{item.username}</span><span className="mt-1 block truncate text-muted-foreground">{item.userId}</span></span><ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground group-hover:text-primary" /></button></td><td className="px-4 py-3"><StatusBadge status={item.activityStatus} t={t} /><div className="mt-1.5 text-muted-foreground" title={new Date(item.lastActiveAt * 1000).toLocaleString()}>{relativeTime(item.lastActiveAt, t)}</div></td><td className="px-4 py-3"><div className="font-medium tabular-nums">{item.onlineAgents} / {item.totalAgents}</div><div className="mt-1 text-muted-foreground">{t.onlineEndpointsMetric}</div></td><td className="px-4 py-3"><div className="font-medium tabular-nums">{item.chatSessions} / {item.orchestrationRuns}</div><div className="mt-1 text-muted-foreground">{t.sessionsMetric} / {t.orchestrationRunsMetric}{item.runningRuns ? ` · ${item.runningRuns} ${t.runningRunsMetric}` : ''}</div></td><td className="px-4 py-3"><div className="font-medium tabular-nums" title={item.totalTokens.toLocaleString()}>{compact(item.totalTokens)}</div><div className="mt-1 text-muted-foreground">{item.callCount.toLocaleString()} {t.callsMetric}</div></td><td className="px-4 py-3"><div className="font-medium tabular-nums">{item.costKnown ? `$${item.estimatedCostUsd.toFixed(4)}` : item.callCount ? `$${item.estimatedCostUsd.toFixed(4)}*` : '—'}</div><div className="mt-1 text-muted-foreground">{item.costKnown ? t.officialCatalog : t.costIncomplete}</div></td></tr>)}</tbody></table></div>
          <div className="space-y-2 md:hidden">{users.map((item) => <button type="button" key={item.userId} onClick={() => navigate(`/admin/usage/users/${encodeURIComponent(item.userId)}`)} className="block w-full rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/40 hover:bg-muted/20" aria-label={`${t.viewUserDetails}: ${item.username}`}><div className="flex min-w-0 items-start justify-between gap-3"><div className="min-w-0"><div className="flex items-center gap-1.5"><span className="truncate text-sm font-semibold">{item.username}</span><ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /></div><div className="mt-1 truncate text-xs text-muted-foreground">{relativeTime(item.lastActiveAt, t)}</div></div><StatusBadge status={item.activityStatus} t={t} /></div><div className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-xs"><Datum label={t.endpointsMetric} value={`${item.onlineAgents} / ${item.totalAgents}`} /><Datum label={t.workloadMetric} value={`${item.chatSessions} / ${item.orchestrationRuns}`} /><Datum label={t.totalTokens} value={compact(item.totalTokens)} title={item.totalTokens.toLocaleString()} /><Datum label={t.estimatedCost} value={item.costKnown ? `$${item.estimatedCostUsd.toFixed(4)}` : item.callCount ? `$${item.estimatedCostUsd.toFixed(4)}*` : '—'} /></div></button>)} </div>
          {!users.length && <div className="rounded-lg border border-dashed border-border p-12 text-center text-sm text-muted-foreground">{t.noUsersFound}</div>}
        </section>
      </> : null}
    </main>
  </div>;
}

function Control({ label, children }: { label: string; children: React.ReactNode }) { return <div><div className="mb-1.5 text-[11px] text-muted-foreground">{label}</div><div className="flex max-w-full overflow-x-auto rounded-md border border-border bg-muted/30 p-0.5">{children}</div></div>; }
function Choice({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) { return <button type="button" onClick={onClick} className={`h-7 whitespace-nowrap rounded px-2.5 text-xs transition-colors ${active ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{children}</button>; }
function Summary({ icon: Icon, label, value, detail, title }: { icon: typeof Activity; label: string; value: string; detail: string; title?: string }) { return <div className="bg-card p-4" title={title}><div className="flex items-center justify-between"><p className="text-xs text-muted-foreground">{label}</p><Icon className="h-3.5 w-3.5 text-muted-foreground" /></div><p className="mt-2 text-xl font-semibold tabular-nums">{value}</p><p className="mt-1 truncate text-[11px] text-muted-foreground">{detail}</p></div>; }
function Datum({ label, value, title }: { label: string; value: string; title?: string }) { return <div><div className="text-muted-foreground">{label}</div><div className="mt-1 font-medium tabular-nums" title={title}>{value}</div></div>; }
function Header({ children, width, onClick, active, icon: Icon }: { children: React.ReactNode; width: string; onClick: () => void; active: boolean; icon: typeof ArrowDown }) { return <th className={`${width} px-4 py-3 font-medium`}><button type="button" onClick={onClick} className="inline-flex items-center gap-1 hover:text-foreground">{children}{active && <Icon className="h-3 w-3" />}</button></th>; }
