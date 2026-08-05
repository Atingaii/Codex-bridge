import React from 'react';
import { BookOpen, Check, Edit2, GitBranch, MessageSquare, Plus, RefreshCw, Search, Settings, Share2, Terminal, Trash2 } from 'lucide-react';
import type { Session } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { Button } from './ui';
import { cn, displaySessionTitle } from '../lib/utils';

export function SidebarContent({
  groupedSessions,
  activeSession,
  setActiveSession,
  createSession,
  shareSession,
  renameSession,
  deleteSession,
  search,
  setSearch,
  openSettings,
  agentOnline,
  openOrchestration,
  shareCopiedSessionId,
  sharingSessionId,
  deletingSessionId,
  t,
}: {
  groupedSessions: Record<string, Session[]>;
  activeSession: string;
  setActiveSession: (id: string) => void;
  createSession: () => void;
  shareSession: (session: Session) => void;
  renameSession: (session: Session) => void;
  deleteSession: (session: Session) => void;
  search: string;
  setSearch: (value: string) => void;
  openSettings: () => void;
  agentOnline: boolean;
  openOrchestration: () => void;
  shareCopiedSessionId: string;
  sharingSessionId: string;
  deletingSessionId: string;
  t: UIText;
}) {
  return (
    <>
      <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
        <div className="flex items-center gap-2 font-medium">
          <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
            <Terminal className="h-3.5 w-3.5" />
          </div>
          <span className="text-sm">{t.codexBridge}</span>
        </div>
      </div>

      <div className="p-3">
        <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm" onClick={createSession}>
          <Plus className="h-4 w-4" />
          {t.newSession}
        </Button>
        <Button variant="ghost" className="mt-2 w-full justify-start gap-2 h-9 rounded-lg text-muted-foreground" onClick={openOrchestration}>
          <GitBranch className="h-4 w-4" />
          {t.orchestration}
        </Button>
      </div>

      <div className="px-3 pb-2">
        <div className="relative">
          <Search className="absolute left-2.5 top-2 h-3.5 w-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder={t.searchSessions}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="w-full h-8 pl-8 pr-3 text-xs bg-sidebar-accent/50 border border-sidebar-border rounded-md focus:outline-none focus:ring-1 focus:ring-ring transition-all"
          />
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-2 space-y-4 elegant-scrollbar">
        {Object.keys(groupedSessions).length === 0 ? (
          <div className="px-2 py-1.5 text-xs text-muted-foreground">{t.noSessions}</div>
        ) : Object.entries(groupedSessions).map(([date, sessions]) => (
          <div key={date}>
            <h4 className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider mb-1.5 px-2">
              {date}
            </h4>
            <div className="space-y-0.5">
              {sessions.map((session) => (
                <div
                  key={session.id}
                  className={cn(
                    "w-full rounded-md text-sm flex items-center transition-colors group",
                    activeSession === session.id
                      ? "bg-sidebar-accent text-sidebar-accent-foreground font-medium"
                      : "text-sidebar-foreground hover:bg-sidebar-accent/50"
                  )}
                >
                  <button
                    type="button"
                    onClick={() => setActiveSession(session.id)}
                    className="min-w-0 flex-1 px-2 py-1.5 text-left flex items-center gap-2 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    aria-current={activeSession === session.id ? 'page' : undefined}
                  >
                    <MessageSquare className="h-3.5 w-3.5 opacity-70 shrink-0" />
                    <span className="truncate">{displaySessionTitle(session, t)}</span>
                  </button>

                  <div className={cn(
                    "pr-1 flex shrink-0 items-center gap-0.5 transition-opacity",
                    activeSession === session.id ? "opacity-100" : "opacity-100 md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100"
                  )}>
                    <button
                      type="button"
                      className={cn(
                        "h-7 w-7 rounded flex items-center justify-center hover:bg-sidebar-border text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
                        shareCopiedSessionId === session.id && "text-emerald-600 dark:text-emerald-400"
                      )}
                      disabled={sharingSessionId === session.id || deletingSessionId === session.id}
                      onClick={(event) => {
                        event.stopPropagation();
                        shareSession(session);
                      }}
                      aria-label={shareCopiedSessionId === session.id ? t.copied : t.shareConversation}
                      title={shareCopiedSessionId === session.id ? t.copied : t.shareConversation}
                    >
                      {sharingSessionId === session.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : shareCopiedSessionId === session.id ? <Check className="h-3 w-3" /> : <Share2 className="h-3 w-3" />}
                    </button>
                    <button
                      type="button"
                      className="h-7 w-7 rounded flex items-center justify-center hover:bg-sidebar-border text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
                      disabled={deletingSessionId === session.id}
                      onClick={(event) => {
                        event.stopPropagation();
                        renameSession(session);
                      }}
                      aria-label={t.renameSession}
                      title={t.renameSession}
                    >
                      <Edit2 className="h-3 w-3" />
                    </button>
                    <button
                      type="button"
                      className="h-7 w-7 rounded flex items-center justify-center hover:bg-destructive/10 hover:text-destructive text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
                      disabled={deletingSessionId === session.id}
                      onClick={(event) => {
                        event.stopPropagation();
                        deleteSession(session);
                      }}
                      aria-label={deletingSessionId === session.id ? t.deletingSession : t.deleteSession}
                      title={deletingSessionId === session.id ? t.deletingSession : t.deleteSession}
                    >
                      {deletingSessionId === session.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : <Trash2 className="h-3 w-3" />}
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>

      <div className="p-3 border-t border-sidebar-border shrink-0 mt-auto bg-sidebar">
        <a href="/help" className="mb-1 flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground">
          <BookOpen className="h-3.5 w-3.5" />
          <span>{t.help}</span>
        </a>
        <button
          onClick={openSettings}
          className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-sm hover:bg-sidebar-accent transition-colors text-sidebar-foreground"
        >
          <div className="h-6 w-6 rounded-full bg-sidebar-primary/10 flex items-center justify-center">
            <Settings className="h-3.5 w-3.5" />
          </div>
          <span className="flex-1 text-left">{t.settings}</span>
          <div className={cn("h-1.5 w-1.5 rounded-full", agentOnline ? "bg-emerald-500" : "bg-muted-foreground")} title={agentOnline ? t.agentOnline : t.agentOffline} />
        </button>
      </div>
    </>
  );
}
