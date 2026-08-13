import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Check,
  Circle,
  CircleDashed,
  LoaderCircle,
  OctagonAlert,
  Pause,
  X,
} from 'lucide-react';
import {
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  useNodesInitialized,
  useReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { cn } from '../lib/utils';

export type OrchestrationProgressStatus =
  | 'pending'
  | 'ready'
  | 'dispatching'
  | 'running'
  | 'unknown'
  | 'succeeded'
  | 'completed'
  | 'failed'
  | 'blocked'
  | 'canceling'
  | 'canceled'
  | string;

export interface OrchestrationProgressTask {
  id: string;
  name: string;
  role?: string;
  status: OrchestrationProgressStatus;
  position?: number;
  dependencies?: string[];
  detail?: string;
  durationMs?: number;
}

export interface OrchestrationProgressMapProps {
  tasks: OrchestrationProgressTask[];
  activeTaskId?: string;
  className?: string;
  height?: number | string;
  ariaLabel?: string;
  emptyLabel?: string;
  statusLabels?: Partial<Record<OrchestrationProgressStatus, string>>;
  inferSequentialDependencies?: boolean;
}

type ProgressNodeData = {
  index: number;
  task: OrchestrationProgressTask;
  active: boolean;
  statusLabel: string;
};

type ProgressNode = Node<ProgressNodeData, 'progress'>;

const NODE_WIDTH = 156;
const NODE_HEIGHT = 58;
const COLUMN_GAP = 44;
const ROW_GAP = 34;

const nodeTypes = { progress: ProgressNodeCard };

const defaultStatusLabels: Record<string, string> = {
  pending: 'Pending',
  ready: 'Ready',
  dispatching: 'Starting',
  running: 'Running',
  unknown: 'Unknown',
  succeeded: 'Completed',
  completed: 'Completed',
  failed: 'Failed',
  blocked: 'Blocked',
  canceling: 'Canceling',
  canceled: 'Canceled',
};

export function OrchestrationProgressMap({
  tasks,
  activeTaskId,
  className,
  height = 220,
  ariaLabel = 'Orchestration progress',
  emptyLabel = 'No progress nodes yet',
  statusLabels,
  inferSequentialDependencies = true,
}: OrchestrationProgressMapProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerWidth, setContainerWidth] = useState(0);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return undefined;
    const updateWidth = () => setContainerWidth(element.getBoundingClientRect().width);
    updateWidth();
    const observer = new ResizeObserver(updateWidth);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const orderedTasks = useMemo(() => tasks
    .map((task, index) => ({ task, index }))
    .sort((left, right) => (left.task.position ?? left.index) - (right.task.position ?? right.index))
    .map(({ task }) => task), [tasks]);

  const columns = columnCount(containerWidth, orderedTasks.length);
  const layout = useMemo(
    () => buildFlow(orderedTasks, columns, activeTaskId, statusLabels, inferSequentialDependencies),
    [activeTaskId, columns, inferSequentialDependencies, orderedTasks, statusLabels],
  );
  const layoutKey = `${columns}:${layout.nodes.map((node) => `${node.id}:${node.position.x}:${node.position.y}`).join('|')}`;

  return (
    <div
      ref={containerRef}
      className={cn('relative w-full min-w-0 overflow-hidden rounded-md border border-border bg-card', className)}
      style={{ height }}
      role="img"
      aria-label={ariaLabel}
    >
      {orderedTasks.length ? (
        <ReactFlow<ProgressNode, Edge>
          nodes={layout.nodes}
          edges={layout.edges}
          nodeTypes={nodeTypes}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          edgesFocusable={false}
          nodesFocusable={false}
          panOnDrag={false}
          panOnScroll={false}
          zoomOnScroll={false}
          zoomOnPinch={false}
          zoomOnDoubleClick={false}
          preventScrolling={false}
          fitView
          fitViewOptions={{ padding: 0.12, minZoom: 0.25, maxZoom: 1 }}
          minZoom={0.25}
          maxZoom={1}
          proOptions={{ hideAttribution: true }}
          disableKeyboardA11y
          className="bg-[radial-gradient(circle_at_center,color-mix(in_oklab,var(--muted-foreground)_11%,transparent)_0.7px,transparent_0.8px)] bg-[length:14px_14px]"
        >
          <FitFlowToContainer layoutKey={layoutKey} />
        </ReactFlow>
      ) : (
        <div className="flex h-full items-center justify-center px-4 text-center text-xs text-muted-foreground">
          {emptyLabel}
        </div>
      )}
    </div>
  );
}

function ProgressNodeCard({ data }: NodeProps<ProgressNode>) {
  const tone = progressStatusTone(data.task.status, data.active);
  const StatusIcon = tone.icon;
  const meta = data.task.detail || data.task.role;
  return (
    <div
      className={cn(
        'relative flex h-[58px] w-[156px] items-center gap-2 rounded-md border bg-card px-2.5 shadow-sm transition-colors',
        tone.card,
        data.active && 'ring-1 ring-emerald-500/30',
      )}
      aria-label={`${data.task.name}: ${data.statusLabel}`}
    >
      <Handle id="target-left" type="target" position={Position.Left} className="!opacity-0" />
      <Handle id="source-left" type="source" position={Position.Left} className="!opacity-0" />
      <Handle id="target-top" type="target" position={Position.Top} className="!opacity-0" />
      <Handle id="source-top" type="source" position={Position.Top} className="!opacity-0" />
      <Handle id="target-right" type="target" position={Position.Right} className="!opacity-0" />
      <Handle id="source-right" type="source" position={Position.Right} className="!opacity-0" />
      <Handle id="target-bottom" type="target" position={Position.Bottom} className="!opacity-0" />
      <Handle id="source-bottom" type="source" position={Position.Bottom} className="!opacity-0" />

      <div className={cn('flex h-7 w-7 shrink-0 items-center justify-center rounded border', tone.iconBox)}>
        <StatusIcon className={cn('h-3.5 w-3.5', tone.spin && 'animate-spin')} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="shrink-0 font-mono text-[9px] tabular-nums text-muted-foreground">
            {String(data.index + 1).padStart(2, '0')}
          </span>
          <span className="truncate text-[11px] font-semibold text-foreground" title={data.task.name}>{data.task.name}</span>
        </div>
        <div className="mt-1 flex min-w-0 items-center gap-1.5 text-[9px]">
          <span className={cn('shrink-0 rounded border px-1 py-px font-medium', tone.badge)}>{data.statusLabel}</span>
          {meta ? <span className="truncate text-muted-foreground" title={meta}>{meta}</span> : null}
          {data.task.durationMs != null ? (
            <span className="ml-auto shrink-0 font-mono text-muted-foreground">{compactDuration(data.task.durationMs)}</span>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function FitFlowToContainer({ layoutKey }: { layoutKey: string }) {
  const nodesInitialized = useNodesInitialized();
  const { fitView } = useReactFlow();

  useEffect(() => {
    if (!nodesInitialized) return undefined;
    const frame = window.requestAnimationFrame(() => {
      void fitView({ padding: 0.12, minZoom: 0.25, maxZoom: 1, duration: 180 });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [fitView, layoutKey, nodesInitialized]);

  return null;
}

function buildFlow(
  tasks: OrchestrationProgressTask[],
  columns: number,
  activeTaskId: string | undefined,
  statusLabels: OrchestrationProgressMapProps['statusLabels'],
  inferSequentialDependencies: boolean,
) {
  const positions = new Map<string, { x: number; y: number }>();
  const nodes: ProgressNode[] = tasks.map((task, index) => {
    const row = Math.floor(index / columns);
    const rawColumn = index % columns;
    const column = row % 2 === 0 ? rawColumn : columns - 1 - rawColumn;
    const position = {
      x: column * (NODE_WIDTH + COLUMN_GAP),
      y: row * (NODE_HEIGHT + ROW_GAP),
    };
    positions.set(task.id, position);
    return {
      id: task.id,
      type: 'progress',
      position,
      draggable: false,
      selectable: false,
      focusable: false,
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
      data: {
        index,
        task,
        active: task.id === activeTaskId || ['dispatching', 'running'].includes(normalizeStatus(task.status)),
        statusLabel: statusLabels?.[task.status] || defaultStatusLabels[normalizeStatus(task.status)] || task.status,
      },
    };
  });

  const taskIDs = new Set(tasks.map((task) => task.id));
  const edges: Edge[] = [];
  tasks.forEach((task, index) => {
    const dependencies = task.dependencies == null && inferSequentialDependencies && index > 0
      ? [tasks[index - 1].id]
      : task.dependencies || [];
    dependencies.filter((dependency) => taskIDs.has(dependency)).forEach((dependency) => {
      const sourcePosition = positions.get(dependency);
      const targetPosition = positions.get(task.id);
      if (!sourcePosition || !targetPosition) return;
      const handles = edgeHandles(sourcePosition, targetPosition);
      const targetStatus = normalizeStatus(task.status);
      const active = task.id === activeTaskId || ['dispatching', 'running'].includes(targetStatus);
      const completed = ['succeeded', 'completed'].includes(targetStatus);
      edges.push({
        id: `${dependency}:${task.id}`,
        source: dependency,
        target: task.id,
        sourceHandle: handles.source,
        targetHandle: handles.target,
        type: 'smoothstep',
        animated: active,
        selectable: false,
        focusable: false,
        markerEnd: {
          type: MarkerType.ArrowClosed,
          width: 10,
          height: 10,
          color: active ? '#10b981' : completed ? '#34d399' : 'color-mix(in oklab, var(--muted-foreground) 55%, transparent)',
        },
        style: {
          stroke: active ? '#10b981' : completed ? '#34d399' : 'color-mix(in oklab, var(--muted-foreground) 38%, transparent)',
          strokeWidth: active ? 1.8 : 1.25,
        },
      });
    });
  });

  return { nodes, edges };
}

function columnCount(width: number, taskCount: number) {
  if (taskCount <= 1) return 1;
  if (width >= 1080) return Math.min(taskCount, 6);
  if (width >= 560) return Math.min(taskCount, 3);
  if (width >= 340) return Math.min(taskCount, 2);
  return 1;
}

function edgeHandles(source: { x: number; y: number }, target: { x: number; y: number }) {
  const deltaX = target.x - source.x;
  const deltaY = target.y - source.y;
  if (Math.abs(deltaX) > Math.abs(deltaY)) {
    return deltaX > 0
      ? { source: 'source-right', target: 'target-left' }
      : { source: 'source-left', target: 'target-right' };
  }
  return deltaY >= 0
    ? { source: 'source-bottom', target: 'target-top' }
    : { source: 'source-top', target: 'target-bottom' };
}

function normalizeStatus(status: OrchestrationProgressStatus) {
  return String(status || 'pending').trim().toLowerCase();
}

function progressStatusTone(status: OrchestrationProgressStatus, active: boolean) {
  const normalized = normalizeStatus(status);
  if (active || ['dispatching', 'running'].includes(normalized)) {
    return {
      icon: LoaderCircle,
      spin: true,
      card: 'border-emerald-500/45 bg-emerald-500/[0.06]',
      iconBox: 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600 dark:text-emerald-300',
      badge: 'border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
    };
  }
  if (['succeeded', 'completed'].includes(normalized)) {
    return {
      icon: Check,
      spin: false,
      card: 'border-emerald-500/25 bg-emerald-500/[0.035]',
      iconBox: 'border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-300',
      badge: 'border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
    };
  }
  if (normalized === 'ready') {
    return {
      icon: Circle,
      spin: false,
      card: 'border-sky-500/30 bg-sky-500/[0.035]',
      iconBox: 'border-sky-500/20 bg-sky-500/10 text-sky-600 dark:text-sky-300',
      badge: 'border-sky-500/20 bg-sky-500/10 text-sky-700 dark:text-sky-300',
    };
  }
  if (['failed', 'blocked'].includes(normalized)) {
    return {
      icon: OctagonAlert,
      spin: false,
      card: 'border-destructive/35 bg-destructive/[0.045]',
      iconBox: 'border-destructive/20 bg-destructive/10 text-destructive',
      badge: 'border-destructive/20 bg-destructive/10 text-destructive',
    };
  }
  if (['canceling', 'canceled'].includes(normalized)) {
    return {
      icon: X,
      spin: normalized === 'canceling',
      card: 'border-amber-500/30 bg-amber-500/[0.04]',
      iconBox: 'border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300',
      badge: 'border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300',
    };
  }
  if (normalized === 'unknown') {
    return {
      icon: Pause,
      spin: false,
      card: 'border-amber-500/25 bg-amber-500/[0.035]',
      iconBox: 'border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300',
      badge: 'border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300',
    };
  }
  return {
    icon: CircleDashed,
    spin: false,
    card: 'border-border bg-card',
    iconBox: 'border-border bg-muted/40 text-muted-foreground',
    badge: 'border-border bg-muted/40 text-muted-foreground',
  };
}

function compactDuration(durationMs: number) {
  const seconds = Math.max(0, Math.round(durationMs / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  if (minutes < 60) return `${minutes}m${remainingSeconds ? `${remainingSeconds}s` : ''}`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return `${hours}h${remainingMinutes ? `${remainingMinutes}m` : ''}`;
}
