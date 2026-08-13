import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const settings = readFileSync(new URL('./src/app/components/Settings.tsx', import.meta.url), 'utf8');

assert.match(settings, /const selectedRepairCommand =\s*repairProfileCommand\(repairInfo, repairInfo\?\.permissionProfile \|\| 'review-required'\) \|\|\s*repairInfo\?\.setupCommand \|\|\s*\(repairInfo\?\.installCommand && repairInfo\?\.connectCommand\s*\? `\$\{repairInfo\.installCommand\} && \$\{repairInfo\.connectCommand\}`\s*: ''\)/);
assert.doesNotMatch(settings, /const selectedRepairCommand =\s*\(repairInfo && repairProfileConnectCommand/);
assert.match(settings, /const alternateRepairCommands = repairInfo\s*\? permissionOptions\s*\.filter\(\(option\) => option\.id !== repairInfo\.permissionProfile\)\s*\.map\(\(option\) => \(\{ id: option\.id, title: option\.title, command: repairProfileCommand\(repairInfo, option\.id\) \}\)\)\s*\.filter\(\(option\) => option\.command\)\s*: \[\];/);
assert.match(settings, /\{alternateRepairCommands\.map\(\(alternate\) => \(/);
