import React, { useEffect, useMemo, useState } from 'react';
import { Activity, AlertCircle, Check, Command, GitBranch, ImagePlus, Lock, MessageSquare, RefreshCw, Send, Share2 } from 'lucide-react';
import { api } from '../lib/api';
import type { PublicSharePayload } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { MessageItem } from '../components/chat/MessageItem';
import { OrchestrationEventItem } from '../components/OrchestrationComponents';
import { Button } from '../components/ui';
import { sessionDateLabel, visibleOrchestrationEvents } from '../lib/utils';

export function PublicSharePage({ shareID, t }: { shareID: string; t: UIText }) {
  const [payload, setPayload] = useState<PublicSharePayload | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const visibleEvents = useMemo(() => {
    if (!payload?.run) return [];
    return visibleOrchestrationEvents(payload.events || [], payload.run.id, payload.run, t);
  }, [payload, t]);

  useEffect(() => {
    let stopped = false;
    if (!shareID) {
      setError(t.failedLoadShare);
      setLoading(false);
      return () => {
        stopped = true;
      };
    }
    setLoading(true);
    setError('');
    api<PublicSharePayload>(`/api/public/shares/${encodeURIComponent(shareID)}`)
      .then((data) => {
        if (!stopped) setPayload(data);
      })
      .catch((err) => {
        if (!stopped) setError(err instanceof Error ? err.message : t.failedLoadShare);
      })
      .finally(() => {
        if (!stopped) setLoading(false);
      });
    return () => {
      stopped = true;
    };
  }, [shareID, t.failedLoadShare]);

  const title = payload?.share.title || payload?.session?.title || payload?.run?.title || t.publicShare;
  const isOrchestration = payload?.share.kind === 'orchestration';
  const messages = payload?.messages || [];
  const goToLogin = () => {
    window.location.href = '/';
  };

  return (
    <div className="h-screen w-full flex bg-background text-foreground overflow-hidden font-sans">
      <aside className="hidden md:flex w-[260px] flex-col border-r border-sidebar-border bg-sidebar">
        <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
          <div className="flex items-center gap-2 font-medium">
            <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
              <Share2 className="h-3.5 w-3.5" />
            </div>
            <span className="text-sm">{t.publicShare}</span>
          </div>
        </div>

        <div className="p-3 space-y-2">
          <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm pointer-events-none" disabled>
            {isOrchestration ? <GitBranch className="h-4 w-4" /> : <MessageSquare className="h-4 w-4" />}
            <span className="truncate">{title}</span>
          </Button>
          <Button variant="ghost" className="w-full justify-start gap-2 h-9 rounded-lg" onClick={goToLogin}>
            <Lock className="h-4 w-4" />
            {t.signInToContinue}
          </Button>
        </div>

        <div className="flex-1 overflow-y-auto px-3 py-2 elegant-scrollbar">
          <div>
            <h4 className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider mb-1.5 px-2">
              {payload?.share.updatedAt ? sessionDateLabel(payload.share.updatedAt, t) : t.readOnlySnapshot}
            </h4>
            <div className="w-full text-left px-2 py-1.5 rounded-md text-sm flex items-center gap-2 bg-sidebar-accent text-sidebar-accent-foreground font-medium">
              {isOrchestration ? <GitBranch className="h-3.5 w-3.5 opacity-70 shrink-0" /> : <MessageSquare className="h-3.5 w-3.5 opacity-70 shrink-0" />}
              <span className="truncate">{title}</span>
            </div>
          </div>
        </div>

        <div className="p-3 border-t border-sidebar-border shrink-0 mt-auto bg-sidebar">
          <div className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-sidebar-foreground">
            <div className="h-6 w-6 rounded-full bg-sidebar-primary/10 flex items-center justify-center">
              <Check className="h-3.5 w-3.5" />
            </div>
            <span className="flex-1 text-left">{t.readOnlySnapshot}</span>
            <div className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
          </div>
        </div>
      </aside>

      <main className="flex-1 flex flex-col min-w-0 h-full">
        <header className="h-14 shrink-0 border-b border-border flex items-center justify-between px-3 md:px-4 bg-background z-10">
          <div className="flex items-center gap-2 min-w-0">
            <Share2 className="h-4 w-4 text-muted-foreground shrink-0" />
            <span className="text-sm font-medium truncate">{title}</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden sm:inline-flex rounded-full border border-border bg-muted/40 px-2.5 py-1 text-xs text-muted-foreground">
              {t.readOnlySnapshot}
            </span>
            <Button variant="secondary" size="sm" className="h-8 gap-1.5 rounded-lg" onClick={goToLogin}>
              <Lock className="h-3.5 w-3.5" />
              {t.signInToContinue}
            </Button>
          </div>
        </header>

        <div className="bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          <div className="flex items-center gap-1.5">
            {isOrchestration ? <GitBranch className="h-3.5 w-3.5" /> : <MessageSquare className="h-3.5 w-3.5" />}
            <span>{t.publicShare}: {payload?.share.kind || '-'}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            <span>{t.status}: {payload?.run?.status || t.ready}</span>
          </div>
          {payload?.share.updatedAt && (
            <div className="flex items-center gap-1.5">
              <Command className="h-3.5 w-3.5" />
              <span>{t.thread}: {sessionDateLabel(payload.share.updatedAt, t)}</span>
            </div>
          )}
        </div>

        <div className="relative flex-1 min-h-0">
          <div className="h-full overflow-y-auto p-4 md:p-6 space-y-4 elegant-scrollbar">
            {loading ? (
              <div className="h-full flex items-center justify-center">
                <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : error ? (
              <div className="mx-auto mt-10 flex max-w-lg items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>{error}</span>
              </div>
            ) : isOrchestration ? (
              visibleEvents.length > 0 ? (
                visibleEvents.map((event) => <OrchestrationEventItem key={event.key} item={event} t={t} />)
              ) : payload?.run?.prompt ? (
                <MessageItem
                  msg={{ id: `${payload.run.id}:prompt`, type: 'message', role: 'user', content: payload.run.prompt, createdAt: payload.run.createdAt }}
                  t={t}
                  readOnly
                />
              ) : null
            ) : (
              messages.map((message) => (
                <MessageItem
                  key={message.id}
                  msg={{ id: message.id, type: 'message', role: message.role, content: message.content, createdAt: message.createdAt }}
                  t={t}
                  readOnly
                />
              ))
            )}
            <div className="h-4" />
          </div>
        </div>

        <div className="shrink-0 p-4 border-t border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
          <div className="max-w-4xl mx-auto flex flex-col bg-card border border-border rounded-xl shadow-sm">
            <textarea
              className="w-full bg-transparent border-0 resize-none p-3 text-sm focus:outline-none focus:ring-0 min-h-[60px] max-h-[120px] text-muted-foreground"
              value=""
              placeholder={t.askCodex}
              disabled
              readOnly
            />
            <div className="flex items-center justify-between p-2 pt-0">
              <Button variant="ghost" size="icon" type="button" className="h-8 w-8 text-muted-foreground rounded-lg" disabled>
                <ImagePlus className="h-4 w-4" />
              </Button>
              <Button size="sm" type="button" className="h-8 px-3 rounded-lg gap-1.5 text-xs font-medium" disabled>
                {t.send}
                <Send className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
          <div className="text-center mt-2">
            <span className="text-[10px] text-muted-foreground/60 font-medium">{t.verifyNotice}</span>
          </div>
        </div>
      </main>
    </div>
  );
}
