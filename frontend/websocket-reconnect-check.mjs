import fs from 'node:fs';

const source = fs.readFileSync(new URL('./src/app/pages/Workspace.tsx', import.meta.url), 'utf8');
const required = [
  'reconnectAttemptRef',
  'loadMessages(sessionId)',
  'loadRuns(sessionId)',
  "window.addEventListener('online', recover)",
  "document.addEventListener('visibilitychange', recover)",
  'connectWS(sessionId, true)',
];

for (const marker of required) {
  if (!source.includes(marker)) {
    throw new Error(`Workspace reconnect recovery is missing: ${marker}`);
  }
}

if (!source.includes('Math.min(15_000')) {
  throw new Error('Workspace reconnect delay must remain bounded');
}

if (!source.includes("index === existing ? { ...item, approval } : item")) {
  throw new Error('Repeated approval requests must retain an existing decision');
}
