import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source = readFileSync(new URL('./src/app/App.tsx', import.meta.url), 'utf8');

assert.match(source, /function orchestrationStatusContent\(event: OrchestrationEvent\)/);
assert.match(source, /if \(\(event\.kind === 'run\.end' \|\| event\.kind === 'run\.error'\) && content\) return content;/);
assert.match(source, /role: 'summary'/);
assert.match(source, /function isBridgeRelayNotice\(event: Pick<OrchestrationEvent, 'kind' \| 'source' \| 'severity'>\)/);
assert.match(source, /function canMergeAdjacentOrchestrationDelta\(previous: OrchestrationEvent \| undefined, event: OrchestrationEvent\)/);
assert.match(source, /function orchestrationTurnDeltaContentByKey\(events: OrchestrationEvent\[\]\)/);
assert.match(source, /function turnEndDisplayContent\(content: string, deltaContent: string\)/);
assert.doesNotMatch(source, /contentfulTurnEnds\.has\(orchestrationTurnKey\(event\)\)/);
assert.match(source, /const rawContent = item\.content \|\| item\.error \|\| '';/);
assert.doesNotMatch(source, /unresolvedAcceptanceSummary/);
assert.doesNotMatch(source, /hasUnresolvedAcceptanceSignal/);
assert.doesNotMatch(source, /Unmet acceptance|未满足验收/);

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
      return content ? [{ kind: event.kind, seq: event.seq, content }] : [];
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
