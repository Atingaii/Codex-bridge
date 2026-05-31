import React, { useState } from 'react';
import { AlertCircle, ChevronDown, Globe, Lock, RefreshCw, Terminal, User } from 'lucide-react';
import { api } from '../lib/api';
import type { UserAccount } from '../lib/types';
import type { Language, UIText } from '../lib/i18n';
import { Button, Input } from '../components/ui';

export function LoginScreen({
  onLogin,
  language,
  setLanguage,
  t,
}: {
  onLogin: (user: UserAccount) => void;
  language: Language;
  setLanguage: (value: Language) => void;
  t: UIText;
}) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleLogin = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    const form = new FormData(e.currentTarget);
    try {
      const data = await api<{ user: UserAccount }>('/api/login', {
        method: 'POST',
        body: JSON.stringify({
          username: String(form.get('username') || ''),
          password: String(form.get('password') || ''),
        }),
      });
      onLogin(data.user);
    } catch (err) {
      setError(err instanceof Error ? err.message : t.connectionFailed);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen w-full flex items-center justify-center bg-background text-foreground p-4">
      <div className="w-full max-w-[360px] flex flex-col gap-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <div className="h-12 w-12 rounded-xl bg-primary text-primary-foreground flex items-center justify-center mb-2 shadow-sm">
            <Terminal className="h-6 w-6" />
          </div>
          <h1 className="text-xl font-medium tracking-tight">Codex Bridge</h1>
          <p className="text-sm text-muted-foreground">{t.secureConnection}</p>
        </div>

        <form onSubmit={handleLogin} className="flex flex-col gap-4">
          <div className="space-y-4">
            <div className="space-y-2">
              <label className="text-sm font-medium leading-none" htmlFor="username">
                {t.username}
              </label>
              <div className="relative">
                <User className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input id="username" name="username" placeholder="admin" className="pl-9" autoComplete="username" required />
              </div>
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium leading-none" htmlFor="password">
                {t.password}
              </label>
              <div className="relative">
                <Lock className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input id="password" name="password" type="password" placeholder="••••••••" className="pl-9" autoComplete="current-password" required />
              </div>
            </div>
          </div>

          {error && (
            <div className="p-3 text-sm bg-destructive/10 text-destructive rounded-md border border-destructive/20 flex items-start gap-2">
              <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
              <p>{error}</p>
            </div>
          )}

          <Button type="submit" className="w-full" disabled={loading}>
            {loading ? <RefreshCw className="h-4 w-4 animate-spin" /> : t.connectToWorkspace}
          </Button>
        </form>

        <div className="flex justify-center mt-4">
          <Button variant="ghost" size="sm" className="text-muted-foreground gap-2" onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')}>
            <Globe className="h-4 w-4" />
            {language === 'zh' ? t.chinese : t.english}
            <ChevronDown className="h-3 w-3 opacity-50" />
          </Button>
        </div>
      </div>
    </div>
  );
}
