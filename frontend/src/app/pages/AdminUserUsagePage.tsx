import React, { useEffect, useMemo, useState } from 'react';
import { ArrowLeft, Coins, Layers3, MessageSquare, Monitor, RefreshCw, Search, ShieldCheck, Workflow } from 'lucide-react';
import { api } from '../lib/api';
import type { AdminConversationUsage, AdminUserUsageDetail } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { Button, Input } from '../components/ui';

type Range = 7 | 30 | 90 | 0;
type Kind = 'all' | AdminConversationUsage['kind'];

function compact(value: number, digits = 1) {
  const unit = [{ n: 1e12, s: 'T' }, { n: 1e9, s: 'B' }, { n: 1e6, s: 'M' }, { n: 1e3, s: 'K' }].find((item) => Math.abs(value) >= item.n);
  if (!unit) return value.toLocaleString();
  return `${(value / unit.n).toFixed(Math.abs(value) >= unit.n * 100 ? 0 : digits).replace(/\.0$/, '')}${unit.s}`;
}

function statusStyle(status: string) {
  if (['running', 'queued', 'canceling'].includes(status)) return 'border-sky-500/30 bg-sky-500/10 text-sky-600 dark:text-sky-400';
  if (['completed', 'succeeded'].includes(status)) return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400';
  if (['failed', 'canceled'].includes(status)) return 'border-rose-500/30 bg-rose-500/10 text-rose-600 dark:text-rose-400';
  return 'border-border bg-muted/40 text-muted-foreground';
}

export function AdminUserUsagePage({ userID, t, navigate }: { userID: string; t: UIText; navigate: (path: string) => void }) {
  const [detail, setDetail] = useState<AdminUserUsageDetail | null>(null);
  const [range, setRange] = useState<Range>(30);
  const [kind, setKind] = useState<Kind>('all');
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = async () => {
    setLoading(true); setError('');
    try {
      const timezoneOffset = new Date().getTimezoneOffset();
      const data = await api<{ detail: AdminUserUsageDetail }>(`/api/admin/users/${encodeURIComponent(userID)}/usage?days=${range}&timezoneOffset=${timezoneOffset}`);
      setDetail(data.detail);
    } catch (err) {
      setError(err instanceof Error ? err.message : t.failedLoadOrchestration);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => { void load(); }, [range, userID]);

  const conversations = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return (detail?.conversations || []).filter((item) => (kind === 'all' || item.kind === kind) && (!needle || item.title.toLowerCase().includes(needle) || item.agentName.toLowerCase().includes(needle)));
  }, [detail, kind, query]);
  const chats = detail?.conversations.filter((item) => item.kind === 'chat').length || 0;
  const orchestrations = detail?.conversations.filter((item) => item.kind === 'orchestration').length || 0;
  const ranges: { value: Range; label: string }[] = [{ value: 7, label: t.last7Days }, { value: 30, label: t.last30Days }, { value: 90, label: t.last90Days }, { value: 0, label: t.allTime }];
  const kinds: { value: Kind; label: string }[] = [{ value: 'all', label: t.allConversations }, { value: 'chat', label: t.chatConversations }, { value: 'orchestration', label: t.orchestrationConversations }];

  return <div className="min-h-screen overflow-x-hidden bg-background text-foreground">
    <header className="flex min-h-14 items-center justify-between gap-3 border-b border-border px-4 py-2 md:px-8">
      <div className="flex min-w-0 items-center gap-3"><ShieldCheck className="h-5 w-5 shrink-0 text-primary" /><div className="min-w-0"><h1 className="truncate text-sm font-semibold">{t.userConversationDetails}</h1><p className="truncate text-xs text-muted-foreground">{detail?.username || userID}</p></div></div>
      <div className="flex shrink-0 items-center gap-2"><Button variant="ghost" size="icon" onClick={() => void load()} disabled={loading} aria-label={t.refresh} title={t.refresh}><RefreshCw className={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /></Button><Button variant="secondary" size="sm" onClick={() => navigate('/admin/usage')}><ArrowLeft className="mr-1.5 h-3.5 w-3.5" /><span className="hidden sm:inline">{t.backToAdminDashboard}</span></Button></div>
    </header>
    <main className="mx-auto max-w-7xl space-y-5 p-4 md:p-8">
      {error && <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>}
      <section className="flex flex-col gap-4 border-b border-border pb-5 lg:flex-row lg:items-end lg:justify-between">
        <div className="min-w-0"><p className="text-xs font-medium uppercase text-muted-foreground">{t.adminDashboard}</p><h2 className="mt-1 truncate text-xl font-semibold">{detail?.username || t.userConversationDetails}</h2><p className="mt-1 text-sm text-muted-foreground">{t.userConversationDetailsSubtitle}</p></div>
        <Control label={t.timeRange}>{ranges.map((item) => <Choice key={item.value} active={range === item.value} onClick={() => setRange(item.value)}>{item.label}</Choice>)}</Control>
      </section>
      {loading && !detail ? <div className="flex justify-center p-16"><RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" /></div> : detail ? <>
        <section className="grid gap-px overflow-hidden rounded-lg border border-border bg-border sm:grid-cols-2 lg:grid-cols-4">
          <Summary icon={MessageSquare} label={t.sessionsMetric} value={chats.toLocaleString()} detail={t.chatConversations} />
          <Summary icon={Workflow} label={t.orchestrationRunsMetric} value={orchestrations.toLocaleString()} detail={t.orchestrationConversations} />
          <Summary icon={Layers3} label={t.totalTokens} value={compact(detail.totalTokens)} title={detail.totalTokens.toLocaleString()} detail={`${detail.callCount.toLocaleString()} ${t.callsMetric}`} />
          <Summary icon={Coins} label={t.estimatedCost} value={detail.callCount ? `$${detail.estimatedCostUsd.toFixed(2)}${detail.costKnown ? '' : '*'}` : '—'} detail={detail.costKnown ? t.officialCatalog : t.costIncomplete} />
        </section>
        <div className="flex items-start gap-2 rounded-md border border-border bg-muted/20 px-3 py-2.5 text-xs text-muted-foreground"><ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0" /><span>{t.readOnlyMetadataNotice}</span></div>
        <section>
          <div className="mb-3 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between"><div><h3 className="text-sm font-semibold">{t.userConversationDetails}</h3><p className="mt-1 text-xs text-muted-foreground">{conversations.length} / {detail.conversations.length}</p></div><div className="flex flex-col gap-3 sm:flex-row"><Control label={t.conversationType}>{kinds.map((item) => <Choice key={item.value} active={kind === item.value} onClick={() => setKind(item.value)}>{item.label}</Choice>)}</Control><div><div className="mb-1.5 text-[11px] text-muted-foreground">{t.search}</div><div className="relative w-full sm:w-64"><Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" /><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t.searchConversations} className="h-9 pl-9" /></div></div></div></div>
          <div className="hidden overflow-hidden rounded-lg border border-border bg-card md:block"><table className="w-full table-fixed text-left text-xs"><thead className="border-b border-border bg-muted/20 text-muted-foreground"><tr><th className="w-[29%] px-4 py-3 font-medium">{t.conversationTitle}</th><th className="w-[13%] px-4 py-3 font-medium">{t.conversationType}</th><th className="w-[16%] px-4 py-3 font-medium">{t.status}</th><th className="w-[16%] px-4 py-3 font-medium">{t.endpointsMetric}</th><th className="w-[14%] px-4 py-3 font-medium">{t.totalTokens}</th><th className="w-[12%] px-4 py-3 font-medium">{t.estimatedCost}</th></tr></thead><tbody className="divide-y divide-border">{conversations.map((item) => <ConversationRow key={`${item.kind}:${item.id}`} item={item} t={t} />)}</tbody></table></div>
          <div className="space-y-2 md:hidden">{conversations.map((item) => <ConversationCard key={`${item.kind}:${item.id}`} item={item} t={t} />)}</div>
          {!conversations.length && <div className="rounded-lg border border-dashed border-border p-12 text-center text-sm text-muted-foreground">{t.noConversationsFound}</div>}
        </section>
      </> : null}
    </main>
  </div>;
}

function ConversationRow({ item, t }: { item: AdminConversationUsage; t: UIText }) {
  const Icon = item.kind === 'chat' ? MessageSquare : Workflow;
  return <tr className="hover:bg-muted/20"><td className="px-4 py-3"><div className="flex min-w-0 items-start gap-2.5"><Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" /><div className="min-w-0"><div className="truncate text-sm font-medium" title={item.title}>{item.title || '—'}</div><div className="mt-1 text-muted-foreground" title={new Date(item.updatedAt * 1000).toLocaleString()}>{new Date(item.updatedAt * 1000).toLocaleString()}</div></div></div></td><td className="px-4 py-3"><div className="font-medium">{item.kind === 'chat' ? t.chatConversations : t.orchestrationConversations}</div>{item.mode && <div className="mt-1 text-muted-foreground">{item.mode} · {item.maxTurns}</div>}</td><td className="px-4 py-3"><Status status={item.status} /><div className="mt-1.5 text-muted-foreground">{item.activityCount.toLocaleString()} {t.activityCountMetric}</div></td><td className="px-4 py-3"><div className="truncate font-medium" title={item.agentName}>{item.agentName || '—'}</div><div className="mt-1 text-muted-foreground"><Monitor className="mr-1 inline h-3 w-3" />{t.endpointsMetric}</div></td><td className="px-4 py-3"><div className="font-medium tabular-nums" title={item.totalTokens.toLocaleString()}>{compact(item.totalTokens)}</div><div className="mt-1 text-muted-foreground">{item.callCount.toLocaleString()} {t.callsMetric}</div></td><td className="px-4 py-3 font-medium tabular-nums">{item.callCount ? `$${item.estimatedCostUsd.toFixed(4)}${item.costKnown ? '' : '*'}` : '—'}</td></tr>;
}

function ConversationCard({ item, t }: { item: AdminConversationUsage; t: UIText }) {
  const Icon = item.kind === 'chat' ? MessageSquare : Workflow;
  return <article className="rounded-lg border border-border bg-card p-4"><div className="flex min-w-0 items-start justify-between gap-3"><div className="flex min-w-0 items-start gap-2.5"><Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" /><div className="min-w-0"><h4 className="break-words text-sm font-semibold">{item.title || '—'}</h4><p className="mt-1 truncate text-xs text-muted-foreground">{item.agentName || '—'}</p></div></div><Status status={item.status} /></div><div className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-xs"><Datum label={t.conversationType} value={item.kind === 'chat' ? t.chatConversations : t.orchestrationConversations} /><Datum label={t.activityCountMetric} value={item.activityCount.toLocaleString()} /><Datum label={t.totalTokens} value={compact(item.totalTokens)} title={item.totalTokens.toLocaleString()} /><Datum label={t.estimatedCost} value={item.callCount ? `$${item.estimatedCostUsd.toFixed(4)}${item.costKnown ? '' : '*'}` : '—'} /></div><p className="mt-3 text-[11px] text-muted-foreground">{new Date(item.updatedAt * 1000).toLocaleString()}</p></article>;
}

function Status({ status }: { status: string }) { return <span className={`inline-flex max-w-full rounded-full border px-2 py-0.5 text-[11px] font-medium ${statusStyle(status)}`}>{status}</span>; }
function Control({ label, children }: { label: string; children: React.ReactNode }) { return <div><div className="mb-1.5 text-[11px] text-muted-foreground">{label}</div><div className="flex max-w-full overflow-x-auto rounded-md border border-border bg-muted/30 p-0.5">{children}</div></div>; }
function Choice({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) { return <button type="button" onClick={onClick} className={`h-7 whitespace-nowrap rounded px-2.5 text-xs transition-colors ${active ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{children}</button>; }
function Summary({ icon: Icon, label, value, detail, title }: { icon: typeof MessageSquare; label: string; value: string; detail: string; title?: string }) { return <div className="min-w-0 bg-card p-4" title={title}><div className="flex items-center justify-between gap-2"><p className="min-w-0 text-xs text-muted-foreground">{label}</p><Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /></div><p className="mt-2 text-xl font-semibold tabular-nums">{value}</p><p className="mt-1 break-words text-[11px] leading-relaxed text-muted-foreground">{detail}</p></div>; }
function Datum({ label, value, title }: { label: string; value: string; title?: string }) { return <div><div className="text-muted-foreground">{label}</div><div className="mt-1 font-medium tabular-nums" title={title}>{value}</div></div>; }
