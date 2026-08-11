import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const settings = readFileSync(new URL('./src/app/components/Settings.tsx', import.meta.url), 'utf8');

assert.match(settings, /const selectedRepairCommand =\s*repairProfileCommand\(repairInfo, repairInfo\?\.permissionProfile \|\| 'review-required'\) \|\|\s*repairInfo\?\.setupCommand \|\|\s*\(repairInfo\?\.installCommand && repairInfo\?\.connectCommand\s*\? `\$\{repairInfo\.installCommand\} && \$\{repairInfo\.connectCommand\}`\s*: ''\)/);
assert.doesNotMatch(settings, /const selectedRepairCommand =\s*\(repairInfo && repairProfileConnectCommand/);
assert.match(settings, /const alternateRepairCommand = repairInfo \? repairProfileCommand\(repairInfo, alternateRepairProfile\) : '';/);
