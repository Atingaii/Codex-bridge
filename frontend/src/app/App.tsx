import React, { useCallback, useEffect, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { api } from './lib/api';
import type { UserAccount } from './lib/types';
import { initialLanguage, uiText, type Language } from './lib/i18n';
import { ConversationSnapshotPage } from './pages/ConversationSnapshotPage';
import { HelpPage } from './pages/HelpPage';
import { LoginScreen } from './pages/LoginScreen';
import { OrchestrationWorkspace } from './pages/OrchestrationWorkspace';
import { OrchestrationStatsPage } from './pages/OrchestrationStatsPage';
import { PublicSharePage } from './pages/PublicSharePage';
import { UpdatesPage } from './pages/UpdatesPage';
import { Workspace } from './pages/Workspace';
import { AdminUsagePage } from './pages/AdminUsagePage';
import { AdminUserUsagePage } from './pages/AdminUserUsagePage';

export default function App() {
  const [user, setUser] = useState<UserAccount | null>(null);
  const [booting, setBooting] = useState(true);
  const [isDarkMode, setIsDarkMode] = useState(() => localStorage.getItem('codexBridge.theme') !== 'light');
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const [path, setPath] = useState(() => window.location.pathname);
  const t = uiText[language];
  const isSnapshotRoute = path.startsWith('/conversation-snapshot');
  const isShareRoute = path.startsWith('/share/');
  const isHelpRoute = path === '/help' || path === '/help/' || path === '/hlep' || path === '/hlep/';
  const isUpdatesRoute = path === '/updates' || path === '/updates/';

  useEffect(() => {
    document.documentElement.classList.toggle('dark', isDarkMode);
    localStorage.setItem('codexBridge.theme', isDarkMode ? 'dark' : 'light');
  }, [isDarkMode]);

  useEffect(() => {
    document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en';
    localStorage.setItem('codexBridge.language', language);
  }, [language]);

  useEffect(() => {
    if (isShareRoute || isHelpRoute || isUpdatesRoute) {
      setBooting(false);
      return;
    }
    api<{ user: UserAccount }>('/api/me')
      .then((data) => setUser(data.user))
      .catch(() => setUser(null))
      .finally(() => setBooting(false));
  }, [isHelpRoute, isShareRoute, isUpdatesRoute]);

  useEffect(() => {
    const handlePop = () => setPath(window.location.pathname);
    window.addEventListener('popstate', handlePop);
    return () => window.removeEventListener('popstate', handlePop);
  }, []);

  useEffect(() => {
    if (!booting && user && path.startsWith('/admin') && !user.isAdmin) {
      window.history.replaceState({}, '', '/');
      setPath('/');
    }
  }, [booting, path, user]);

  const navigate = useCallback((nextPath: string, options: { replace?: boolean } = {}) => {
    if (window.location.pathname !== nextPath) {
      if (options.replace) {
        window.history.replaceState({}, '', nextPath);
      } else {
        window.history.pushState({}, '', nextPath);
      }
      setPath(nextPath);
    }
  }, []);

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

  if (isHelpRoute) {
    return <HelpPage language={language} setLanguage={setLanguage} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} />;
  }

  if (isUpdatesRoute) {
    return <UpdatesPage language={language} setLanguage={setLanguage} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} />;
  }

  if (!user) {
    return <LoginScreen onLogin={setUser} language={language} setLanguage={setLanguage} t={t} />;
  }

  if (isSnapshotRoute) {
    return <ConversationSnapshotPage t={t} />;
  }

  if (path.startsWith('/admin')) {
    if (!user.isAdmin) return null;
    const adminUserMatch = path.match(/^\/admin\/usage\/users\/([^/]+)$/);
    if (adminUserMatch) return <AdminUserUsagePage userID={decodeURIComponent(adminUserMatch[1])} t={t} navigate={navigate} />;
    return <AdminUsagePage t={t} navigate={navigate} />;
  }

  if (path.startsWith('/orchestrate')) {
    if (path.startsWith('/orchestrate/stats') || path.startsWith('/orchestrate/usage')) {
      const runId = new URLSearchParams(window.location.search).get('run') || localStorage.getItem('codexBridge.activeOrchestrationRunId') || '';
      return <OrchestrationStatsPage t={t} navigate={navigate} runId={path.startsWith('/orchestrate/usage') ? '' : runId} />;
    }
    return (
      <OrchestrationWorkspace
        user={user}
        onLogout={() => setUser(null)}
        isDarkMode={isDarkMode}
        setIsDarkMode={setIsDarkMode}
        language={language}
        setLanguage={setLanguage}
        t={t}
        canOpenMain
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
