import fs from 'node:fs';

const app = read('./src/app/App.tsx');
const updates = read('./src/app/pages/UpdatesPage.tsx');
const help = read('./src/app/pages/HelpPage.tsx');
const sidebar = read('./src/app/components/SidebarContent.tsx');
const orchestration = read('./src/app/pages/OrchestrationWorkspace.tsx');

assert(app.includes("path === '/updates'"), 'missing /updates route');
assert(app.indexOf('if (isUpdatesRoute)') < app.indexOf('if (!user)'), 'updates must render before authentication');
assert(updates.includes('修正时间线'), 'missing update timeline');
assert(updates.includes('当前版本 v0.3.37'), 'missing current version');
assert(updates.includes('消除嵌套沙箱冲突'), 'missing nested sandbox compatibility update');
assert(updates.includes('外层规则覆盖全部命令'), 'missing outer sandbox coverage update');
assert(updates.includes('隐藏工具目录只读可用'), 'missing strict hidden-tool compatibility update');
assert(updates.includes('系统与命令沙箱正常工作'), 'missing strict runtime compatibility update');
assert(updates.includes('兼容外部 Codex 配置文件'), 'missing strict workspace switcher compatibility fix');
assert(updates.includes('修复严格模式启动参数'), 'missing strict workspace link fix');
assert(updates.includes('统一功能发布策略'), 'missing feature rollout update');
assert(updates.includes('正式更名为 ProofBridge'), 'missing brand rename update');
assert(updates.includes('支持修改供应商预设'), 'missing provider preset editing update');
assert(updates.includes('Android 包装器归档'), 'missing Android archive update');
assertCount(updates, "date: '2026", 10);
assertCount(updates, "title: '", 24);
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
