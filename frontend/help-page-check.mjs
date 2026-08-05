import fs from 'node:fs';

const app = read('./src/app/App.tsx');
const help = read('./src/app/pages/HelpPage.tsx');
const login = read('./src/app/pages/LoginScreen.tsx');
const sidebar = read('./src/app/components/SidebarContent.tsx');
const orchestration = read('./src/app/pages/OrchestrationWorkspace.tsx');

assert(app.includes("path === '/help'"), 'missing /help route');
assert(app.includes("path === '/hlep'"), 'missing /hlep alias');
assert(app.indexOf('if (isHelpRoute)') < app.indexOf('if (!user)'), 'help must render before authentication');
assertCount(help, 'src="/help/', 17);
assert(help.includes('协作模式：分析、实现、验证逐步收敛'), 'missing collaboration guide');
assert(help.includes('辩论模式：先写可证伪主张，再寻找反例'), 'missing debate guide');
assert(help.includes('形式化验证：让证明义务和证据成为主线'), 'missing formal-proof guide');
assert(help.includes('用更少上下文获得更稳定的编排'), 'missing context guide');
assertCount(help, '<DemoBlock', 4);
assert(login.includes('href="/help"'), 'login help link missing');
assert(sidebar.includes('href="/help"'), 'chat sidebar help link missing');
assert(orchestration.includes('href="/help"'), 'orchestration sidebar help link missing');

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
