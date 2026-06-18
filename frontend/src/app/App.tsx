import React, { useCallback, useEffect, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { api } from './lib/api';
import type { UserAccount } from './lib/types';
import { initialLanguage, uiText, type Language } from './lib/i18n';
import { ConversationSnapshotPage } from './pages/ConversationSnapshotPage';
import { LoginScreen } from './pages/LoginScreen';
import { OrchestrationWorkspace } from './pages/OrchestrationWorkspace';
import { PublicSharePage } from './pages/PublicSharePage';
import { Workspace } from './pages/Workspace';

export default function App() {
  const [user, setUser] = useState<UserAccount | null>(null);
  const [booting, setBooting] = useState(true);
  const [isDarkMode, setIsDarkMode] = useState(() => localStorage.getItem('codexBridge.theme') !== 'light');
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const [path, setPath] = useState(() => window.location.pathname);
  const t = uiText[language];
  const isSnapshotRoute = path.startsWith('/conversation-snapshot');
  const isShareRoute = path.startsWith('/share/');

  useEffect(() => {
    document.documentElement.classList.toggle('dark', isDarkMode);
    localStorage.setItem('codexBridge.theme', isDarkMode ? 'dark' : 'light');
  }, [isDarkMode]);

  useEffect(() => {
    document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en';
    localStorage.setItem('codexBridge.language', language);
  }, [language]);

  useEffect(() => {
    if (isShareRoute) {
      setBooting(false);
      return;
    }
    api<{ user: UserAccount }>('/api/me')
      .then((data) => setUser(data.user))
      .catch(() => setUser(null))
      .finally(() => setBooting(false));
  }, [isShareRoute]);

  useEffect(() => {
    const handlePop = () => setPath(window.location.pathname);
    window.addEventListener('popstate', handlePop);
    return () => window.removeEventListener('popstate', handlePop);
  }, []);

  useEffect(() => {
    if (user && !user.isAdmin && !path.startsWith('/orchestrate') && !path.startsWith('/conversation-snapshot') && !path.startsWith('/share/')) {
      window.history.replaceState({}, '', '/orchestrate');
      setPath('/orchestrate');
    }
  }, [path, user]);

  const navigate = useCallback((nextPath: string, options: { replace?: boolean } = {}) => {
    if (user && !user.isAdmin && !nextPath.startsWith('/orchestrate') && !nextPath.startsWith('/conversation-snapshot') && !nextPath.startsWith('/share/')) {
      nextPath = '/orchestrate';
    }
    if (window.location.pathname !== nextPath) {
      if (options.replace) {
        window.history.replaceState({}, '', nextPath);
      } else {
        window.history.pushState({}, '', nextPath);
      }
      setPath(nextPath);
    }
  }, [user]);

  if (booting) {
    return (
      <div className="min-h-screen w-full flex items-center justify-center bg-background text-foreground">
        <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (isShareRoute) {
    return <PublicSharePage shareID={decodeURIComponent(path.replace(/^\/share\/?/, '').split('/')[0] || '')} t={t} />;
  }

  if (!user) {
    return <LoginScreen onLogin={setUser} language={language} setLanguage={setLanguage} t={t} />;
  }

  if (isSnapshotRoute) {
    return <ConversationSnapshotPage t={t} />;
  }

  if (!user.isAdmin || path.startsWith('/orchestrate')) {
    return (
      <OrchestrationWorkspace
        user={user}
        onLogout={() => setUser(null)}
        isDarkMode={isDarkMode}
        setIsDarkMode={setIsDarkMode}
        language={language}
        setLanguage={setLanguage}
        t={t}
        canOpenMain={Boolean(user.isAdmin)}
        path={path}
        navigate={navigate}
      />
    );
  }

  return (
    <Workspace
      user={user}
      onLogout={() => setUser(null)}
      isDarkMode={isDarkMode}
      setIsDarkMode={setIsDarkMode}
      language={language}
      setLanguage={setLanguage}
      t={t}
      navigate={navigate}
    />
  );
}
