import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const settings = readFileSync(new URL('./src/app/components/Settings.tsx', import.meta.url), 'utf8');

assert.match(settings, /const openModelConfiguration = \(agent: Agent\) =>/);
assert.match(settings, /if \(agent\.capabilities\?\.configSwitcher\) \{\s*setConfigAgent\(agent\);/);
assert.match(settings, /setModelUpgradeAgentId\(agent\.id\);/);
assert.match(settings, /generateRepairToken\(agent\)\.catch/);
assert.match(settings, /orchestrationApprovalMode\(agent\) === 'auto-execute' \? 'auto-execute' : 'review-required'/);
assert.match(settings, /onClick=\{\(\) => openModelConfiguration\(agent\)\}/);
assert.match(settings, /disabled=\{!agent\.online\}/);
assert.doesNotMatch(settings, /disabled=\{!agent\.online \|\| !agent\.capabilities\?\.configSwitcher\}/);
