import React from 'react';
import { Activity, ArrowLeft, Check, ChevronDown, Command, FileText, ImagePlus, MessageSquare, Send, Server, Terminal } from 'lucide-react';
import type { ChatItem } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { MessageItem } from '../components/chat/MessageItem';
import { Button } from '../components/ui';
import { formatTime } from '../lib/utils';

type SnapshotItem =
  | { id: string; type: 'message'; role: 'user' | 'assistant' | 'system'; content: string; createdAt: number }
  | { id: string; type: 'tool'; title: string; command: string; output: string; createdAt: number; status?: string; kind?: string; defaultOpen?: boolean };

const conversationSnapshotItems: SnapshotItem[] = [
  {
    id: 'snapshot-user-task',
    type: 'message',
    role: 'user',
    createdAt: 1779634200,
    content: [
      '已上传了三个文件。Model.thy是HWQueue的Isabelle模型，现在想证明Termination.thy中的termination modify_lin。请你填上证明中的sorry。',
      '',
      'modify_lin的可终止性证明会非常复杂，建议你们先讨论出一个可用的证明框架，里面可以用sorry填充。但是不能更改原来的模型，也不能自行引入公理。',
      '',
      '我建议你们还是一步一步来，先生成一个可以编译通过的证明框架。checker可以接受证明框架里存在sorry。关于这个任务要求你们先商量出用户的真实意图再去做，放到该路径下新建的一个文件夹中',
      '',
      '上传文件：`Model.thy`、`Termination.thy`、`ROOT`',
    ].join('\n'),
  },
  {
    id: 'snapshot-assistant-implementer',
    type: 'message',
    role: 'assistant',
    createdAt: 1779641618,
    content: [
      '最终编译成功、应该以它为准的是：',
      '',
      '`/home/zy/os/termination_framework`',
      '',
      '验证命令是：',
      '',
      '```bash',
      'isabelle build -c -D /home/zy/os/termination_framework',
      '```',
      '',
      '结果已经成功通过。',
      '',
      '补充：隐藏目录 `/home/zy/os/.codex-bridge/orchestrations/orc_abd603dc6f82ab5f2a90c16f415979a3/termination_framework` 也曾经构建成功，但那是 orchestration 运行中的中间副本。用户可见、最终应查看和提交的是 `/home/zy/os/termination_framework`。',
      '',
      'Msg: to=user; intent=final; need=none',
      'Handoff: status=resolved; changed=none; verified=isabelle build -c -D /home/zy/os/termination_framework; next=none; risks=proof framework still contains sorry placeholders',
    ].join('\n'),
  },
  {
    id: 'snapshot-tool-files',
    type: 'tool',
    title: '命令',
    kind: 'command.end',
    command: '/bin/bash -lc "find /home/zy/os -maxdepth 3 -type f \\( -name ROOT -o -name Termination.thy -o -name Model.thy \\) | sort"',
    output: [
      '/home/zy/os/termination_framework/Model.thy',
      '/home/zy/os/termination_framework/ROOT',
      '/home/zy/os/termination_framework/Termination.thy',
      '',
    ].join('\n'),
    createdAt: 1779641636,
    status: 'completed',
    defaultOpen: false,
  },
  {
    id: 'snapshot-tool-risks',
    type: 'tool',
    title: '命令',
    kind: 'command.end',
    command: '/bin/bash -lc \'rg -n "termination modify_lin|sorry|modify_lin_.*decreases|quick_and_dirty" /home/zy/os/termination_framework\'',
    output: [
      '/home/zy/os/termination_framework/Termination.thy:24:lemma modify_lin_last_enq_branch_decreases:',
      '/home/zy/os/termination_framework/Termination.thy:38:  sorry',
      '/home/zy/os/termination_framework/Termination.thy:40:lemma modify_lin_o1_before_bt_branch_decreases:',
      '/home/zy/os/termination_framework/Termination.thy:57:  sorry',
      '/home/zy/os/termination_framework/Termination.thy:59:lemma modify_lin_b_before_o1_branch_decreases:',
      '/home/zy/os/termination_framework/Termination.thy:76:  sorry',
      '/home/zy/os/termination_framework/Termination.thy:78:lemma modify_lin_unordered_branch_decreases:',
      '/home/zy/os/termination_framework/Termination.thy:96:  sorry',
      '/home/zy/os/termination_framework/Termination.thy:98:termination modify_lin',
      '/home/zy/os/termination_framework/Termination.thy:102:  sorry',
      '/home/zy/os/termination_framework/ROOT:2:  options [document = false, quick_and_dirty = true]',
      '',
    ].join('\n'),
    createdAt: 1779641636,
    status: 'completed',
    defaultOpen: false,
  },
  {
    id: 'snapshot-tool-build',
    type: 'tool',
    title: '命令',
    kind: 'command.end',
    command: "/bin/bash -lc 'isabelle build -c -D /home/zy/os/termination_framework'",
    output: [
      'Cleaned Termination_Framework',
      'Running Termination_Framework ...',
      'Finished Termination_Framework (0:00:06 elapsed time, 0:00:06 cpu time, factor 1.04)',
      '0:00:10 elapsed time, 0:00:06 cpu time, factor 0.60',
      '',
    ].join('\n'),
    createdAt: 1779641653,
    status: 'completed',
    defaultOpen: true,
  },
  {
    id: 'snapshot-assistant-final',
    type: 'message',
    role: 'assistant',
    createdAt: 1779641669,
    content: [
      '最终成功构建的是：',
      '',
      '`/home/zy/os/termination_framework`',
      '',
      '我刚重新验证了这个目录：',
      '',
      '```bash',
      'isabelle build -c -D /home/zy/os/termination_framework',
      '```',
      '',
      '结果 exit 0，`Termination_Framework` 构建通过。',
      '',
      '注意：这表示“证明框架可编译通过”，不是完整证明已经完成。当前 `Termination.thy` 里仍有 5 个 `sorry`，并且 `ROOT` 使用了 `quick_and_dirty = true` 来允许带 `sorry` 的批量构建。',
      '',
      'Msg: to=user; intent=final; need=none',
      'Handoff: status=resolved; changed=none; verified=isabelle build -c -D /home/zy/os/termination_framework; next=prove remaining sorry placeholders; risks=framework passes with quick_and_dirty but termination proof is not complete',
    ].join('\n'),
  },
  {
    id: 'snapshot-tool-glob',
    type: 'tool',
    title: '命令',
    kind: 'command.end',
    command: 'Glob termination_framework/**',
    output: [
      'termination_framework/Model.thy',
      'termination_framework/Termination.thy',
      'termination_framework/ROOT',
    ].join('\n'),
    createdAt: 1779641678,
    status: 'completed',
    defaultOpen: false,
  },
  {
    id: 'snapshot-assistant-verifier',
    type: 'message',
    role: 'assistant',
    createdAt: 1779641684,
    content: [
      '最终成功编译的是：',
      '',
      '`/home/zy/os/termination_framework`',
      '',
      '里面关键文件是：',
      '',
      '- `/home/zy/os/termination_framework/Termination.thy`',
      '- `/home/zy/os/termination_framework/Model.thy`',
      '- `/home/zy/os/termination_framework/ROOT`',
      '',
      '已报告通过的命令是：',
      '',
      '```bash',
      'isabelle build -c -D /home/zy/os/termination_framework',
      '```',
      '',
      '注意：这是**可编译的证明框架**，不是完全无 `sorry` 的最终证明；风险仍是 `Termination.thy` 中还保留了若干 `sorry` 占位。',
      '',
      'Msg: to=user; intent=final; need=none',
      'Handoff: status=resolved; changed=none; verified=isabelle build -c -D /home/zy/os/termination_framework; next=prove remaining sorry placeholders; risks=proof framework still contains sorry placeholders',
    ].join('\n'),
  },
];
const conversationSnapshotDateLabel = '2026-05-24';

export function ConversationSnapshotPage({ t }: { t: UIText }) {
  return (
    <div className="h-screen w-full flex bg-background text-foreground overflow-hidden font-sans">
      <aside className="hidden md:flex w-[260px] flex-col border-r border-sidebar-border bg-sidebar">
        <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
          <div className="flex items-center gap-2 font-medium">
            <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
              <FileText className="h-3.5 w-3.5" />
            </div>
            <span className="text-sm">{t.conversationSnapshot}</span>
          </div>
        </div>

        <div className="p-3 space-y-2">
          <Button variant="ghost" className="w-full justify-start gap-2 h-9 rounded-lg pointer-events-none opacity-60" disabled>
            <ArrowLeft className="h-4 w-4" />
            {t.codexBridge}
          </Button>
          <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm pointer-events-none" disabled>
            <MessageSquare className="h-4 w-4" />
            termination framework
          </Button>
        </div>

        <div className="flex-1 overflow-y-auto px-3 py-2 elegant-scrollbar">
          <div>
            <h4 className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider mb-1.5 px-2">
              {conversationSnapshotDateLabel}
            </h4>
            <div className="w-full text-left px-2 py-1.5 rounded-md text-sm flex items-center gap-2 bg-sidebar-accent text-sidebar-accent-foreground font-medium">
              <MessageSquare className="h-3.5 w-3.5 opacity-70 shrink-0" />
              <span className="truncate">termination framework</span>
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
            <Button variant="ghost" size="icon" className="text-muted-foreground pointer-events-none opacity-60" disabled>
              <ArrowLeft className="h-5 w-5" />
            </Button>
            <div className="h-4 w-px bg-border mx-1 hidden md:block" />
            <FileText className="h-4 w-4 text-muted-foreground shrink-0" />
            <span className="text-sm font-medium truncate">termination framework</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden sm:inline-flex rounded-full border border-border bg-muted/40 px-2.5 py-1 text-xs text-muted-foreground">
              {t.readOnlySnapshot}
            </span>
          </div>
        </header>

        <div className="bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          <div className="flex items-center gap-1.5">
            <Server className="h-3.5 w-3.5" />
            <span>{t.runner}: orchestration snapshot</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            <span>{t.thread}: /home/zy/os/termination_framework</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Command className="h-3.5 w-3.5" />
            <span>{t.status}: {t.ready}</span>
          </div>
        </div>

        <div className="relative flex-1 min-h-0">
          <div className="h-full overflow-y-auto p-4 md:p-6 space-y-4 elegant-scrollbar">
            {conversationSnapshotItems.map((item) => item.type === 'message'
              ? <SnapshotMessageItem key={item.id} item={item} t={t} />
              : <SnapshotToolBlock key={item.id} item={item} t={t} />
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
              <div className="flex items-center gap-1">
                <Button variant="ghost" size="icon" type="button" className="h-8 w-8 text-muted-foreground rounded-lg" disabled>
                  <ImagePlus className="h-4 w-4" />
                </Button>
              </div>
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

function SnapshotMessageItem({ item, t }: { item: Extract<SnapshotItem, { type: 'message' }>; t: UIText }) {
  const msg: Extract<ChatItem, { type: 'message' }> = {
    id: item.id,
    type: 'message',
    role: item.role,
    content: item.content,
    createdAt: item.createdAt,
  };
  return <MessageItem msg={msg} t={t} readOnly />;
}

function SnapshotToolBlock({ item, t }: { item: Extract<SnapshotItem, { type: 'tool' }>; t: UIText }) {
  return (
    <details className="w-full max-w-4xl mx-auto mt-2 bg-muted/30 border border-border rounded-lg overflow-hidden text-[13px] group/tool" open={item.defaultOpen}>
      <summary className="flex cursor-pointer items-center gap-2 px-3 py-1.5 bg-muted/50 border-b border-border marker:content-none">
        <Terminal className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="font-medium text-xs">{item.title}</span>
        {item.kind && <span className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">{item.kind}</span>}
        <span className="ml-auto text-xs text-muted-foreground font-mono truncate max-w-[260px]">{item.command}</span>
        {item.status && <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">{item.status}</span>}
        <span className="shrink-0 text-[10px] text-muted-foreground">{formatTime(item.createdAt)}</span>
        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground opacity-50 transition-transform group-open/tool:rotate-180" />
      </summary>
      <div className="grid gap-2 p-3 bg-background/50">
        <pre className="overflow-x-auto whitespace-pre-wrap rounded-md border border-border bg-muted/40 p-3 font-mono text-[11px] leading-relaxed text-foreground/80 elegant-scrollbar">
          {item.command}
        </pre>
        <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-md border border-border bg-muted/20 p-3 font-mono text-[11px] leading-relaxed text-muted-foreground elegant-scrollbar">
          {item.output}
        </pre>
      </div>
    </details>
  );
}
