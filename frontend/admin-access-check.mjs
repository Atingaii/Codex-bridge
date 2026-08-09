import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const app = readFileSync(new URL('./src/app/App.tsx', import.meta.url), 'utf8');
const workspace = readFileSync(new URL('./src/app/pages/Workspace.tsx', import.meta.url), 'utf8');
const orchestration = readFileSync(new URL('./src/app/pages/OrchestrationWorkspace.tsx', import.meta.url), 'utf8');
const overview = readFileSync(new URL('./src/app/pages/AdminUsagePage.tsx', import.meta.url), 'utf8');
const detail = readFileSync(new URL('./src/app/pages/AdminUserUsagePage.tsx', import.meta.url), 'utf8');

assert.match(app, /path\.startsWith\('\/admin'\) && !user\.isAdmin/);
assert.match(app, /if \(!user\.isAdmin\) return null;/);
assert.match(app, /\/admin\\\/usage\\\/users/);
assert.match(workspace, /user\.isAdmin && <Button[\s\S]{0,400}?navigate\('\/admin\/usage'\)/);
assert.match(orchestration, /user\.isAdmin && <Button[\s\S]{0,400}?navigate\('\/admin\/usage'\)/);
assert.match(overview, /navigate\(`\/admin\/usage\/users\/\$\{encodeURIComponent\(item\.userId\)\}`\)/);
assert.match(detail, /\/api\/admin\/users\/\$\{encodeURIComponent\(userID\)\}\/usage/);
assert.doesNotMatch(detail, /\/api\/sessions\/|\/api\/orchestrations\/\$\{/);
assert.doesNotMatch(detail, /prompt|remoteThreadId|runCwd|message\.content/i);
