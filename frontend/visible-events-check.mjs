import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const utilsSource = readFileSync(new URL('./src/app/lib/utils.ts', import.meta.url), 'utf8');
const orchestrationComponentsSource = readFileSync(new URL('./src/app/components/OrchestrationComponents.tsx', import.meta.url), 'utf8');
const orchestrationWorkspaceSource = readFileSync(new URL('./src/app/pages/OrchestrationWorkspace.tsx', import.meta.url), 'utf8');
const mainSource = readFileSync(new URL('./src/main.tsx', import.meta.url), 'utf8');
const indexSource = readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const recoverySource = readFileSync(new URL('./public/app-recovery.js', import.meta.url), 'utf8');
const legacyBundleSource = readFileSync(new URL('./public/assets/index-BWIkJOjq.js', import.meta.url), 'utf8');
const source = `${utilsSource}\n${orchestrationComponentsSource}\n${orchestrationWorkspaceSource}`;

assert.match(source, /function orchestrationStatusContent\(event: OrchestrationEvent\)/);
assert.match(source, /if \(\(event\.kind === 'run\.end' \|\| event\.kind === 'run\.error'\) && content\) return content;/);
assert.match(source, /role: 'summary'/);
assert.match(source, /function isBridgeRelayNotice\(event: Pick<OrchestrationEvent, 'kind' \| 'source' \| 'severity'>\)/);
assert.match(source, /function canMergeAdjacentOrchestrationDelta\(previous: OrchestrationEvent \| undefined, event: OrchestrationEvent\)/);
assert.match(source, /function orchestrationTurnDeltaContentByKey\(events: OrchestrationEvent\[\]\)/);
assert.match(source, /function turnEndDisplayContent\(content: string, deltaContent: string\)/);
assert.match(source, /export function orchestrationTimelineGroups\(\s*items: OrchestrationTimelineItem\[\]/);
assert.match(source, /export function OrchestrationTimelineGroupItem\(/);
assert.match(source, /export function groupedOrchestrationTimelineItems\(items: OrchestrationTimelineItem\[\]\)/);
assert.match(source, /<CommandEventBatch key=\{block\.key\} items=\{block\.items\} t=\{t\} \/>/);
assert.match(source, /turnMissingEndDescription/);
assert.match(source, /function completedOrchestrationTurnGroupKeys\(events: OrchestrationEvent\[\], runId\?: string\)/);
assert.match(source, /const completeTurnKeys = completedOrchestrationTurnGroupKeys\(events, run\?\.id\);/);
assert.match(source, /group\.incomplete = terminalRun && !group\.complete && group\.items\.length > 0;/);
assert.match(source, /visible\.push\(statusVisibleEvent\(event, index, ':status'\)\);/);
assert.doesNotMatch(source, /contentfulTurnEnds\.has\(orchestrationTurnKey\(event\)\)/);
assert.match(source, /const rawContent = item\.content \|\| item\.error \|\| '';/);
assert.match(utilsSource, /export function upsertApprovalItem\(/);
assert.match(utilsSource, /export function updateApprovalItemStatus\(/);
assert.match(orchestrationWorkspaceSource, /upsertApprovalItem,/);
assert.match(orchestrationWorkspaceSource, /updateApprovalItemStatus,/);
assert.match(indexSource, /src="\/app-recovery\.js"/);
assert.match(mainSource, /__codexBridgeAppReady/);
assert.match(recoverySource, /window\.__codexBridgeAppReady = function \(\)/);
assert.match(recoverySource, /function isApplicationScript\(target\)/);
assert.match(recoverySource, /if \(target && target !== window\) \{\s*return;\s*\}/);
assert.match(recoverySource, /navigator\.serviceWorker\.getRegistrations/);
assert.match(recoverySource, /window\.caches\.delete/);
assert.match(recoverySource, /legacyEntryRecoveryAttempts/);
assert.match(recoverySource, /index-BzGp0PoF\.js/);
assert.match(recoverySource, /function recoverLegacyEntry\(script, reason\)/);
assert.match(recoverySource, /document\.addEventListener\("DOMContentLoaded", checkLegacyEntryScripts\)/);
assert.match(legacyBundleSource, /legacyBundleRecovery\.index-BWIkJOjq/);
assert.match(legacyBundleSource, /renderFallback\(reason\)/);
assert.match(legacyBundleSource, /currentStylesheetSources\(html\)\.forEach\(loadStylesheet\)/);
assert.match(legacyBundleSource, /return import\(moduleURL\.href\)/);
assert.doesNotMatch(source, /unresolvedAcceptanceSummary/);
assert.doesNotMatch(source, /hasUnresolvedAcceptanceSignal/);
assert.doesNotMatch(source, /Unmet acceptance|未满足验收/);

function groupedTimelineItems(items) {
  const blocks = [];
  let pendingCommands = [];
  const flushCommands = () => {
    if (pendingCommands.length === 0) return;
    if (pendingCommands.length === 1) {
      blocks.push({ type: 'item', item: pendingCommands[0] });
    } else {
      blocks.push({
        type: 'command-batch',
        key: `command-batch:${pendingCommands[0].key}:${pendingCommands[pendingCommands.length - 1].key}`,
        items: pendingCommands,
      });
    }
    pendingCommands = [];
  };
  items.forEach((item) => {
    if (item.type === 'event' && item.event.type === 'command') {
      pendingCommands.push(item);
      return;
    }
    flushCommands();
    blocks.push({ type: 'item', item });
  });
  flushCommands();
  return blocks;
}

const timelineBlocks = groupedTimelineItems([
  { type: 'event', key: 'm1', event: { type: 'message' } },
  { type: 'event', key: 'c1', event: { type: 'command' } },
  { type: 'event', key: 'c2', event: { type: 'command' } },
  { type: 'approval', key: 'a1', approval: {} },
  { type: 'event', key: 'c3', event: { type: 'command' } },
]);

assert.deepEqual(timelineBlocks.map((block) => block.type), ['item', 'command-batch', 'item', 'item']);
assert.equal(timelineBlocks[1].items.length, 2);
assert.equal(timelineBlocks[1].key, 'command-batch:c1:c2');
assert.equal(timelineBlocks[3].item.key, 'c3');

function timelineTurnGroupKey(meta) {
  return `turn:${meta.runId || ''}:${meta.turnId || ''}`;
}

function timelineItemTurnMeta(item) {
  return {
    runId: item.event.runId,
    turnId: item.event.turnId,
    role: item.event.role,
    cli: item.event.cli,
  };
}

function timelineGroups(items, run, events) {
  const completeTurnKeys = new Set(
    events
      .filter((event) => event.kind === 'turn.end' && event.runId === run.id && event.turnId)
      .map((event) => timelineTurnGroupKey(event))
  );
  const terminalClosedTurnKeys = terminalClosedTurnGroupKeys(events, run.id);
  const groups = new Map();
  items.forEach((item) => {
    const meta = timelineItemTurnMeta(item);
    const key = timelineTurnGroupKey(meta);
    const previous = groups.get(key);
    const group = previous || {
      type: 'turn',
      key,
      runId: meta.runId,
      turnId: meta.turnId,
      role: meta.role,
      cli: meta.cli,
      items: [],
      complete: false,
      incomplete: false,
    };
    group.role = group.role || meta.role;
    group.cli = group.cli || meta.cli;
    group.items.push(item);
    group.complete = group.complete || item.event.kind === 'turn.end';
    groups.set(key, group);
  });
  groups.forEach((group) => {
    const terminalClosed = terminalClosedTurnKeys.has(group.key);
    group.complete = group.complete || completeTurnKeys.has(group.key) || terminalClosed;
    group.hasError = Boolean(group.hasError || terminalClosed);
    group.incomplete = (run.status === 'completed' || run.status === 'failed' || run.status === 'canceled') && !group.complete && group.items.length > 0;
  });
  return Array.from(groups.values());
}

function terminalClosedTurnGroupKeys(events, runId) {
  const completeTurnKeys = new Set(
    events
      .filter((event) => event.kind === 'turn.end' && event.runId === runId && event.turnId)
      .map((event) => timelineTurnGroupKey(event))
  );
  const keys = new Set();
  let latestTurn = null;
  events
    .filter((event) => event.runId === runId)
    .sort((a, b) => (a.seq || 0) - (b.seq || 0))
    .forEach((event) => {
      if (event.turnId) latestTurn = { runId: event.runId, turnId: event.turnId };
      if ((event.kind !== 'run.error' && event.kind !== 'run.cancelled') || !latestTurn) return;
      const key = timelineTurnGroupKey(latestTurn);
      if (!completeTurnKeys.has(key)) keys.add(key);
    });
  return keys;
}

const mixedMetaEvents = [
  { runId: 'run1', turnId: 'run1-01', kind: 'command.end', status: 'completed' },
  { runId: 'run1', turnId: 'run1-01', role: 'reviewer', cli: 'codex', kind: 'turn.end', status: 'success', content: '最终结论：已收尾。' },
];
const mixedMetaGroups = timelineGroups(
  mixedMetaEvents.map((event, index) => ({ type: 'event', key: `event:${index}`, event })),
  { id: 'run1', status: 'completed' },
  mixedMetaEvents
);

assert.equal(mixedMetaGroups.length, 1);
assert.equal(mixedMetaGroups[0].key, 'turn:run1:run1-01');
assert.equal(mixedMetaGroups[0].cli, 'codex');
assert.equal(mixedMetaGroups[0].complete, true);
assert.equal(mixedMetaGroups[0].incomplete, false);

const legacyDisconnectEvents = [
  { runId: 'run2', turnId: 'run2-01', role: 'implementer', cli: 'claude', kind: 'turn.start', seq: 1 },
  { runId: 'run2', turnId: 'run2-01', role: 'implementer', cli: 'claude', kind: 'command.end', seq: 2, status: 'completed' },
  { runId: 'run2', kind: 'run.error', seq: 3, status: 'failed', error: 'bridge disconnected while run was active' },
];
const legacyDisconnectGroups = timelineGroups(
  legacyDisconnectEvents
    .filter((event) => event.turnId)
    .map((event, index) => ({ type: 'event', key: `legacy:${index}`, event })),
  { id: 'run2', status: 'failed' },
  legacyDisconnectEvents
);

assert.equal(legacyDisconnectGroups.length, 1);
assert.equal(legacyDisconnectGroups[0].complete, true);
assert.equal(legacyDisconnectGroups[0].hasError, true);
assert.equal(legacyDisconnectGroups[0].incomplete, false);

const resumedRunEvents = [
  { runId: 'run3', kind: 'user.message', seq: 1 },
  { runId: 'run3', kind: 'run.start', seq: 2, status: 'running' },
  { runId: 'run3', turnId: 'run3-p001-01', role: 'reviewer', cli: 'codex', kind: 'turn.start', seq: 3 },
  { runId: 'run3', turnId: 'run3-p001-01', role: 'reviewer', cli: 'codex', kind: 'command.end', seq: 4, status: 'completed' },
  { runId: 'run3', turnId: 'run3-p001-01', role: 'reviewer', cli: 'codex', kind: 'turn.end', seq: 5, status: 'success', content: '最终结论：第一轮已收尾。' },
  { runId: 'run3', turnId: 'run3-p001-02', role: 'implementer', cli: 'claude', kind: 'turn.start', seq: 6 },
  { runId: 'run3', kind: 'run.error', seq: 7, status: 'failed', error: 'bridge disconnected while run was active' },
  { runId: 'run3', turnId: 'run3-p001-02', role: 'implementer', cli: 'claude', kind: 'turn.end', seq: 8, status: 'error', error: 'server_error' },
  { runId: 'run3', kind: 'user.message', seq: 9 },
  { runId: 'run3', kind: 'run.start', seq: 10, status: 'running' },
  { runId: 'run3', turnId: 'run3-p009-01', role: 'reviewer', cli: 'codex', kind: 'turn.start', seq: 11 },
  { runId: 'run3', turnId: 'run3-p009-01', role: 'reviewer', cli: 'codex', kind: 'command.end', seq: 12, status: 'completed' },
  { runId: 'run3', turnId: 'run3-p009-01', role: 'reviewer', cli: 'codex', kind: 'turn.end', seq: 13, status: 'success', content: '最终结论：继续后的第一轮已收尾。' },
  { runId: 'run3', turnId: 'run3-p009-02', role: 'implementer', cli: 'claude', kind: 'turn.start', seq: 14 },
  { runId: 'run3', turnId: 'run3-p009-02', role: 'implementer', cli: 'claude', kind: 'turn.end', seq: 15, status: 'error', error: 'authentication_failed' },
  { runId: 'run3', kind: 'run.error', seq: 16, status: 'failed', error: 'authentication_failed' },
];
const resumedRunGroups = timelineGroups(
  resumedRunEvents
    .filter((event) => event.turnId)
    .map((event, index) => ({ type: 'event', key: `resumed:${index}`, event })),
  { id: 'run3', status: 'failed' },
  resumedRunEvents
);
const resumedFirstTurn = resumedRunGroups.find((group) => group.turnId === 'run3-p009-01');

assert.ok(resumedFirstTurn);
assert.equal(resumedFirstTurn.complete, true);
assert.equal(resumedFirstTurn.incomplete, false);
assert.ok(!resumedRunGroups.some((group) => group.turnId && group.incomplete));

function stringsTrim(value) {
  return typeof value === 'string' ? value.trim() : '';
}

function orchestrationStatusContent(event) {
  const content = stringsTrim(event.content);
  const error = stringsTrim(event.error);
  if ((event.kind === 'run.end' || event.kind === 'run.error') && content) return content;
  return error || content || stringsTrim(event.status) || event.kind;
}

function orchestrationTurnKey(event) {
  return `${event.runId}:${event.turnId || ''}:${event.role || ''}:${event.cli || ''}`;
}

function isBridgeRelayNotice(event) {
  return event.source === 'bridge' && (event.kind === 'turn.delta' || event.kind === 'run.conclusion' || Boolean(event.severity));
}

function mergeDeltaContent(previous, next) {
  if (!previous) return next;
  if (!next) return previous;
  if (next.startsWith(previous)) return next;
  if (previous.endsWith(next)) return previous;
  return previous + next;
}

function canMergeAdjacentOrchestrationDelta(previous, event) {
  if (!previous || previous.kind !== 'turn.delta') return false;
  return orchestrationTurnKey(previous) === orchestrationTurnKey(event)
    && isBridgeRelayNotice(previous) === isBridgeRelayNotice(event);
}

function mergeOrchestrationDeltaEvents(events) {
  const merged = [];
  events.forEach((event) => {
    if (event.kind !== 'turn.delta') {
      merged.push(event);
      return;
    }
    const content = String(event.content || '');
    if (!stringsTrim(content)) return;
    if (isBridgeRelayNotice(event)) {
      merged.push({ ...event, content });
      return;
    }
    const previous = merged[merged.length - 1];
    if (!canMergeAdjacentOrchestrationDelta(previous, event)) {
      merged.push({ ...event, content });
      return;
    }
    merged[merged.length - 1] = {
      ...previous,
      content: mergeDeltaContent(previous.content || '', content),
      status: event.status || previous.status,
      error: event.error || previous.error,
      createdAt: previous.createdAt || event.createdAt,
      seq: previous.seq || event.seq,
    };
  });
  return merged;
}

function turnEndDisplayContent(content, deltaContent) {
  const value = stringsTrim(content);
  const deltas = stringsTrim(deltaContent);
  if (!value || !deltas) return value;
  return '';
}

function visibleEventKindsAndContent(events) {
  const ordered = mergeOrchestrationDeltaEvents(events);
  const deltaContent = new Map();
  ordered.forEach((event) => {
    if (event.kind !== 'turn.delta' || isBridgeRelayNotice(event)) return;
    const content = stringsTrim(event.content);
    if (!content) return;
    const key = orchestrationTurnKey(event);
    deltaContent.set(key, mergeDeltaContent(deltaContent.get(key) || '', content));
  });
  return ordered.flatMap((event) => {
    if (event.kind === 'turn.delta') {
      const content = stringsTrim(event.content);
      return content ? [{ kind: event.kind, seq: event.seq, content }] : [];
    }
    if (event.kind.startsWith('command.')) {
      return [{ kind: event.kind, seq: event.seq, content: event.status || '' }];
    }
    if (event.kind === 'turn.end') {
      const content = turnEndDisplayContent(event.content, deltaContent.get(orchestrationTurnKey(event)) || '');
      const out = content ? [{ kind: event.kind, seq: event.seq, content }] : [];
      if (event.error) out.push({ kind: event.kind, seq: event.seq, content: orchestrationStatusContent(event), type: 'status' });
      return out;
    }
    return [];
  });
}

const failedAssessment = orchestrationStatusContent({
  kind: 'run.error',
  content: '最终测试结果：未通过\n\n验收维度：\n- 假设审计：未通过',
  error: 'formal proof assessment failed: missing assumption audit',
  status: 'failed',
});

assert.ok(failedAssessment.includes('最终测试结果：未通过'));
assert.ok(failedAssessment.includes('验收维度'));
assert.ok(!failedAssessment.startsWith('formal proof assessment failed'));

const commandError = orchestrationStatusContent({
  kind: 'command.end',
  content: 'tool output',
  error: 'command failed',
  status: 'failed',
});

assert.equal(commandError, 'command failed');

function visibleTerminalSummary(events) {
  const visible = [];
  events.forEach((event, index) => {
    if (event.kind === 'turn.end' && stringsTrim(event.content)) {
      visible.push({ kind: event.kind, content: event.content, key: `turn:${index}` });
      return;
    }
    if (event.kind === 'run.end' || event.kind === 'run.error') {
      const content = orchestrationStatusContent(event);
      const duplicate = visible.some((item) => stringsTrim(item.content) === stringsTrim(content));
      if (content && !duplicate) visible.push({ kind: event.kind, role: 'summary', content, key: `run:${index}` });
    }
  });
  return visible;
}

const visible = visibleTerminalSummary([
  { kind: 'turn.end', content: '本轮结论：构建通过，但 modify_lin 仍未证明。' },
  { kind: 'run.end', status: 'completed', content: '最终测试结果：未满足用户要求，缺少 modify_lin 原始终止性证明。' },
]);

assert.equal(visible.length, 2);
assert.equal(visible[1].role, 'summary');
assert.ok(visible[1].content.includes('最终测试结果'));

const orderedProgress = visibleEventKindsAndContent([
  { runId: 'run1', turnId: 'turn1', role: 'reviewer', cli: 'codex', kind: 'turn.delta', seq: 39, content: '我会加入两个很小的辅助引理。' },
  { runId: 'run1', turnId: 'turn1', role: 'reviewer', cli: 'codex', kind: 'command.start', seq: 40, status: 'in_progress' },
  { runId: 'run1', turnId: 'turn1', role: 'reviewer', cli: 'codex', kind: 'command.end', seq: 41, status: 'completed' },
  { runId: 'run1', turnId: 'turn1', role: 'reviewer', cli: 'codex', kind: 'turn.delta', seq: 104, content: '目标 lemma 的多数 assert 基本过了。' },
  { runId: 'run1', turnId: 'turn1', role: 'reviewer', cli: 'codex', kind: 'turn.end', seq: 122, status: 'success', content: '我会加入两个很小的辅助引理。目标 lemma 的多数 assert 基本过了。' },
]);

assert.deepEqual(orderedProgress.map((item) => item.seq), [39, 40, 41, 104]);
assert.ok(orderedProgress[0].content.includes('辅助引理'));
assert.ok(orderedProgress[3].content.includes('多数 assert'));
assert.ok(!orderedProgress.some((item) => item.seq === 122));

const failedTurnEnd = visibleEventKindsAndContent([
  { runId: 'run1', turnId: 'turn1', role: 'reviewer', cli: 'codex', kind: 'turn.end', seq: 10, content: '继续编译。', error: 'codex app-server turn ended after coqc failed without a follow-up response' },
]);

assert.equal(failedTurnEnd.length, 2);
assert.equal(failedTurnEnd[1].type, 'status');
assert.ok(failedTurnEnd[1].content.includes('without a follow-up response'));
