import fs from 'node:fs';

const loginScreen = fs.readFileSync(new URL('./src/app/pages/LoginScreen.tsx', import.meta.url), 'utf8');
const translations = fs.readFileSync(new URL('./src/app/lib/i18n.ts', import.meta.url), 'utf8');

assertCount(loginScreen, 'type={passwordVisible ?', 1);
assertCount(loginScreen, 'type={confirmPasswordVisible ?', 1);
assertCount(loginScreen, 'aria-pressed={passwordVisible}', 1);
assertCount(loginScreen, 'aria-pressed={confirmPasswordVisible}', 1);
assertCount(loginScreen, 't.hidePassword : t.showPassword', 2);
assertCount(loginScreen, 'type="button"', 4);
assertCount(translations, "showPassword: 'Show password'", 1);
assertCount(translations, "hidePassword: 'Hide password'", 1);
assertCount(translations, "showPassword: '显示密码'", 1);
assertCount(translations, "hidePassword: '隐藏密码'", 1);

function assertCount(source, value, expected) {
  const actual = source.split(value).length - 1;
  if (actual !== expected) {
    throw new Error(`expected ${JSON.stringify(value)} ${expected} time(s), found ${actual}`);
  }
}
