const { execFileSync, spawn } = require('node:child_process');
const { createHash } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');

const { chromium } = require(process.env.PLAYWRIGHT_NODE_PATH
  ? path.join(process.env.PLAYWRIGHT_NODE_PATH, 'playwright')
  : 'playwright');
const frontendDir = path.resolve(__dirname, '..');
const outputDir = path.join(frontendDir, 'public', 'help');
const port = Number(process.env.HELP_SCREENSHOT_PORT || 4193);
const baseURL = `http://127.0.0.1:${port}`;
const now = 1_785_888_000;
let activeDemoRunID = '';

const user = { id: 'usr_demo_001', username: 'demo_user', createdAt: now - 86400, isAdmin: false };
const agent = {
  id: 'agt_demo_001', userId: user.id, name: 'proof-workstation', machineId: 'mac_demo_001',
  hostname: 'demo-host', workingDirs: ['/workspace/formal-demo', '/workspace/codex-bridge'],
  lastSeenAt: now, online: true,
  capabilities: {
    runner: 'codex', sandbox: 'workspace-write', approvalPolicy: 'on-request',
    chat: { codex: { available: true, execution: 'exec-json', browserApproval: true } },
    orchestration: {
      codex: { available: true, execution: 'native', browserApproval: true },
      claude: { available: true, execution: 'native', browserApproval: true },
    },
    metadata: { approvalMode: 'review-required' },
  },
};
const session = {
  id: 'ses_demo_001', agentId: agent.id, userId: user.id, title: '上传文件名解析修复',
  remoteThreadId: 'thread_demo_001', nativeResumeId: 'native_demo_001', createdAt: now - 7200, updatedAt: now - 180,
};
const messages = [
  { id: 'msg_demo_001', sessionId: session.id, role: 'user', content: '修复上传文件名包含空格时的解析问题。\n\n边界：只改上传解析与相关测试。\n验收：运行 go test ./internal/hub -run Upload。', createdAt: now - 620 },
  { id: 'msg_demo_002', sessionId: session.id, role: 'assistant', content: '已定位 multipart 文件名规范化路径，并补充了包含空格与中文字符的回归测试。\n\n`go test ./internal/hub -run Upload` 已通过。修改仅限上传解析和对应测试。', createdAt: now - 510 },
];

function run(id, mode, profile = 'default') {
  return {
    id, agentId: agent.id, title: mode === 'debate' ? '验证 SQLite 用户隔离' : profile === 'formal-proof' ? '证明 reverse_acc_correct' : '请求幂等性协作实现',
    mode, workerPair: 'claude-codex', firstCli: 'claude', profile, nativeContextCompaction: profile === 'formal-proof' ? 'after-turn' : 'off',
    prompt: mode === 'debate' ? '把证明缓存改为 SQLite 是否会破坏 user_id 运行隔离？请用反例和测试证据辩论。' : profile === 'formal-proof' ? '完成 theorem reverse_acc_correct，不允许 Admitted 或弱化命题。' : '在不改变公开 API 的前提下增加请求幂等性，并以竞态测试验收。',
    cwd: '/workspace/formal-demo', maxTurns: 4, status: 'completed',
    files: profile === 'formal-proof' ? [{ name: 'Task.v', mimeType: 'text/plain', size: 2841 }, { name: 'Contract.v', mimeType: 'text/plain', size: 1920 }] : [],
    createdAt: now - 3600, updatedAt: now - 120, finishedAt: now - 120,
  };
}
const collabRun = run('orc_collab_001', 'collaboration');
const debateRun = run('orc_debate_001', 'debate');
const proofRun = run('orc_proof_001', 'collaboration', 'formal-proof');

function eventsFor(target) {
  const isDebate = target.mode === 'debate';
  const isProof = target.profile === 'formal-proof';
  const contents = isProof
    ? [
      ['claude', 'proof-author', '建立列表反转累加器的归纳不变量：`rev (acc ++ xs) = rev xs ++ rev acc`，先检查命题没有被弱化。'],
      ['codex', 'proof-reviewer', '已实现辅助引理并运行 `coqc Contract.v && coqc Task.v`，两个文件均退出 0。'],
      ['claude', 'proof-auditor', '负向审计未发现 Admitted、Axiom、sorry 或新增公理；证明义务保持原样。'],
    ]
    : isDebate
      ? [
        ['claude', 'proposer', '主张：以 `(user_id, proof_key)` 作为复合唯一键，并在事务中写入，可以保持租户隔离。'],
        ['codex', 'opponent', '反例：仅在查询层追加 user_id 不足以防止错误 upsert。数据库约束必须包含 user_id，并用两个并发用户复现。'],
        ['claude', 'synthesizer', '采纳反例。最小方案是复合主键、所有 CRUD 强制 user_id、跨用户并发测试以及重启恢复测试。'],
      ]
      : [
        ['claude', 'planner', '识别不变量：同一 Idempotency-Key 只产生一个写入结果；不同用户的 key 不能互相命中。'],
        ['codex', 'implementer', '已加入存储层原子 claim，并完成并发回归测试。`go test -race ./...` 通过。'],
        ['claude', 'reviewer', '交叉检查了冲突路径、失败重试和用户隔离，未发现越过公开 API 的变更。'],
      ];
  const result = [{ id: `${target.id}:start`, runId: target.id, seq: 1, timelineOrder: 1, kind: 'run.start', source: 'bridge', runStartData: { cwd: target.cwd, mode: target.mode, workerPair: target.workerPair, firstCli: target.firstCli, maxTurnsRequested: 4, maxTurnsApplied: 4, profile: target.profile, nativeContextCompaction: target.nativeContextCompaction }, createdAt: now - 3500 }];
  let seq = 2;
  contents.forEach(([cli, role, content], index) => {
    const turnId = `${target.id}:turn:${index + 1}`;
    result.push({ id: `${turnId}:start`, runId: target.id, seq: seq++, timelineOrder: seq, kind: 'turn.start', source: 'bridge', cli, role, turnId, turnStartData: { cli, workerSlot: role, turn: index + 1, maxTurns: 4, profile: target.profile }, createdAt: now - 3000 + index * 500 });
    result.push({ id: `${turnId}:delta`, runId: target.id, seq: seq++, timelineOrder: seq, kind: 'turn.delta', source: 'bridge', cli, role, turnId, content, createdAt: now - 2980 + index * 500 });
    if (index === 1) result.push({ id: `${turnId}:cmd`, runId: target.id, seq: seq++, timelineOrder: seq, kind: 'command.completed', source: 'bridge', cli, role, turnId, commandData: { id: `cmd-${index}`, command: isProof ? 'coqc Contract.v && coqc Task.v' : 'go test -race ./...', output: isProof ? 'Finished Contract.v\nFinished Task.v' : 'ok  codex-bridge/internal/store  2.318s', status: 'completed', exitCode: 0, durationMs: 2318 }, createdAt: now - 2900 + index * 500 });
    result.push({ id: `${turnId}:end`, runId: target.id, seq: seq++, timelineOrder: seq, kind: 'turn.end', source: 'bridge', cli, role, turnId, status: 'completed', createdAt: now - 2850 + index * 500 });
  });
  result.push({ id: `${target.id}:conclusion`, runId: target.id, seq: seq++, timelineOrder: seq, kind: 'run.conclusion', source: 'bridge', role: 'verifier', content: isProof ? '证明已满足：真实 proof assistant 构建成功，且负向审计通过。' : '所有验收条件均有测试证据，结论为 satisfied。', runConclusion: { outcome: 'satisfied', summary: isProof ? '证明构建和禁止项审计均通过。' : '实现、竞态测试和隔离检查均通过。', buildOrAuditCommands: [isProof ? 'coqc Contract.v && coqc Task.v' : 'go test -race ./...'], evidenceRefs: ['turn:2', 'command:completed'] }, createdAt: now - 180 });
  result.push({ id: `${target.id}:end`, runId: target.id, seq: seq++, timelineOrder: seq, kind: 'run.end', source: 'bridge', status: 'completed', runEndData: { codexThreadId: 'thread_demo_orchestration', claudeSessionId: 'claude_demo_orchestration' }, createdAt: now - 120 });
  return result;
}

const bridgeToken = {
  token: 'demo_enroll_token_redacted', expiresAt: now + 86400, label: 'proof-workstation', hubUrl: 'https://sparkon.cn', downloadUrl: 'https://sparkon.cn/download/codex-bridge-linux-amd64', permissionProfile: 'review-required',
  setupCommand: 'curl -fsSL https://sparkon.cn/install.sh | sh', installCommand: 'curl -fsSL https://sparkon.cn/install.sh | sh',
  connectCommand: "codex-bridge bridge --hub https://sparkon.cn --token 'demo_enroll_token_redacted'",
  commands: ['curl -fsSL https://sparkon.cn/install.sh | sh', "codex-bridge bridge --hub https://sparkon.cn --token 'demo_enroll_token_redacted'"],
  permissionProfiles: [
    { id: 'review-required', setupCommand: "codex-bridge bridge --hub https://sparkon.cn --token 'demo_enroll_token_redacted' --permission-profile review-required", connectCommand: "codex-bridge bridge --hub https://sparkon.cn --token 'demo_enroll_token_redacted' --permission-profile review-required" },
    { id: 'auto-execute', setupCommand: "codex-bridge bridge --hub https://sparkon.cn --token 'demo_enroll_token_redacted' --permission-profile auto-execute", connectCommand: "codex-bridge bridge --hub https://sparkon.cn --token 'demo_enroll_token_redacted' --permission-profile auto-execute" },
  ],
};

async function main() {
  fs.mkdirSync(outputDir, { recursive: true });
  const server = spawn(process.platform === 'win32' ? 'npm.cmd' : 'npm', ['run', 'dev', '--', '--host', '127.0.0.1', '--port', String(port), '--strictPort'], {
    cwd: frontendDir,
    stdio: ['ignore', 'pipe', 'pipe'],
    detached: process.platform !== 'win32',
  });
  let serverLog = '';
  let browser;
  server.stdout.on('data', (chunk) => { serverLog += chunk; });
  server.stderr.on('data', (chunk) => { serverLog += chunk; });
  try {
    await waitForServer();
    browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM || chromium.executablePath(), args: ['--no-sandbox'] });
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1, locale: 'zh-CN', colorScheme: 'dark' });
    await context.addInitScript(() => {
      localStorage.setItem('codexBridge.language', 'zh');
      localStorage.setItem('codexBridge.theme', 'dark');
      localStorage.setItem('codexBridge.selectedAgentId', 'agt_demo_001');
      localStorage.setItem('codexBridge.activeSessionByAgent', JSON.stringify({ agt_demo_001: 'ses_demo_001' }));
      window.turnstile = {
        render(container, options) {
          container.innerHTML = '<div style="width:300px;height:65px;border:1px solid #3f4a5a;border-radius:4px;background:#1f2937;color:#e5e7eb;display:flex;align-items:center;gap:12px;padding:12px;font:14px system-ui"><span style="width:22px;height:22px;border:2px solid #22c55e;border-radius:3px;display:grid;place-items:center;color:#22c55e">✓</span><span>安全验证已完成</span><span style="margin-left:auto;font-size:10px;color:#94a3b8">CLOUDFLARE</span></div>';
          setTimeout(() => options.callback?.('demo-turnstile-token'), 10);
          return 'demo-widget';
        },
        remove() {},
        reset() {},
      };
      class DemoWebSocket {
        static CONNECTING = 0; static OPEN = 1; static CLOSING = 2; static CLOSED = 3;
        constructor(url) {
          this.url = String(url); this.readyState = DemoWebSocket.CONNECTING;
          setTimeout(() => { this.readyState = DemoWebSocket.OPEN; this.onopen?.({}); this.emitDemo(); }, 20);
        }
        emit(payload) { this.onmessage?.({ data: JSON.stringify(payload) }); }
        emitDemo() {
          if (!this.url.includes('/ws/chat')) return;
          const sid = 'ses_demo_001';
          this.emit({ type: 'session_opened', sid, payload: { runner: 'codex', remoteThreadId: 'thread_demo_001' } });
          this.emit({ type: 'session_update', sid, payload: { tool: { id: 'tool_demo_001', name: 'bash', command: 'go test ./internal/hub -run Upload', output: 'ok  codex-bridge/internal/hub  0.284s', status: 'completed', exitCode: 0 } } });
          if (localStorage.getItem('helpShotState') === 'approval') this.emit({ type: 'approval_request', sid, payload: { requestId: 'approval_demo_001', kind: 'ccb.terminal_prompt', command: 'go test -race ./...', cwd: '/workspace/formal-demo', reason: '运行竞态检测以满足验收条件', runId: 'run_demo_001', promptId: 'prompt_demo_001' } });
        }
        send() {} close() { this.readyState = DemoWebSocket.CLOSED; this.onclose?.({}); }
        addEventListener(name, fn) { this[`on${name}`] = fn; }
        removeEventListener(name, fn) { if (this[`on${name}`] === fn) this[`on${name}`] = null; }
      }
      window.WebSocket = DemoWebSocket;
    });
    await context.route('**/api/**', async (route) => handleAPI(route));
    const page = await context.newPage();
    const errors = [];
    page.on('console', (msg) => { if (msg.type() === 'error') errors.push(msg.text()); });
    page.on('pageerror', (error) => errors.push(error.message));

    await shotLogin(page);
    await shotRegister(page);
    await shotWorkspace(page);
    await shotSettings(page);
    await shotOrchestrations(page);
    assertDistinctOrchestrationScreenshots();
    await shotShare(page);
    await shotMobile(page);
    await checkHelpPage(page);
    if (errors.length) throw new Error(`browser errors:\n${errors.join('\n')}`);
    console.log(`generated 17 screenshots in ${outputDir}`);
  } finally {
    await browser?.close().catch(() => undefined);
    if (process.platform === 'win32') server.kill('SIGTERM');
    else {
      try { process.kill(-server.pid, 'SIGTERM'); } catch {}
    }
    await Promise.race([
      new Promise((resolve) => server.once('close', resolve)),
      new Promise((resolve) => setTimeout(resolve, 2000)),
    ]);
  }
}

async function handleAPI(route) {
  const request = route.request();
  const url = new URL(request.url());
  const pathname = url.pathname;
  let body;
  if (pathname === '/api/me') {
    if (request.headers()['x-help-shot-state'] === 'anonymous') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ user: null }) });
    }
    body = { user };
  }
  else if (pathname === '/api/auth/config') body = { registrationEnabled: true, turnstileSiteKey: 'demo-site-key' };
  else if (pathname === '/api/agents') body = { agents: [agent] };
  else if (pathname === '/api/sessions') body = request.method() === 'POST' ? { session } : { sessions: [session] };
  else if (pathname === `/api/sessions/${session.id}/messages`) body = { messages };
  else if (pathname === `/api/sessions/${session.id}/runs`) body = { runs: [] };
  else if (pathname === '/api/bridge-tokens') body = bridgeToken;
  else if (pathname === '/api/orchestrations') {
    body = request.method() === 'POST' ? { run: collabRun } : { runs: activeDemoRunID === 'empty' ? [] : [proofRun, debateRun, collabRun] };
  }
  else if (/^\/api\/orchestrations\/[^/]+\/events$/.test(pathname)) {
    const id = pathname.split('/')[3];
    const target = [proofRun, debateRun, collabRun].find((item) => item.id === id) || collabRun;
    body = { events: eventsFor(target) };
  } else if (/^\/api\/orchestrations\/[^/]+$/.test(pathname)) {
    const id = pathname.split('/')[3];
    body = { run: [proofRun, debateRun, collabRun].find((item) => item.id === id) || collabRun };
  } else if (pathname === '/api/public/shares/demo-share') body = { share: { id: 'demo-share', kind: 'chat', title: '上传文件名解析修复', createdAt: now - 500, updatedAt: now - 120 }, session, messages };
  else body = {};
  await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
}

async function shotLogin(page) {
  await page.setExtraHTTPHeaders({ 'x-help-shot-state': 'anonymous' });
  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.locator('#username').fill('demo_user');
  await page.locator('#password').fill('correct-horse-demo');
  await capture(page, 'auth-login.webp', page.locator('form').locator('..'));
}

async function shotRegister(page) {
  await page.setExtraHTTPHeaders({ 'x-help-shot-state': 'anonymous' });
  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.getByRole('tab', { name: /注册账户/ }).click();
  await page.locator('#username').fill('demo_user');
  await page.locator('#password').fill('correct-horse-demo');
  await page.locator('#confirmPassword').fill('correct-horse-demo');
  await page.getByText('安全验证已完成').waitFor();
  await capture(page, 'auth-register.webp', page.locator('form').locator('..'));
}

async function shotWorkspace(page) {
  await page.setExtraHTTPHeaders({ 'x-help-shot-state': 'authenticated' });
  await page.evaluate(() => localStorage.setItem('helpShotState', 'chat'));
  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.getByText('上传文件名解析修复', { exact: true }).first().waitFor();
  await capture(page, 'workspace-overview.webp', page.locator('body'));
  await capture(page, 'chat-demo.webp', page.locator('main'));
  await page.evaluate(() => localStorage.setItem('helpShotState', 'approval'));
  await page.reload({ waitUntil: 'networkidle' });
  await page.getByText('运行竞态检测以满足验收条件').waitFor();
  await capture(page, 'chat-approval.webp', page.locator('main'));
  await capture(page, 'session-actions.webp', page.locator('aside').first());
}

async function shotSettings(page) {
  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.getByRole('button', { name: /设置/ }).last().click();
  const dialog = page.locator('.fixed.inset-0 > div').last();
  await dialog.waitFor();
  await capture(page, 'settings-overview.webp', dialog);
  await page.getByPlaceholder('wsl2-cli').fill('proof-workstation');
  await page.getByRole('button', { name: '添加', exact: true }).click();
  const enrollToken = page.getByText('demo_enroll_token_redacted', { exact: true });
  await enrollToken.waitFor();
  await enrollToken.scrollIntoViewIfNeeded();
  await capture(page, 'endpoint-enroll.webp', dialog);
  const machineId = page.getByText('mac_demo_001', { exact: true });
  await machineId.waitFor();
  await machineId.scrollIntoViewIfNeeded();
  await capture(page, 'endpoint-capabilities.webp', dialog);
}

async function shotOrchestrations(page) {
  activeDemoRunID = '';
  await page.setExtraHTTPHeaders({ 'x-help-shot-state': 'authenticated' });
  await page.goto(`${baseURL}/orchestrate`, { waitUntil: 'networkidle' });
  await page.getByText('请求幂等性协作实现', { exact: true }).waitFor();
  await capture(page, 'orchestration-overview.webp', page.locator('body'));
  await openRun(page, collabRun.id, 'collaboration-run.webp');
  await openRun(page, debateRun.id, 'debate-run.webp');
  await openRun(page, proofRun.id, 'formal-proof-result.webp');
  activeDemoRunID = 'empty';
  await page.setExtraHTTPHeaders({ 'x-help-shot-state': 'authenticated' });
  await page.goto(`${baseURL}/orchestrate`, { waitUntil: 'networkidle' });
  await page.getByRole('button', { name: '新运行', exact: true }).click();
  await page.getByRole('button', { name: /形式化证明/ }).last().click();
  await page.getByRole('button', { name: '关闭', exact: true }).last().click();
  await capture(page, 'formal-proof-setup.webp', page.locator('main'));
  await page.getByRole('button', { name: /每轮后/ }).last().click();
  await capture(page, 'context-controls.webp', page.locator('main'));
}

async function openRun(page, id, filename) {
  activeDemoRunID = id;
  const expected = id === proofRun.id ? '证明 reverse_acc_correct' : id === debateRun.id ? '验证 SQLite 用户隔离' : '请求幂等性协作实现';
  const evidence = id === proofRun.id ? '负向审计未发现' : id === debateRun.id ? '采纳反例' : '交叉检查了冲突路径';
  await page.locator('aside').first().getByText(expected, { exact: true }).click();
  await page.locator('header').getByText(expected, { exact: true }).waitFor();
  await page.locator('main').getByText(new RegExp(evidence)).waitFor();
  const fixtureText = JSON.stringify(eventsFor(id === proofRun.id ? proofRun : id === debateRun.id ? debateRun : collabRun));
  if (id === proofRun.id && !fixtureText.includes('coqc Contract.v')) throw new Error('formal-proof screenshot fixture is missing coqc evidence');
  if (id === debateRun.id && !fixtureText.includes('复合主键')) throw new Error('debate screenshot fixture is missing falsification evidence');
  await page.waitForTimeout(250);
  await capture(page, filename, page.locator('main'));
}

async function shotShare(page) {
  await page.setExtraHTTPHeaders({ 'x-help-shot-state': 'authenticated' });
  await page.goto(`${baseURL}/share/demo-share`, { waitUntil: 'networkidle' });
  await page.getByText('只读快照').first().waitFor();
  await capture(page, 'public-share.webp', page.locator('body'));
}

async function shotMobile(page) {
  await page.setExtraHTTPHeaders({ 'x-help-shot-state': 'authenticated' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(baseURL, { waitUntil: 'networkidle' });
  await page.locator('main').waitFor();
  await page.locator('textarea').last().waitFor();
  await capture(page, 'mobile-workspace.webp', page.locator('body'));
  await page.setViewportSize({ width: 1440, height: 900 });
}

async function checkHelpPage(page) {
  await page.setExtraHTTPHeaders({});
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${baseURL}/help`, { waitUntil: 'networkidle' });
  await page.getByRole('heading', { name: 'Codex Bridge 详细使用教程' }).waitFor();
  const helpImages = page.locator('main img');
  for (let index = 0; index < await helpImages.count(); index += 1) {
    await helpImages.nth(index).scrollIntoViewIfNeeded();
    await helpImages.nth(index).evaluate((img) => img.complete && img.naturalWidth > 0 || new Promise((resolve) => {
      img.addEventListener('load', () => resolve(true), { once: true });
      img.addEventListener('error', () => resolve(false), { once: true });
    }));
  }
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
  const desktop = await page.evaluate(() => ({
    overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    images: Array.from(document.images).map((img) => ({ src: img.getAttribute('src'), width: img.naturalWidth, height: img.naturalHeight })),
  }));
  if (desktop.overflow > 1) throw new Error(`desktop help overflow: ${desktop.overflow}px`);
  if (desktop.images.length !== 17 || desktop.images.some((img) => !img.width || !img.height)) throw new Error('help page contains missing screenshots');
  await page.getByRole('button', { name: '复制' }).first().click();
  await page.getByRole('button', { name: '已复制' }).waitFor();
  await page.getByRole('button', { name: /切换浅色模式|切换深色模式/ }).click();
  await page.setViewportSize({ width: 360, height: 800 });
  const mobileOverflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  if (mobileOverflow > 1) throw new Error(`mobile help overflow: ${mobileOverflow}px`);
  await page.getByRole('button', { name: '打开目录' }).click();
  await page.locator('.fixed.inset-0 aside').getByText('形式化验证', { exact: true }).waitFor();
  await page.goto(`${baseURL}/hlep`, { waitUntil: 'networkidle' });
  await page.getByRole('heading', { name: 'Codex Bridge 详细使用教程' }).waitFor();
  await page.setViewportSize({ width: 1440, height: 900 });
}

async function capture(page, filename, locator) {
  await page.waitForTimeout(100);
  const output = path.join(outputDir, filename);
  const temporary = `${output}.png`;
  await locator.screenshot({ path: temporary, type: 'png', animations: 'disabled' });
  execFileSync('convert', [temporary, '-quality', '84', output]);
  fs.unlinkSync(temporary);
  if (fs.statSync(output).size < 1024) throw new Error(`screenshot is unexpectedly small: ${filename}`);
}

function assertDistinctOrchestrationScreenshots() {
  const filenames = [
    'collaboration-run.webp',
    'debate-run.webp',
    'formal-proof-result.webp',
    'formal-proof-setup.webp',
    'context-controls.webp',
  ];
  const hashes = filenames.map((filename) => createHash('sha256').update(fs.readFileSync(path.join(outputDir, filename))).digest('hex'));
  if (new Set(hashes).size !== filenames.length) {
    throw new Error('orchestration help screenshots must show five distinct states');
  }
}

async function waitForServer() {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    try {
      const response = await fetch(baseURL);
      if (response.ok) return;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Vite did not start at ${baseURL}`);
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
