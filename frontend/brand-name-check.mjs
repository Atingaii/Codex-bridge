import fs from 'node:fs';

const files = [
  './index.html',
  './public/app-recovery.js',
  './public/manifest.webmanifest',
  './src/app/components/AppErrorBoundary.tsx',
  './src/app/lib/i18n.ts',
  './src/app/pages/HelpPage.tsx',
  './src/app/pages/LoginScreen.tsx',
  './src/app/pages/UpdatesPage.tsx',
];

for (const path of files) {
  const source = fs.readFileSync(new URL(path, import.meta.url), 'utf8');
  if (!source.includes('ProofBridge')) throw new Error(`${path} is missing the ProofBridge brand`);
  if (/Codex Bridge|Codex-Bridge|Codex-bridge/.test(source)) throw new Error(`${path} contains the retired brand`);
}
