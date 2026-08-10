import fs from 'node:fs';

const source = fs.readFileSync(new URL('./src/app/pages/Workspace.tsx', import.meta.url), 'utf8');
const utils = fs.readFileSync(new URL('./src/app/lib/utils.ts', import.meta.url), 'utf8');
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

if (!utils.includes('return selected?.id || defaultAgentID(agents);')) {
  throw new Error('Selected offline endpoints must remain selected while they still exist');
}

if (utils.includes('if (selected?.online) return selected.id;')) {
  throw new Error('Endpoint selection must not replace an offline endpoint with an online one');
}
