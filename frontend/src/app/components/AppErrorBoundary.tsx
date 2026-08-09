import React from 'react';
import { AlertCircle, RefreshCw } from 'lucide-react';

type AppErrorBoundaryState = {
  failed: boolean;
};

export class AppErrorBoundary extends React.Component<React.PropsWithChildren, AppErrorBoundaryState> {
  state: AppErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): AppErrorBoundaryState {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('Codex Bridge render failure', error, info);
  }

  render() {
    if (!this.state.failed) return this.props.children;

    const chinese = (localStorage.getItem('codexBridge.language') || navigator.language).toLowerCase().startsWith('zh');
    return (
      <main className="min-h-screen bg-background px-4 text-foreground flex items-center justify-center">
        <section className="w-full max-w-md border border-border bg-card p-6 text-center shadow-sm rounded-lg">
          <AlertCircle className="mx-auto mb-3 h-6 w-6 text-destructive" />
          <h1 className="text-base font-semibold">{chinese ? '页面暂时无法显示' : 'The page could not be displayed'}</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            {chinese ? '后台任务不会因此停止。请重新加载页面以恢复显示。' : 'The background task is not stopped. Reload the page to restore the view.'}
          </p>
          <button
            type="button"
            className="mt-5 inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground"
            onClick={() => window.location.reload()}
          >
            <RefreshCw className="h-4 w-4" />
            {chinese ? '重新加载' : 'Reload'}
          </button>
        </section>
      </main>
    );
  }
}
