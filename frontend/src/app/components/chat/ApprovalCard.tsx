import React from 'react';
import { Check, ShieldQuestion, X } from 'lucide-react';
import type { ApprovalRequest, ApprovalStatus } from '../../lib/types';
import type { UIText } from '../../lib/i18n';
import { Button } from '../ui';

export function ApprovalCard({
  item,
  t,
  onDecision,
}: {
  item: { approval: ApprovalRequest; status?: ApprovalStatus };
  t: UIText;
  onDecision: (requestId: string, decision: 'accept' | 'decline' | 'cancel') => void;
}) {
  const pending = !item.status || item.status === 'pending';
  const statusText =
    item.status === 'accepted' ? t.approved :
      item.status === 'declined' ? t.denied :
        item.status === 'canceled' ? t.approvalCanceled :
          t.approvalRequired;
  const approvalTitle = item.approval.kind === 'ccb.terminal_prompt' ? t.browserApproval : t.approvalRequired;
  const detail = [item.approval.command, item.approval.cwd, item.approval.reason].filter(Boolean).join('\n');

  return (
    <div className="w-full max-w-4xl mx-auto rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-3">
      <div className="flex items-start gap-3">
        <div className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300">
          <ShieldQuestion className="h-4 w-4" />
        </div>
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">{approvalTitle}</span>
            <span className="rounded border border-border bg-background/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">{statusText}</span>
          </div>
          {detail && (
            <pre className="max-h-32 overflow-x-auto whitespace-pre-wrap rounded-md border border-border bg-background/70 p-2 font-mono text-[11px] text-muted-foreground elegant-scrollbar">
              {detail}
            </pre>
          )}
          {pending && (
            <div className="flex gap-2">
              <Button size="sm" type="button" className="h-8" onClick={() => onDecision(item.approval.requestId, 'accept')}>
                <Check className="mr-1.5 h-3.5 w-3.5" />
                {t.approve}
              </Button>
              <Button variant="secondary" size="sm" type="button" className="h-8" onClick={() => onDecision(item.approval.requestId, 'decline')}>
                <X className="mr-1.5 h-3.5 w-3.5" />
                {t.deny}
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
