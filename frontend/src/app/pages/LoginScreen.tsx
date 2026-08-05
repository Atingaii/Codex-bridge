import React, { useEffect, useRef, useState } from 'react';
import { AlertCircle, BookOpen, ChevronDown, Eye, EyeOff, Globe, Lock, RefreshCw, Terminal, User } from 'lucide-react';
import { api } from '../lib/api';
import type { UserAccount } from '../lib/types';
import type { Language, UIText } from '../lib/i18n';
import { Button, Input } from '../components/ui';

type AuthConfig = {
  registrationEnabled: boolean;
  turnstileSiteKey: string;
};

type TurnstileAPI = {
  render: (container: HTMLElement, options: Record<string, unknown>) => string;
  remove: (widgetId: string) => void;
  reset: (widgetId: string) => void;
};

declare global {
  interface Window {
    turnstile?: TurnstileAPI;
  }
}

let turnstileScriptPromise: Promise<TurnstileAPI> | null = null;

function loadTurnstile(): Promise<TurnstileAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  if (turnstileScriptPromise) return turnstileScriptPromise;
  turnstileScriptPromise = new Promise((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>('script[data-codex-bridge-turnstile]');
    const script = existing || document.createElement('script');
    const handleLoad = () => window.turnstile ? resolve(window.turnstile) : reject(new Error('Turnstile unavailable'));
    script.addEventListener('load', handleLoad, { once: true });
    script.addEventListener('error', () => reject(new Error('Turnstile unavailable')), { once: true });
    if (!existing) {
      script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
      script.async = true;
      script.defer = true;
      script.dataset.codexBridgeTurnstile = 'true';
      document.head.appendChild(script);
    }
  }).catch((error) => {
    turnstileScriptPromise = null;
    throw error;
  });
  return turnstileScriptPromise;
}

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
  const [mode, setMode] = useState<'login' | 'register'>('login');
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [confirmPasswordVisible, setConfirmPasswordVisible] = useState(false);
  const [authConfig, setAuthConfig] = useState<AuthConfig | null>(null);
  const [turnstileToken, setTurnstileToken] = useState('');
  const turnstileContainerRef = useRef<HTMLDivElement | null>(null);
  const turnstileWidgetRef = useRef<string | null>(null);

  const selectMode = (nextMode: 'login' | 'register') => {
    setMode(nextMode);
    setError('');
    setPasswordVisible(false);
    setConfirmPasswordVisible(false);
  };

  useEffect(() => {
    let active = true;
    api<AuthConfig>('/api/auth/config')
      .then((config) => {
        if (active) setAuthConfig(config);
      })
      .catch(() => {
        if (active) setAuthConfig({ registrationEnabled: false, turnstileSiteKey: '' });
      });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (mode !== 'register' || !authConfig?.registrationEnabled || !authConfig.turnstileSiteKey || !turnstileContainerRef.current) return;
    let active = true;
    loadTurnstile()
      .then((turnstile) => {
        if (!active || !turnstileContainerRef.current) return;
        turnstileWidgetRef.current = turnstile.render(turnstileContainerRef.current, {
          sitekey: authConfig.turnstileSiteKey,
          action: 'register',
          theme: 'auto',
          callback: (token: string) => {
            setTurnstileToken(token);
            setError('');
          },
          'expired-callback': () => setTurnstileToken(''),
          'error-callback': () => {
            setTurnstileToken('');
            setError(t.securityVerificationFailed);
          },
        });
      })
      .catch(() => {
        if (active) setError(t.securityVerificationFailed);
      });
    return () => {
      active = false;
      if (turnstileWidgetRef.current && window.turnstile) {
        window.turnstile.remove(turnstileWidgetRef.current);
        turnstileWidgetRef.current = null;
      }
      setTurnstileToken('');
    };
  }, [authConfig, mode, t.securityVerificationFailed]);

  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    const form = new FormData(e.currentTarget);
    const password = String(form.get('password') || '');
    if (mode === 'register' && password !== String(form.get('confirmPassword') || '')) {
      setError(t.passwordsDoNotMatch);
      setLoading(false);
      return;
    }
    if (mode === 'register' && !turnstileToken) {
      setError(t.completeSecurityVerification);
      setLoading(false);
      return;
    }
    try {
      const data = await api<{ user: UserAccount }>(mode === 'register' ? '/api/register' : '/api/login', {
        method: 'POST',
        body: JSON.stringify({
          username: String(form.get('username') || ''),
          password,
          ...(mode === 'register' ? { turnstileToken } : {}),
        }),
      });
      onLogin(data.user);
    } catch (err) {
      setError(err instanceof Error ? err.message : t.connectionFailed);
      if (mode === 'register' && turnstileWidgetRef.current && window.turnstile) {
        window.turnstile.reset(turnstileWidgetRef.current);
        setTurnstileToken('');
      }
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

        {authConfig?.registrationEnabled && (
          <div className="grid h-9 grid-cols-2 rounded-md bg-muted p-1" role="tablist" aria-label="Authentication mode">
            <button type="button" role="tab" aria-selected={mode === 'login'} onClick={() => selectMode('login')} className={`rounded-sm text-sm transition-colors ${mode === 'login' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>
              {t.signIn}
            </button>
            <button type="button" role="tab" aria-selected={mode === 'register'} onClick={() => selectMode('register')} className={`rounded-sm text-sm transition-colors ${mode === 'register' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>
              {t.createAccount}
            </button>
          </div>
        )}

        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
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
                <Input id="password" name="password" type={passwordVisible ? 'text' : 'password'} placeholder="••••••••••" className="pl-9 pr-10" autoComplete={mode === 'register' ? 'new-password' : 'current-password'} minLength={mode === 'register' ? 10 : undefined} required />
                <button
                  type="button"
                  className="absolute right-0 top-0 flex h-full w-10 items-center justify-center rounded-r-md text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                  aria-label={passwordVisible ? t.hidePassword : t.showPassword}
                  aria-pressed={passwordVisible}
                  onClick={() => setPasswordVisible((visible) => !visible)}
                >
                  {passwordVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
            </div>
            {mode === 'register' && (
              <div className="space-y-2">
                <label className="text-sm font-medium leading-none" htmlFor="confirmPassword">
                  {t.confirmPassword}
                </label>
                <div className="relative">
                  <Lock className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                  <Input id="confirmPassword" name="confirmPassword" type={confirmPasswordVisible ? 'text' : 'password'} placeholder="••••••••••" className="pl-9 pr-10" autoComplete="new-password" minLength={10} required />
                  <button
                    type="button"
                    className="absolute right-0 top-0 flex h-full w-10 items-center justify-center rounded-r-md text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                    aria-label={confirmPasswordVisible ? t.hidePassword : t.showPassword}
                    aria-pressed={confirmPasswordVisible}
                    onClick={() => setConfirmPasswordVisible((visible) => !visible)}
                  >
                    {confirmPasswordVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                  </button>
                </div>
              </div>
            )}
          </div>

          {mode === 'register' && <div ref={turnstileContainerRef} className="min-h-[65px] w-full overflow-hidden flex justify-center" />}

          {error && (
            <div className="p-3 text-sm bg-destructive/10 text-destructive rounded-md border border-destructive/20 flex items-start gap-2">
              <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
              <p>{error}</p>
            </div>
          )}

          <Button type="submit" className="w-full" disabled={loading}>
            {loading ? <RefreshCw className="h-4 w-4 animate-spin" /> : mode === 'register' ? t.createAccount : t.connectToWorkspace}
          </Button>
        </form>

        <div className="flex justify-center mt-4">
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="sm" className="text-muted-foreground gap-2" onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')}>
              <Globe className="h-4 w-4" />
              {language === 'zh' ? t.chinese : t.english}
              <ChevronDown className="h-3 w-3 opacity-50" />
            </Button>
            <a href="/help" className="inline-flex h-8 items-center justify-center gap-2 rounded-md px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
              <BookOpen className="h-4 w-4" />{language === 'zh' ? '使用帮助' : 'Help'}
            </a>
          </div>
        </div>
      </div>
    </div>
  );
}
