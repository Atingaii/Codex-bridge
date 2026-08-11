import React, { useEffect } from 'react';
import {
  ArrowRight,
  BarChart3,
  BookOpen,
  Bot,
  CheckCircle2,
  ExternalLink,
  Globe,
  History,
  Moon,
  Network,
  Settings2,
  ShieldCheck,
  Sun,
  Terminal,
  Wrench,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import type { Language } from '../lib/i18n';

type UpdatesPageProps = {
  language: Language;
  setLanguage: (language: Language) => void;
  isDarkMode: boolean;
  setIsDarkMode: (dark: boolean) => void;
};

type UpdateItem = {
  icon: LucideIcon;
  category: string;
  title: string;
  detail: string;
};

const releases: Array<{ date: string; version: string; summary: string; items: UpdateItem[] }> = [
  {
    date: '2026 年 8 月 11 日',
    version: 'v0.3.26',
    summary: '模型配置进入 Hub，补齐升级和原生模型选择体验。',
    items: [
      { icon: Settings2, category: '模型', title: '在 Hub 配置模型供应商', detail: '支持测试 Base URL、加密传输 API Key、发现并选择模型。' },
      { icon: Bot, category: 'CLI', title: '修复原生模型选择器', detail: 'Codex 可识别自定义模型；Claude 不再把内置选项全部映射成同一模型。' },
      { icon: Wrench, category: '升级', title: '修复命令包含更新与重连', detail: '一条命令完成 Bridge 下载、安装、启动，并保留当前权限策略。' },
    ],
  },
  {
    date: '2026 年 8 月 10 日',
    version: 'v0.3.22',
    summary: '多机器切换更稳定，离线内容仍然可读。',
    items: [
      { icon: Network, category: '多机器', title: '离线机器保持选中', detail: '机器掉线后仍可查看历史会话，不再自动跳回第一台机器。' },
    ],
  },
  {
    date: '2026 年 8 月 9 日',
    version: 'v0.3.20',
    summary: '集中提升长编排的可见性、恢复能力与部署体验。',
    items: [
      { icon: BarChart3, category: '统计', title: '新增完整用量视图', detail: '按轮次、CLI、模型、机器和任务查看 Token、调用次数与价格估算。' },
      { icon: Zap, category: '性能', title: '长编排按需加载并合并进度', detail: '减少首屏数据和高频事件写入，降低 Hub、Bridge 与 SQLite 压力。' },
      { icon: CheckCircle2, category: '可靠性', title: '断线、超时和重启可恢复', detail: '短暂网络中断自动重连，卡住的命令和未知运行状态会主动收敛。' },
      { icon: ShieldCheck, category: '管理', title: '管理员用量与会话审计', detail: '提供用户活动、会话内容和编排用量的只读查看入口。' },
      { icon: Terminal, category: '部署', title: 'Bridge 更新自动化', detail: '支持重试、断点续传、curl/wget 回退，并自动重启受管理服务。' },
    ],
  },
];

const highlights = [
  { icon: Settings2, label: '模型切换', value: 'Hub 直接配置' },
  { icon: Network, label: '多机器', value: '离线仍可查看' },
  { icon: Zap, label: '长编排', value: '更轻、更稳定' },
] as const;

export function UpdatesPage({ language, setLanguage, isDarkMode, setIsDarkMode }: UpdatesPageProps) {
  useEffect(() => {
    document.title = 'Codex Bridge 更新记录';
  }, []);

  return (
    <div className="min-h-screen bg-background text-foreground font-sans">
      <header className="sticky top-0 z-40 border-b border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/85">
        <div className="mx-auto flex h-14 max-w-5xl items-center gap-3 px-4 md:px-6">
          <a href="/" className="flex items-center gap-2 font-medium">
            <span className="flex h-7 w-7 items-center justify-center rounded-md bg-primary text-primary-foreground"><Terminal className="h-4 w-4" /></span>
            <span className="text-sm">Codex Bridge</span>
          </a>
          <span className="hidden h-4 w-px bg-border sm:block" />
          <span className="hidden text-sm text-muted-foreground sm:block">更新记录</span>
          <nav className="ml-auto flex items-center gap-1">
            <a href="/help" className="hidden h-8 items-center gap-1.5 rounded-md px-2.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground sm:inline-flex"><BookOpen className="h-3.5 w-3.5" />使用帮助</a>
            <button type="button" className="inline-flex h-8 items-center gap-1.5 rounded-md px-2.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')} aria-label="切换语言"><Globe className="h-3.5 w-3.5" />{language === 'zh' ? '中文' : 'EN'}</button>
            <button type="button" className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => setIsDarkMode(!isDarkMode)} aria-label={isDarkMode ? '切换浅色模式' : '切换深色模式'}>{isDarkMode ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}</button>
            <a href="/" className="ml-1 inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground hover:bg-primary/90">打开应用 <ArrowRight className="h-3.5 w-3.5" /></a>
          </nav>
        </div>
      </header>

      <main className="mx-auto max-w-5xl px-4 pb-24 pt-12 md:px-6 md:pt-16">
        <section className="border-b border-border pb-12">
          <div className="mb-4 flex items-center gap-2 text-xs font-medium text-emerald-600 dark:text-emerald-400"><span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />当前版本 v0.3.27</div>
          <div className="flex flex-col gap-5 md:flex-row md:items-end md:justify-between">
            <div>
              <h1 className="text-3xl font-semibold leading-tight sm:text-4xl">最近更新</h1>
              <p className="mt-4 max-w-2xl text-base leading-7 text-muted-foreground">模型切换、多机器稳定性和长编排体验，最近几次更新集中解决了这些高频问题。</p>
            </div>
            <a href="https://github.com/Atingaii/Codex-bridge/releases/tag/v0.3.27" target="_blank" rel="noreferrer" className="inline-flex h-9 w-fit items-center gap-2 rounded-md border border-border px-3 text-xs font-medium hover:bg-muted">查看 Release <ExternalLink className="h-3.5 w-3.5" /></a>
          </div>
          <div className="mt-8 grid gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-3">
            {highlights.map(({ icon: Icon, label, value }) => (
              <div key={label} className="bg-background p-4">
                <div className="flex items-center gap-2 text-xs text-muted-foreground"><Icon className="h-3.5 w-3.5" />{label}</div>
                <p className="mt-2 text-sm font-medium">{value}</p>
              </div>
            ))}
          </div>
        </section>

        <section className="pt-12" aria-labelledby="timeline-title">
          <div className="mb-8 flex items-center gap-2"><History className="h-4 w-4 text-muted-foreground" /><h2 id="timeline-title" className="text-sm font-semibold">修正时间线</h2></div>
          <div className="space-y-12">
            {releases.map((release) => (
              <article key={release.date} className="grid gap-5 md:grid-cols-[160px_minmax(0,1fr)] md:gap-8">
                <div>
                  <time className="text-sm font-medium">{release.date}</time>
                  <p className="mt-1 font-mono text-[11px] text-muted-foreground">{release.version}</p>
                </div>
                <div className="relative border-l border-border pl-6">
                  <span className="absolute -left-[4.5px] top-1.5 h-2 w-2 rounded-full border-2 border-background bg-emerald-500 ring-1 ring-border" />
                  <p className="mb-5 text-sm leading-6 text-muted-foreground">{release.summary}</p>
                  <div className="divide-y divide-border border-y border-border">
                    {release.items.map(({ icon: Icon, category, title, detail }) => (
                      <div key={title} className="grid gap-2 py-4 sm:grid-cols-[100px_minmax(0,1fr)] sm:gap-4">
                        <div className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground"><Icon className="h-3.5 w-3.5" />{category}</div>
                        <div><h3 className="text-sm font-medium">{title}</h3><p className="mt-1 text-xs leading-5 text-muted-foreground">{detail}</p></div>
                      </div>
                    ))}
                  </div>
                </div>
              </article>
            ))}
          </div>
        </section>
      </main>
    </div>
  );
}
