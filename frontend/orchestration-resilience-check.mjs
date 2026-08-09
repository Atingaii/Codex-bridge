import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const utils = readFileSync(new URL('./src/app/lib/utils.ts', import.meta.url), 'utf8');
const workspace = readFileSync(new URL('./src/app/pages/OrchestrationWorkspace.tsx', import.meta.url), 'utf8');
const main = readFileSync(new URL('./src/main.tsx', import.meta.url), 'utf8');
const boundary = readFileSync(new URL('./src/app/components/AppErrorBoundary.tsx', import.meta.url), 'utf8');

assert.match(utils, /export function isOrchestrationRun\(value: unknown\)/);
assert.match(utils, /if \(!isOrchestrationRun\(next\)\) return validCurrent;/);
assert.match(workspace, /if \(!isOrchestrationRun\(data\.run\)\) throw new Error\(t\.failedLoadOrchestration\);/);
assert.match(workspace, /Array\.isArray\(data\.runs\) \? data\.runs\.filter\(isOrchestrationRun\) : \[\]/);
assert.match(workspace, /retryTimer = window\.setTimeout\(retry, 3000\);/);
assert.match(workspace, /window\.addEventListener\('online', recover\);/);
assert.match(workspace, /document\.addEventListener\('visibilitychange', recover\);/);
assert.match(main, /<AppErrorBoundary><App \/><\/AppErrorBoundary>/);
assert.match(boundary, /The background task is not stopped/);
