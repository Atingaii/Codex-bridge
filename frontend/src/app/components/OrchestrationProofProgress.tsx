import { useMemo, useState } from 'react';
import {
  Check,
  ChevronDown,
  CircleDashed,
  ListChecks,
  LoaderCircle,
  OctagonAlert,
} from 'lucide-react';
import type {
  OrchestrationPlanItem,
  OrchestrationPlanProgress,
} from '../lib/types';
import { cn } from '../lib/utils';

export interface OrchestrationProofProgressLabels {
  title?: string;
  completed?: string;
  active?: string;
  pending?: string;
  blocked?: string;
  ready?: string;
  empty?: string;
  evidence?: string;
  rationale?: string;
  dependency?: string;
  showCompleted?: string;
  hideCompleted?: string;
}

export interface OrchestrationProofProgressProps {
  plan?: OrchestrationPlanProgress | null;
  planItems?: OrchestrationPlanItem[];
  className?: string;
  defaultCompletedExpanded?: boolean;
  labels?: OrchestrationProofProgressLabels;
  onSelectItem?: (item: OrchestrationPlanItem) => void;
}

const defaultLabels: Required<OrchestrationProofProgressLabels> = {
  title: '整个任务计划',
  completed: '已完成',
  active: '当前',
  pending: '待证明',
  blocked: '阻塞',
  ready: '可开始',
  empty: '本次任务还没有可用的结构化计划。',
  evidence: '证据',
  rationale: '说明',
  dependency: '依赖',
  showCompleted: '展开已完成项',
  hideCompleted: '收起已完成项',
};

export function OrchestrationProofProgress({
  plan,
  planItems,
  className,
  defaultCompletedExpanded = false,
  labels,
  onSelectItem,
}: OrchestrationProofProgressProps) {
  const text = { ...defaultLabels, ...labels };
  const items = useMemo(
    () => proofObligationsFromProgress(plan?.items || planItems),
    [plan?.items, planItems],
  );
  const summary = useMemo(() => summarizeProofObligations(items, plan), [items, plan]);
  const [completedExpanded, setCompletedExpanded] = useState(defaultCompletedExpanded);
  const current = items.filter((item) => normalizePlanStatus(item.status) === 'in_progress');
  const blocked = items.filter((item) => normalizePlanStatus(item.status) === 'blocked');
  const pending = items.filter((item) => normalizePlanStatus(item.status) === 'pending');
  const completed = items.filter((item) => normalizePlanStatus(item.status) === 'completed');

  return (
    <section className={cn('min-w-0 rounded-lg border border-border bg-card/50', className)}>
      <header className="border-b border-border px-4 py-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-sm font-semibold">
              <ListChecks className="h-4 w-4 text-primary" />
              <span>{text.title}</span>
              <span className="font-mono text-[11px] font-medium tabular-nums text-muted-foreground">
                {summary.completed}/{summary.total}
              </span>
            </div>
            {plan?.goal ? <p className="mt-1.5 line-clamp-2 text-xs leading-5 text-muted-foreground" title={plan.goal}>{plan.goal}</p> : null}
          </div>
          <span className="shrink-0 font-mono text-sm font-semibold tabular-nums text-foreground">{summary.percent}%</span>
        </div>
        <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-muted" aria-label={`${summary.percent}%`}>
          <div className="h-full rounded-full bg-emerald-500 transition-[width] duration-300" style={{ width: `${summary.percent}%` }} />
        </div>
        <div className="mt-3 grid grid-cols-2 gap-1.5 sm:grid-cols-4">
          <SummaryChip label={text.completed} value={summary.completed} tone="completed" />
          <SummaryChip label={text.active} value={summary.inProgress} tone="active" />
          <SummaryChip label={text.pending} value={summary.pending} tone="pending" />
          <SummaryChip label={text.blocked} value={summary.blocked} tone="blocked" />
        </div>
      </header>

      <div className="max-h-[24rem] overflow-y-auto p-3 elegant-scrollbar">
        {!items.length ? (
          <p className="rounded-md border border-dashed border-border px-3 py-4 text-xs leading-5 text-muted-foreground">{text.empty}</p>
        ) : (
          <div className="space-y-3">
            <ProofGroup label={text.active} items={current} labels={text} onSelectItem={onSelectItem} />
            <ProofGroup label={text.blocked} items={blocked} labels={text} onSelectItem={onSelectItem} />
            <ProofGroup label={text.pending} items={pending} labels={text} onSelectItem={onSelectItem} />
            {completed.length ? (
              <div>
                <button
                  type="button"
                  className="flex w-full items-center justify-between rounded-md px-1 py-1 text-[11px] font-medium text-muted-foreground hover:text-foreground"
                  onClick={() => setCompletedExpanded((value) => !value)}
                  aria-expanded={completedExpanded}
                >
                  <span>{completedExpanded ? text.hideCompleted : text.showCompleted} · {completed.length}</span>
                  <ChevronDown className={cn('h-3.5 w-3.5 transition-transform', completedExpanded && 'rotate-180')} />
                </button>
                {completedExpanded ? <ProofGroup items={completed} labels={text} onSelectItem={onSelectItem} /> : null}
              </div>
            ) : null}
          </div>
        )}
      </div>
    </section>
  );
}

function SummaryChip({ label, value, tone }: { label: string; value: number; tone: 'completed' | 'active' | 'pending' | 'blocked' }) {
  return (
    <div className={cn(
      'flex min-w-0 items-center justify-between gap-2 rounded-md border px-2 py-1.5 text-[10px]',
      tone === 'completed' && 'border-emerald-500/20 bg-emerald-500/[0.06] text-emerald-700 dark:text-emerald-300',
      tone === 'active' && 'border-sky-500/20 bg-sky-500/[0.06] text-sky-700 dark:text-sky-300',
      tone === 'pending' && 'border-border bg-muted/35 text-muted-foreground',
      tone === 'blocked' && 'border-destructive/20 bg-destructive/[0.05] text-destructive',
    )}>
      <span className="truncate">{label}</span>
      <span className="font-mono font-semibold tabular-nums">{value}</span>
    </div>
  );
}

function ProofGroup({
  label,
  items,
  labels,
  onSelectItem,
}: {
  label?: string;
  items: OrchestrationPlanItem[];
  labels: Required<OrchestrationProofProgressLabels>;
  onSelectItem?: (item: OrchestrationPlanItem) => void;
}) {
  if (!items.length) return null;
  return (
    <div className="space-y-1.5">
      {label ? <p className="px-1 text-[10px] font-semibold uppercase text-muted-foreground">{label} · {items.length}</p> : null}
      {items.map((item) => <ProofRow key={item.id} item={item} labels={labels} onSelect={onSelectItem} />)}
    </div>
  );
}

function ProofRow({
  item,
  labels,
  onSelect,
}: {
  item: OrchestrationPlanItem;
  labels: Required<OrchestrationProofProgressLabels>;
  onSelect?: (item: OrchestrationPlanItem) => void;
}) {
  const status = normalizePlanStatus(item.status);
  const tone = proofTone(status);
  const StatusIcon = tone.icon;
  const ready = status === 'pending' && item.ready;
  const body = (
    <>
      <div className={cn('mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded border', tone.iconBox)}>
        <StatusIcon className={cn('h-3 w-3', tone.spin && 'animate-spin')} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs font-medium leading-5 text-foreground">{item.title}</span>
          {ready ? <MetaBadge>{labels.ready}</MetaBadge> : null}
          {item.branch ? <MetaBadge>{item.branch}</MetaBadge> : null}
          {item.difficulty ? <MetaBadge>{difficultyLabel(item.difficulty)}</MetaBadge> : null}
          {item.priority ? <MetaBadge>{`优先级 ${item.priority}`}</MetaBadge> : null}
          {item.progress != null && status !== 'completed' ? <MetaBadge>{`${clampPercent(item.progress)}%`}</MetaBadge> : null}
        </div>
        {item.rationale ? <Detail label={labels.rationale} value={item.rationale} /> : null}
        {item.evidence ? <Detail label={labels.evidence} value={item.evidence} /> : null}
        {item.blockedBy?.length ? <Detail label={labels.dependency} value={item.blockedBy.join(', ')} destructive /> : null}
      </div>
    </>
  );

  const className = cn(
    'flex w-full items-start gap-2.5 rounded-md border border-border/70 bg-background/70 px-3 py-2.5 text-left',
    onSelect && 'transition-colors hover:border-primary/35 hover:bg-muted/35',
  );
  return onSelect ? <button type="button" className={className} onClick={() => onSelect(item)}>{body}</button> : <div className={className}>{body}</div>;
}

function MetaBadge({ children }: { children: string | number }) {
  return <span className="rounded border border-border bg-muted/40 px-1 py-px text-[9px] font-medium text-muted-foreground">{children}</span>;
}

function Detail({ label, value, destructive = false }: { label: string; value: string; destructive?: boolean }) {
  return <p className={cn('mt-1 line-clamp-2 break-words text-[10px] leading-4 text-muted-foreground', destructive && 'text-destructive')} title={value}><span className="font-medium">{label}:</span> {value}</p>;
}

export function proofObligationsFromProgress(
  planItems?: OrchestrationPlanItem[] | null,
): OrchestrationPlanItem[] {
  return planItems?.length ? planItems : [];
}

export function summarizeProofObligations(
  items: OrchestrationPlanItem[],
  projection?: OrchestrationPlanProgress | null,
) {
  const summary = { total: items.length, completed: 0, inProgress: 0, pending: 0, blocked: 0, ready: 0, percent: 0 };
  items.forEach((item) => {
    const status = normalizePlanStatus(item.status);
    if (status === 'completed') summary.completed += 1;
    else if (status === 'in_progress') summary.inProgress += 1;
    else if (status === 'blocked') summary.blocked += 1;
    else {
      summary.pending += 1;
      if (item.ready) summary.ready += 1;
    }
  });
  summary.percent = summary.total ? Math.round(summary.completed * 100 / summary.total) : 0;
  if (projection && projection.total === summary.total) {
    summary.percent = clampPercent(projection.percent ?? summary.percent);
  }
  return summary;
}

function normalizePlanStatus(status: string) {
  const normalized = String(status || 'pending').toLowerCase();
  if (['succeeded', 'completed'].includes(normalized)) return 'completed';
  if (['running', 'dispatching', 'in_progress'].includes(normalized)) return 'in_progress';
  if (['failed', 'blocked', 'canceled'].includes(normalized)) return 'blocked';
  return 'pending';
}

function proofTone(status: string) {
  if (status === 'completed') return { icon: Check, spin: false, iconBox: 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600 dark:text-emerald-300' };
  if (status === 'in_progress') return { icon: LoaderCircle, spin: true, iconBox: 'border-sky-500/25 bg-sky-500/10 text-sky-600 dark:text-sky-300' };
  if (status === 'blocked') return { icon: OctagonAlert, spin: false, iconBox: 'border-destructive/25 bg-destructive/10 text-destructive' };
  return { icon: CircleDashed, spin: false, iconBox: 'border-border bg-muted/40 text-muted-foreground' };
}

function difficultyLabel(value: string) {
  const normalized = value.toLowerCase();
  if (normalized === 'easy') return '简单';
  if (normalized === 'medium') return '中等';
  if (normalized === 'hard') return '困难';
  if (normalized === 'critical') return '关键';
  return value;
}

function clampPercent(value: number) {
  return Math.max(0, Math.min(100, Math.round(value)));
}
