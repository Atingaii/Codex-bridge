import fs from 'node:fs';

const app = read('./src/app/App.tsx');
const updates = read('./src/app/pages/UpdatesPage.tsx');
const help = read('./src/app/pages/HelpPage.tsx');
const sidebar = read('./src/app/components/SidebarContent.tsx');
const orchestration = read('./src/app/pages/OrchestrationWorkspace.tsx');

assert(app.includes("path === '/updates'"), 'missing /updates route');
assert(app.indexOf('if (isUpdatesRoute)') < app.indexOf('if (!user)'), 'updates must render before authentication');
assert(updates.includes('修正时间线'), 'missing update timeline');
assert(updates.includes('当前版本 v0.3.27'), 'missing current version');
assertCount(updates, "date: '2026", 3);
assertCount(updates, "title: '", 9);
assert(help.includes('href="/updates"'), 'help updates link missing');
assert(sidebar.includes('href="/updates"'), 'chat sidebar updates link missing');
assert(orchestration.includes('href="/updates"'), 'orchestration sidebar updates link missing');

function read(path) {
  return fs.readFileSync(new URL(path, import.meta.url), 'utf8');
}

function assert(value, message) {
  if (!value) throw new Error(message);
}

function assertCount(source, value, expected) {
  const actual = source.split(value).length - 1;
  if (actual !== expected) throw new Error(`expected ${JSON.stringify(value)} ${expected} time(s), found ${actual}`);
}
