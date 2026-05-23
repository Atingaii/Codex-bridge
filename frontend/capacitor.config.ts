import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'tech.sparkapi.codexbridge',
  appName: 'Codex Bridge',
  webDir: '../internal/web/static',
  server: {
    url: 'https://sparkapi.tech',
    cleartext: false,
  },
  android: {
    path: '../android',
  },
};

export default config;
