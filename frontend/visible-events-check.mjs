import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source = readFileSync(new URL('./src/app/App.tsx', import.meta.url), 'utf8');

assert.match(source, /function orchestrationStatusContent\(event: OrchestrationEvent\)/);
assert.match(source, /if \(\(event\.kind === 'run\.end' \|\| event\.kind === 'run\.error'\) && content\) return content;/);
assert.match(source, /const rawContent = item\.content \|\| item\.error \|\| '';/);

function stringsTrim(value) {
  return typeof value === 'string' ? value.trim() : '';
}

function orchestrationStatusContent(event) {
  const content = stringsTrim(event.content);
  const error = stringsTrim(event.error);
  if ((event.kind === 'run.end' || event.kind === 'run.error') && content) return content;
  return error || content || stringsTrim(event.status) || event.kind;
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
