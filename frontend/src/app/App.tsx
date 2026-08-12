import React, { lazy, Suspense, useCallback, useEffect, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { api } from './lib/api';
import type { UserAccount } from './lib/types';
import { initialLanguage, uiText, type Language } from './lib/i18n';

// Page modules are independent interaction surfaces. Loading them on demand
// keeps initial login and workspace navigation responsive on slower networks.
const ConversationSnapshotPage = lazy(() => import('./pages/ConversationSnapshotPage').then(({ ConversationSnapshotPage }) => ({ default: ConversationSnapshotPage })));
const HelpPage = lazy(() => import('./pages/HelpPage').then(({ HelpPage }) => ({ default: HelpPage })));
const LoginScreen = lazy(() => import('./pages/LoginScreen').then(({ LoginScreen }) => ({ default: LoginScreen })));
const OrchestrationWorkspace = lazy(() => import('./pages/OrchestrationWorkspace').then(({ OrchestrationWorkspace }) => ({ default: OrchestrationWorkspace })));
const OrchestrationStatsPage = lazy(() => import('./pages/OrchestrationStatsPage').then(({ OrchestrationStatsPage }) => ({ default: OrchestrationStatsPage })));
const PublicSharePage = lazy(() => import('./pages/PublicSharePage').then(({ PublicSharePage }) => ({ default: PublicSharePage })));
const UpdatesPage = lazy(() => import('./pages/UpdatesPage').then(({ UpdatesPage }) => ({ default: UpdatesPage })));
const Workspace = lazy(() => import('./pages/Workspace').then(({ Workspace }) => ({ default: Workspace })));
const AdminUsagePage = lazy(() => import('./pages/AdminUsagePage').then(({ AdminUsagePage }) => ({ default: AdminUsagePage })));
const AdminUserUsagePage = lazy(() => import('./pages/AdminUserUsagePage').then(({ AdminUserUsagePage }) => ({ default: AdminUserUsagePage })));

function PageLoading() {
  return (
    <div className="min-h-screen w-full flex items-center justify-center bg-background text-foreground">
      <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
    </div>
  );
}

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

  let page: React.ReactNode;
  if (isShareRoute) {
    page = <PublicSharePage shareID={decodeURIComponent(path.replace(/^\/share\/?/, '').split('/')[0] || '')} t={t} />;
  } else if (isHelpRoute) {
    page = <HelpPage language={language} setLanguage={setLanguage} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} />;
  } else if (isUpdatesRoute) {
    page = <UpdatesPage language={language} setLanguage={setLanguage} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} />;
  } else if (!user) {
    page = <LoginScreen onLogin={setUser} language={language} setLanguage={setLanguage} t={t} />;
  } else if (isSnapshotRoute) {
    page = <ConversationSnapshotPage t={t} />;
  } else if (path.startsWith('/admin')) {
    if (!user.isAdmin) return null;
    const adminUserMatch = path.match(/^\/admin\/usage\/users\/([^/]+)$/);
    page = adminUserMatch
      ? <AdminUserUsagePage userID={decodeURIComponent(adminUserMatch[1])} t={t} navigate={navigate} />
      : <AdminUsagePage t={t} navigate={navigate} />;
  } else if (path.startsWith('/orchestrate')) {
    if (path.startsWith('/orchestrate/stats') || path.startsWith('/orchestrate/usage')) {
      const runId = new URLSearchParams(window.location.search).get('run') || localStorage.getItem('codexBridge.activeOrchestrationRunId') || '';
      page = <OrchestrationStatsPage t={t} navigate={navigate} runId={path.startsWith('/orchestrate/usage') ? '' : runId} />;
    } else {
      page = <OrchestrationWorkspace user={user} onLogout={() => setUser(null)} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} language={language} setLanguage={setLanguage} t={t} canOpenMain path={path} navigate={navigate} />;
    }
  } else {
    page = <Workspace user={user} onLogout={() => setUser(null)} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} language={language} setLanguage={setLanguage} t={t} navigate={navigate} />;
  }
  return <Suspense fallback={<PageLoading />}>{page}</Suspense>;
}
