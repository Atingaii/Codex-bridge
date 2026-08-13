import React, { useEffect, useState } from 'react';
import {
  ArrowRight,
  Check,
  CheckCircle2,
  ChevronRight,
  CircleHelp,
  Cloud,
  Code2,
  Copy,
  ExternalLink,
  FileCheck2,
  GitBranch,
  Globe,
  KeyRound,
  Laptop,
  Menu,
  MessageSquare,
  Moon,
  History,
  Network,
  Scale,
  Settings,
  ShieldCheck,
  Sparkles,
  Sun,
  Terminal,
  UsersRound,
  X,
  Zap,
} from 'lucide-react';
import type { Language } from '../lib/i18n';

type HelpPageProps = {
  language: Language;
  setLanguage: (language: Language) => void;
  isDarkMode: boolean;
  setIsDarkMode: (dark: boolean) => void;
};

type FigureProps = {
  src: string;
  alt: string;
  caption: string;
  number: number;
};

const navItems = [
  ['start', '快速开始'],
  ['account', '注册与登录'],
  ['endpoint', '连接 CLI 端点'],
  ['chat', '单 CLI 对话'],
  ['orchestration', '编排总览'],
  ['collaboration', '协作模式'],
  ['debate', '辩论模式'],
  ['formal-proof', '形式化验证'],
  ['context', '节省上下文'],
  ['sharing', '分享与会话'],
  ['settings', '设置与维护'],
  ['troubleshooting', '排错清单'],
] as const;

const collaborationDemo = `目标：在不改变公开 API 的前提下，为当前 Go 项目增加请求幂等性。

验收条件：
1. 同一 Idempotency-Key 只执行一次写入；
2. 并发请求返回同一结果；
3. 新增竞态测试并运行 go test -race ./...；
4. 不引入新的外部服务。

分工建议：先由 Claude 梳理不变量与失败路径，再由 Codex 实现和测试；最后一轮只核对验收条件与测试证据。`;

const debateDemo = `命题：把当前证明缓存从进程内 map 改为 SQLite 是否会破坏运行隔离？

请围绕以下可证伪主张辩论：
- 并发写入不会把不同 user_id 的证明状态混合；
- Hub 重启后恢复语义与现有协议一致；
- 最终方案不增加 Bridge 对 Hub 的入站连接。

要求：每个反对意见必须给出反例或复现实验；最终结论列出证据、残余风险和最小实现方案。`;

const formalProofDemo = `目标系统：Coq/Rocq
工作目录：选择包含 _CoqProject 的目录
附件：Task.v、Contract.v（只上传本题相关文件）

证明目标：完成 theorem reverse_acc_correct，不允许 Admitted、Axiom 或弱化命题。

验收命令：coqc Contract.v && coqc Task.v
额外检查：搜索 admitted/axiom；说明使用的归纳不变量；最终结论引用成功命令和修改文件。`;

export function HelpPage({ language, setLanguage, isDarkMode, setIsDarkMode }: HelpPageProps) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [copied, setCopied] = useState('');
  const [activeSection, setActiveSection] = useState('start');

  useEffect(() => {
    document.title = 'ProofBridge 使用帮助';
    const sections = navItems
      .map(([id]) => document.getElementById(id))
      .filter(Boolean) as HTMLElement[];
    const update = () => {
      const current = sections.slice().reverse().find((section) => section.getBoundingClientRect().top <= 120);
      if (current) setActiveSection(current.id);
    };
    update();
    window.addEventListener('scroll', update, { passive: true });
    return () => window.removeEventListener('scroll', update);
  }, []);

  const copyDemo = async (key: string, value: string) => {
    await navigator.clipboard.writeText(value);
    setCopied(key);
    window.setTimeout(() => setCopied(''), 1400);
  };

  return (
    <div className="min-h-screen bg-background text-foreground font-sans">
      <header className="sticky top-0 z-40 border-b border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/85">
        <div className="mx-auto flex h-14 max-w-[1440px] items-center gap-3 px-4 md:px-6">
          <button type="button" className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted lg:hidden" onClick={() => setMenuOpen(true)} aria-label="打开目录">
            <Menu className="h-4 w-4" />
          </button>
          <a href="/" className="flex items-center gap-2 font-medium">
            <span className="flex h-7 w-7 items-center justify-center rounded-md bg-primary text-primary-foreground"><Terminal className="h-4 w-4" /></span>
            <span className="text-sm">ProofBridge</span>
          </a>
          <span className="hidden h-4 w-px bg-border sm:block" />
          <span className="hidden text-sm text-muted-foreground sm:block">使用帮助</span>
          <div className="ml-auto flex items-center gap-1">
            <a href="/updates" className="hidden h-8 items-center gap-1.5 rounded-md px-2.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground sm:inline-flex"><History className="h-3.5 w-3.5" />更新记录</a>
            <button type="button" className="inline-flex h-8 items-center gap-1.5 rounded-md px-2.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')} aria-label="切换语言">
              <Globe className="h-3.5 w-3.5" />{language === 'zh' ? '中文' : 'EN'}
            </button>
            <button type="button" className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => setIsDarkMode(!isDarkMode)} aria-label={isDarkMode ? '切换浅色模式' : '切换深色模式'}>
              {isDarkMode ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </button>
            <a href="/" className="ml-1 inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground hover:bg-primary/90">
              打开应用 <ArrowRight className="h-3.5 w-3.5" />
            </a>
          </div>
        </div>
      </header>

      {menuOpen && (
        <div className="fixed inset-0 z-50 lg:hidden">
          <button type="button" className="absolute inset-0 bg-black/45" onClick={() => setMenuOpen(false)} aria-label="关闭目录" />
          <aside className="absolute inset-y-0 left-0 w-[286px] border-r border-border bg-background p-4 shadow-xl">
            <div className="mb-4 flex items-center justify-between"><span className="text-sm font-medium">使用目录</span><button type="button" className="flex h-8 w-8 items-center justify-center rounded-md hover:bg-muted" onClick={() => setMenuOpen(false)}><X className="h-4 w-4" /></button></div>
            <HelpNav activeSection={activeSection} close={() => setMenuOpen(false)} />
          </aside>
        </div>
      )}

      <div className="mx-auto grid max-w-[1440px] grid-cols-1 lg:grid-cols-[230px_minmax(0,900px)] xl:grid-cols-[230px_minmax(0,900px)_210px] lg:gap-8 xl:gap-10 px-4 md:px-6">
        <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] overflow-y-auto border-r border-border py-8 pr-5 lg:block elegant-scrollbar">
          <p className="mb-3 px-2 text-[11px] font-semibold uppercase text-muted-foreground">使用目录</p>
          <HelpNav activeSection={activeSection} />
          <div className="mt-6 border-t border-border px-2 pt-5 text-xs leading-relaxed text-muted-foreground">
            <p>当前文档适用于 v0.3.33。</p>
            <a className="mt-2 inline-flex items-center gap-1 hover:text-foreground" href="/updates">更新记录 <History className="h-3 w-3" /></a>
            <a className="mt-2 inline-flex items-center gap-1 hover:text-foreground" href="https://github.com/Atingaii/ProofBridge" target="_blank" rel="noreferrer">GitHub <ExternalLink className="h-3 w-3" /></a>
          </div>
        </aside>

        <main className="min-w-0 pb-24 pt-10 md:pt-14">
          <section id="start" className="scroll-mt-24 border-b border-border pb-14">
            <div className="mb-5 flex items-center gap-2 text-xs font-medium text-emerald-600 dark:text-emerald-400"><span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />从浏览器安全连接你的私有 CLI</div>
            <h1 className="max-w-3xl text-3xl font-semibold leading-tight sm:text-4xl">ProofBridge 详细使用教程</h1>
            <p className="mt-5 max-w-3xl text-base leading-7 text-muted-foreground">本页从注册开始，带你连接私有机器上的 Codex / Claude Code，完成单 CLI 对话、协作编排、证据驱动辩论与形式化验证。所有示例都以“少占上下文、可验证、可继续”为目标。</p>
            <div className="mt-8 grid gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-3">
              <QuickFact icon={ShieldCheck} title="Hub 不接入私有机" text="Bridge 主动反向连接，模型凭据与工作区保留在你的机器。" />
              <QuickFact icon={UsersRound} title="两种编排模式" text="协作用于分工交付，辩论用于证伪高风险主张。" />
              <QuickFact icon={FileCheck2} title="形式化验证优先" text="Proof profile 固化证明义务、审计证据和最终检查。" />
            </div>
            <div className="mt-8 rounded-md border border-border bg-muted/35 p-5">
              <h2 className="text-sm font-semibold">5 分钟路径</h2>
              <ol className="mt-4 grid gap-3 sm:grid-cols-5">
                {['注册账户', '添加 CLI 端点', '运行连接命令', '新建会话', '发送首个任务'].map((item, index) => <li key={item} className="flex gap-2 text-xs leading-5 text-muted-foreground"><span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-primary text-[10px] text-primary-foreground">{index + 1}</span><span>{item}</span></li>)}
              </ol>
            </div>
          </section>

          <GuideSection id="account" eyebrow="01 · 账户" title="注册、Cloudflare 验证与登录" icon={KeyRound}>
            <p>打开 <code>https://sparkon.cn</code>。已有账户直接登录；新用户切换到“注册账户”，用户名使用 3–32 位 ASCII 字符，密码至少 10 位，并完成 Cloudflare Turnstile 人机验证。</p>
            <StepList items={[
              ['选择登录或注册', '注册模式会增加确认密码和安全验证；登录模式不会重复要求 Turnstile。'],
              ['检查密码', '点击输入框右侧眼睛可独立显示/隐藏密码；切换模式后会恢复隐藏。'],
              ['完成验证', '看到绿色验证状态后再提交。Token 过期时重新勾选，不要反复刷新页面。'],
              ['进入工作区', '注册成功会自动登录；账户只能看到自己拥有的端点、会话和编排运行。'],
            ]} />
            <div className="mt-7 grid gap-5 md:grid-cols-2">
              <Figure number={1} src="/help/auth-login.webp" alt="ProofBridge 登录页面，显示用户名、密码与眼睛按钮" caption="登录：输入已有账户，眼睛按钮只改变可见性，不改变密码值。" />
              <Figure number={2} src="/help/auth-register.webp" alt="ProofBridge 注册页面，显示两次密码与 Cloudflare Turnstile" caption="注册：两次密码一致并通过 Cloudflare 安全验证后创建账户。" />
            </div>
            <Callout icon={ShieldCheck} title="安全边界">Turnstile 的 Secret 只保存在 Hub 服务端；浏览器只收到公开 Site Key。不要把注册验证码、Cookie 或密码发给任何 CLI。</Callout>
          </GuideSection>

          <GuideSection id="endpoint" eyebrow="02 · 首次连接" title="把私有机器注册为 CLI 端点" icon={Network}>
            <p>浏览器只是控制面。真正读取代码、调用模型和执行命令的是你私有机器上的 Bridge。每个用户都应连接自己的端点。</p>
            <StepList items={[
              ['打开“设置”', '点击左下角设置，进入“添加 CLI 端点”。'],
              ['填写端点名称', '使用能识别机器和用途的名字，例如 macbook-proof 或 wsl2-work。'],
              ['选择权限策略', '“需要确认”会把危险命令交给浏览器审批；可信专机才使用“自动执行”。'],
              ['生成并运行命令', '复制“安装并连接”命令，在端点机器普通用户的终端运行。Token 24 小时有效且只用于注册。'],
              ['确认在线', '状态点变绿且能力矩阵出现 Codex / Claude 后即可使用。'],
            ]} />
            <Figure number={3} src="/help/settings-overview.webp" alt="设置窗口，包含账户、主题、语言和 CLI 端点状态" caption="设置总览：查看账户、外观、语言以及当前端点状态。" />
            <Figure number={4} src="/help/endpoint-enroll.webp" alt="添加 CLI 端点窗口，显示权限策略与安装连接命令" caption="端点注册：复制生成的命令到私有机器终端执行，命令中的 token 不要分享。" />
            <Callout icon={Terminal} title="端点前置条件">目标机器需要已安装并登录 Codex CLI 和/或 Claude Code。Bridge 不代替模型登录，也不会把模型密钥上传到 Hub。</Callout>
          </GuideSection>

          <GuideSection id="chat" eyebrow="03 · 单 CLI" title="创建会话并完成一次可继续的任务" icon={MessageSquare}>
            <p>单 CLI 对话适合明确、线性的工作：理解代码、修复一个缺陷、运行测试或解释日志。系统会复用当前会话 ID 和原生 thread，后续消息不会静默开启新上下文。</p>
            <Figure number={5} src="/help/workspace-overview.webp" alt="ProofBridge 工作区，左侧是会话，顶部是端点，中央是聊天区域" caption="工作区：左侧管理会话，顶部选择在线端点，底部输入任务和上传图片。" />
            <StepList items={[
              ['选端点', '顶部选择器决定任务在哪台私有机器、哪个工作目录运行。绿色状态表示在线。'],
              ['新建会话', '明确的新任务使用“新会话”；补充同一任务直接在当前会话继续。'],
              ['写可验证任务', '说明目标、边界、验收命令；避免只写“优化一下”或一次塞入多个无关目标。'],
              ['查看工具事件', '命令、输出、审批和最终回答按发生顺序显示；重要修改仍需检查 diff 与测试。'],
              ['需要时停止', '生成中可点击停止。停止后可在同一会话说明失败点并继续。'],
            ]} />
            <DemoBlock title="单 CLI demo：小而完整的修复" value={`修复上传文件名包含空格时的解析问题。\n\n边界：只改上传解析与相关测试，不重构存储层。\n验收：新增失败复现测试；运行 go test ./internal/hub -run Upload；总结修改文件和残余风险。`} copied={copied === 'chat'} onCopy={() => copyDemo('chat', `修复上传文件名包含空格时的解析问题。\n\n边界：只改上传解析与相关测试，不重构存储层。\n验收：新增失败复现测试；运行 go test ./internal/hub -run Upload；总结修改文件和残余风险。`)} />
            <div className="mt-7 grid gap-5 md:grid-cols-2">
              <Figure number={6} src="/help/chat-demo.webp" alt="包含用户任务、工具命令和助手结果的聊天会话" caption="任务执行：将目标、命令证据和最终结论放在同一连续会话。" />
              <Figure number={7} src="/help/chat-approval.webp" alt="浏览器中的命令审批卡片，提供允许和拒绝按钮" caption="浏览器审批：先读命令、目录与理由，再决定允许或拒绝。" />
            </div>
          </GuideSection>

          <GuideSection id="orchestration" eyebrow="04 · 多 CLI" title="编排界面的四个关键选择" icon={GitBranch}>
            <p>编排让两个长期存活的 CLI 会话围绕同一 run 交替工作。它不是把同一问题重复问两遍，而是让角色承担不同证明义务，并在可见时间线中留下证据。</p>
            <Figure number={8} src="/help/orchestration-overview.webp" alt="CLI 编排页面，包含时间线、协作模式、工作器、配置、轮次和任务输入" caption="编排总览：左侧保留 run，中央是轮次时间线，右侧配置模式、工作器、证明配置和轮次。" />
            <div className="mt-7 grid gap-3 sm:grid-cols-2">
              <Choice title="模式" value="协作 / 辩论" text="协作追求分工交付；辩论追求反例、证伪和更强替代方案。" />
              <Choice title="工作器" value="Claude + Codex / Codex + Codex" text="按端点能力选择。异构组合适合互补审查，同构组合适合双实现或交叉验证。" />
              <Choice title="配置" value="默认 / 形式化证明" text="证明任务选择 formal-proof；普通代码和设计任务保持默认。" />
              <Choice title="轮次" value="通常 2–4 轮" text="轮次越多并非越好。给每轮明确职责，最后一轮只做验收和结论。" />
            </div>
            <Callout icon={GitBranch} title="连续性原则">对同一目标的补充要求使用“继续”，系统调用当前 run 的 prompts 端点。只有真正的新目标才点“新运行”。这能保留上下文并避免重复扫描仓库。</Callout>
          </GuideSection>

          <GuideSection id="collaboration" eyebrow="05 · 编排模式 A" title="协作模式：分析、实现、验证逐步收敛" icon={UsersRound}>
            <p>协作模式适合能拆分为互补职责的交付任务。推荐把首轮给“建模/审查者”，中间轮给“实现者”，最后轮做独立验收；不要让每一轮都从头分析整个仓库。</p>
            <RoleFlow roles={[
              ['第 1 轮', '建模', '读取最小相关文件，列出不变量、风险和执行计划。'],
              ['第 2 轮', '实现', '依据上一轮结论改代码并运行聚焦测试。'],
              ['第 3 轮', '审查', '针对验收条件寻找遗漏、竞态和反例。'],
              ['最终检查', '验证', '只复跑关键命令，给出满足/不满足结论。'],
            ]} />
            <DemoBlock title="协作模式 demo" value={collaborationDemo} copied={copied === 'collaboration'} onCopy={() => copyDemo('collaboration', collaborationDemo)} />
            <Figure number={9} src="/help/collaboration-run.webp" alt="协作编排运行时间线，显示建模、实现、审查和最终检查轮次" caption="协作时间线：每轮职责不同，命令与结论折叠在对应轮次中，减少重复上下文。" />
            <GoodBad good={['交付目标清晰，可拆成分析/实现/验证', '需要一个 CLI 修改、另一个 CLI 独立审查', '需要保留命令、审批和验收证据']} bad={['只有一个简单问题，单 CLI 一轮即可完成', '两个角色没有不同职责，只会重复回答', '任务包含多个互不相关的仓库或目标']} />
          </GuideSection>

          <GuideSection id="debate" eyebrow="06 · 编排模式 B" title="辩论模式：先写可证伪主张，再寻找反例" icon={Scale}>
            <p>辩论模式不追求“观点数量”，而是测试最强版本的主张。支持方给出可检查的方案与证据；质疑方必须提出反例、隐藏假设或失败实验，并给出更安全的替代方案。</p>
            <RoleFlow roles={[
              ['主张', '提案者', '明确不变量、边界和可证伪结果，避免泛泛立场。'],
              ['反证', '质疑者', '构造具体反例或运行不利实验，不接受抽象反对。'],
              ['修正', '提案者', '吸收有效反例，收紧设计或最小化修改。'],
              ['裁决', '验证者', '按证据判断 satisfied / unsatisfied / blocked。'],
            ]} />
            <DemoBlock title="辩论模式 demo" value={debateDemo} copied={copied === 'debate'} onCopy={() => copyDemo('debate', debateDemo)} />
            <Figure number={10} src="/help/debate-run.webp" alt="辩论编排运行时间线，显示主张、反证、修正和裁决" caption="辩论时间线：把争议压缩成可证伪主张、反例和最终证据，而不是重复长篇观点。" />
            <GoodBad good={['安全边界、并发语义或数据隔离存在高风险假设', '需要在实现前比较两种架构并主动找反例', '形式化规格可能被弱化，需要独立质疑']} bad={['需求已经确定，只需按步骤实现', '没有可验证命题或数据，只有偏好讨论', '为了“更聪明”无条件增加轮次']} />
          </GuideSection>

          <GuideSection id="formal-proof" eyebrow="07 · 专项配置" title="形式化验证：让证明义务和证据成为主线" icon={FileCheck2}>
            <p>选择“形式化证明”配置后，Bridge 会保留实际项目和一份轻量证明记录，集中记录目标、未解决义务、关键命令证据与阻塞点。它适合 Coq/Rocq、Isabelle 等需要真实编译器验证的任务，不把语言模型的文字判断当作证明完成。</p>
            <Figure number={11} src="/help/formal-proof-setup.webp" alt="形式化证明编排配置，显示 proof profile、工作目录、文件和轮次" caption="证明配置：选择 formal-proof、准确工作目录和最少附件，再给出具体构建命令。" />
            <StepList items={[
              ['限定系统与目标', '写明 Coq/Rocq 或 Isabelle、目标 theorem/session，以及不可使用的 shortcut。'],
              ['上传最少材料', '只上传目标源文件、契约、ROOT/_CoqProject 和必要日志；不要上传整个 build 输出。'],
              ['声明证明义务', '列出不得弱化的前置/后置条件、允许修改范围和必须保持的不变量。'],
              ['给出真实命令', '例如 coqc、rocq compile、isabelle build；版本探测不等于证明通过。'],
              ['要求负向审计', '搜索 sorry、admitted、axiom、oops 以及被偷偷改弱的命题。'],
              ['阅读最终结论', '结论应包含 outcome、成功命令、未满足义务、证据引用和残余风险。'],
            ]} />
            <DemoBlock title="Coq/Rocq 形式化证明 demo" value={formalProofDemo} copied={copied === 'proof'} onCopy={() => copyDemo('proof', formalProofDemo)} />
            <Figure number={12} src="/help/formal-proof-result.webp" alt="形式化证明编排结果，显示构建命令、证明轮次和 satisfied 结论" caption="证明结果：只有真实 proof assistant 命令成功且负向审计通过，才接受 satisfied。" />
            <Callout icon={FileCheck2} title="不要接受这些替代品">“代码看起来正确”、只运行 <code>--version</code>、添加 <code>Admitted</code>/<code>sorry</code>、更改 theorem 使其更弱、或者只给伪代码，都不等于形式化验证完成。</Callout>
          </GuideSection>

          <GuideSection id="context" eyebrow="08 · 性能" title="用更少上下文获得更稳定的编排" icon={Zap}>
            <p>上下文成本主要来自重复扫描、无边界附件、过多轮次和新建 run。下面的做法同时减少 token、延迟和无关输出。</p>
            <div className="mt-6 overflow-hidden rounded-md border border-border">
              <table className="w-full text-left text-sm">
                <thead className="bg-muted/50 text-xs"><tr><th className="px-4 py-3 font-medium">做法</th><th className="px-4 py-3 font-medium">为什么有效</th><th className="hidden px-4 py-3 font-medium sm:table-cell">建议值</th></tr></thead>
                <tbody className="divide-y divide-border text-muted-foreground">
                  {[
                    ['任务先写边界和验收', '避免两个 CLI 各自猜测目标并扫描无关目录', '4–12 行'],
                    ['协作默认少轮次', '角色明确时无需反复总结相同背景', '2–4 轮'],
                    ['续问留在同一 run', '复用原生 Codex thread / Claude session', '使用“继续”'],
                    ['附件只给相关文件', '大日志和构建目录会淹没证明义务', '通常 2–8 个文件'],
                    ['打开每轮后压缩', '长运行可在原生 CLI 轮次间压缩历史', '长任务再启用'],
                    ['最终轮只做验证', '不重新设计，只运行验收命令并输出结论', '1 个 verifier'],
                  ].map(([a, b, c]) => <tr key={a}><td className="px-4 py-3 font-medium text-foreground">{a}</td><td className="px-4 py-3 leading-6">{b}</td><td className="hidden px-4 py-3 font-mono text-xs sm:table-cell">{c}</td></tr>)}
                </tbody>
              </table>
            </div>
            <Figure number={13} src="/help/context-controls.webp" alt="编排侧栏中的轮次和原生上下文压缩控制" caption="上下文控制：少轮次优先；只有长运行才选择“每轮后”原生压缩。" />
            <Callout icon={Sparkles} title="一个实用判断">如果第二个 CLI 无法得到不同职责，先用单 CLI。只有当独立审查、反证或 proof assistant 证据能明显降低错误风险时，才启动编排。</Callout>
          </GuideSection>

          <GuideSection id="sharing" eyebrow="09 · 结果管理" title="会话、继续运行与只读分享" icon={Cloud}>
            <p>左侧会话和 run 列表用于恢复上下文。分享按钮生成只读快照链接，适合让同事检查结果；分享不会授权对方访问你的端点或继续执行。</p>
            <StepList items={[
              ['重命名', '使用能表达目标的标题，后续搜索比“新会话”更可靠。'],
              ['继续', '同一任务直接发送补充；编排使用当前 run 的“继续”。'],
              ['分享', '点击分享后复制只读链接，先自行打开检查是否包含敏感内容。'],
              ['撤销', '不再需要时撤销分享；删除会话会移除 Hub 记录，但不会改动工作区文件。'],
            ]} />
            <div className="mt-7 grid gap-5 md:grid-cols-2">
              <Figure number={14} src="/help/session-actions.webp" alt="会话列表中的分享、重命名和删除操作" caption="会话管理：分享、重命名和删除都作用于当前用户拥有的记录。" />
              <Figure number={15} src="/help/public-share.webp" alt="公开只读对话快照页面" caption="只读分享：访问者只能查看已保存内容，不能调用你的 CLI。" />
            </div>
          </GuideSection>

          <GuideSection id="settings" eyebrow="10 · 设置" title="主题、语言、端点维修与移动端" icon={Settings}>
            <p>设置页可以切换主题和语言、查看端点能力矩阵、生成修复命令或删除旧端点。移动端保留主要工作流，侧栏通过左上角菜单打开。</p>
            <div className="mt-7 grid gap-5 md:grid-cols-[1.45fr_0.75fr]">
              <Figure number={16} src="/help/endpoint-capabilities.webp" alt="CLI 端点展开后的能力矩阵和修复连接按钮" caption="能力矩阵：确认 chat、orchestration 和 browser approval 均符合所选策略。" />
              <Figure number={17} src="/help/mobile-workspace.webp" alt="手机尺寸的 ProofBridge 工作区" caption="移动端：菜单、端点状态、消息和输入区保持可用。" />
            </div>
            <Callout icon={Laptop} title="端点离线时">先确认本机 Bridge 进程和网络；若机器 ID 已存在但 token 过期，在设置中展开该端点并生成“修复连接”命令，不要重复创建多个同名端点。</Callout>
          </GuideSection>

          <GuideSection id="troubleshooting" eyebrow="11 · 排错" title="按现象快速定位问题" icon={CircleHelp}>
            <div className="mt-5 divide-y divide-border rounded-md border border-border">
              <Trouble title="注册按钮不可用或验证反复出现" answer="确认两次密码一致且至少 10 位；关闭会拦截 challenges.cloudflare.com 的脚本扩展；等待 Turnstile 显示完成后再提交。" />
              <Trouble title="端点一直离线" answer="在端点机器检查 Bridge 进程、系统时间和出站 HTTPS/WSS；token 过期时从设置生成修复命令。" />
              <Trouble title="编排开始按钮禁用" answer="所选端点必须在线，并同时上报所选两个工作器的编排能力；“需要确认”模式还要求 browser approval 可用。" />
              <Trouble title="形式化证明显示完成但没有可信证据" answer="检查最终结论是否列出真实构建命令和退出结果；没有 coqc/rocq/isabelle build 成功记录时，继续当前 run 要求补验证。" />
              <Trouble title="上下文越来越长、响应变慢" answer="停止增加重复轮次；在同一 run 里明确只读相关文件；长任务启用每轮后压缩；将新目标拆成新 run。" />
              <Trouble title="线上页面仍是旧版本" answer="硬刷新一次并等待 Service Worker 更新；查看 /health 的 version；若主包 hash 未变化，检查 Hub 是否已重启到新二进制。" />
            </div>
            <div className="mt-8 flex flex-col items-start justify-between gap-4 rounded-md border border-border bg-muted/30 p-5 sm:flex-row sm:items-center">
              <div><h3 className="text-sm font-semibold">准备开始？</h3><p className="mt-1 text-xs leading-5 text-muted-foreground">先连接一个端点，再从小而可验证的任务开始。</p></div>
              <a href="/" className="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90">打开 ProofBridge <ArrowRight className="h-4 w-4" /></a>
            </div>
          </GuideSection>
        </main>

        <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] py-8 xl:block">
          <div className="border-l border-border pl-4">
            <p className="text-[11px] font-semibold uppercase text-muted-foreground">本页重点</p>
            <div className="mt-3 space-y-3 text-xs leading-5 text-muted-foreground">
              <p className="flex gap-2"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />至少 17 张真实界面截图</p>
              <p className="flex gap-2"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />4 个可复制 demo</p>
              <p className="flex gap-2"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />协作与辩论分开讲解</p>
              <p className="flex gap-2"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />形式化验证验收清单</p>
            </div>
          </div>
        </aside>
      </div>
    </div>
  );
}

function HelpNav({ activeSection, close }: { activeSection: string; close?: () => void }) {
  return <nav className="space-y-0.5">{navItems.map(([id, label]) => <a key={id} href={`#${id}`} onClick={close} className={`flex items-center justify-between rounded-md px-2 py-1.5 text-xs transition-colors ${activeSection === id ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}`}><span>{label}</span>{activeSection === id && <ChevronRight className="h-3 w-3" />}</a>)}</nav>;
}

function GuideSection({ id, eyebrow, title, icon: Icon, children }: { id: string; eyebrow: string; title: string; icon: React.ComponentType<{ className?: string }>; children: React.ReactNode }) {
  return <section id={id} className="scroll-mt-24 border-b border-border py-14 last:border-b-0"><div className="mb-4 flex items-center gap-2 text-xs font-medium text-muted-foreground"><Icon className="h-4 w-4" />{eyebrow}</div><h2 className="text-2xl font-semibold leading-tight">{title}</h2><div className="mt-5 space-y-5 text-sm leading-7 text-muted-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1.5 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-xs [&_code]:text-foreground">{children}</div></section>;
}

function Figure({ src, alt, caption, number }: FigureProps) {
  return <figure className="overflow-hidden rounded-md border border-border bg-card"><div className="aspect-[16/10] overflow-hidden bg-muted/30 p-1"><img src={src} alt={alt} loading="lazy" className="h-full w-full object-contain" /></div><figcaption className="flex gap-3 border-t border-border px-3 py-3 text-xs leading-5 text-muted-foreground"><span className="font-mono text-[10px] text-foreground">{String(number).padStart(2, '0')}</span><span>{caption}</span></figcaption></figure>;
}

function QuickFact({ icon: Icon, title, text }: { icon: React.ComponentType<{ className?: string }>; title: string; text: string }) {
  return <div className="bg-background p-4"><Icon className="h-4 w-4 text-primary" /><h3 className="mt-3 text-sm font-medium">{title}</h3><p className="mt-1 text-xs leading-5 text-muted-foreground">{text}</p></div>;
}

function StepList({ items }: { items: Array<[string, string]> }) {
  return <ol className="mt-6 space-y-4">{items.map(([title, text], index) => <li key={title} className="grid grid-cols-[28px_1fr] gap-3"><span className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-muted/40 font-mono text-[11px] text-foreground">{String(index + 1).padStart(2, '0')}</span><div><h3 className="text-sm font-medium text-foreground">{title}</h3><p className="mt-0.5 text-sm leading-6 text-muted-foreground">{text}</p></div></li>)}</ol>;
}

function Callout({ icon: Icon, title, children }: { icon: React.ComponentType<{ className?: string }>; title: string; children: React.ReactNode }) {
  return <div className="mt-6 flex gap-3 rounded-md border border-border bg-muted/35 p-4"><Icon className="mt-0.5 h-4 w-4 shrink-0 text-primary" /><div><h3 className="text-xs font-semibold text-foreground">{title}</h3><div className="mt-1 text-xs leading-6 text-muted-foreground">{children}</div></div></div>;
}

function DemoBlock({ title, value, copied, onCopy }: { title: string; value: string; copied: boolean; onCopy: () => void }) {
  return <div className="mt-7 overflow-hidden rounded-md border border-border bg-[#111827] text-slate-100"><div className="flex items-center justify-between border-b border-white/10 px-4 py-2.5"><span className="flex items-center gap-2 text-xs font-medium"><Code2 className="h-3.5 w-3.5" />{title}</span><button type="button" onClick={onCopy} className="inline-flex h-7 items-center gap-1.5 rounded px-2 text-[11px] text-slate-300 hover:bg-white/10 hover:text-white">{copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}{copied ? '已复制' : '复制'}</button></div><pre className="overflow-x-auto whitespace-pre-wrap p-4 font-mono text-xs leading-6 text-slate-300">{value}</pre></div>;
}

function Choice({ title, value, text }: { title: string; value: string; text: string }) {
  return <div className="rounded-md border border-border p-4"><p className="text-[11px] font-semibold uppercase text-muted-foreground">{title}</p><h3 className="mt-1 text-sm font-medium text-foreground">{value}</h3><p className="mt-2 text-xs leading-5 text-muted-foreground">{text}</p></div>;
}

function RoleFlow({ roles }: { roles: Array<[string, string, string]> }) {
  return <div className="mt-6 grid gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-2">{roles.map(([turn, role, text]) => <div key={turn} className="bg-background p-4"><p className="font-mono text-[10px] text-muted-foreground">{turn}</p><h3 className="mt-1 text-sm font-medium text-foreground">{role}</h3><p className="mt-2 text-xs leading-5 text-muted-foreground">{text}</p></div>)}</div>;
}

function GoodBad({ good, bad }: { good: string[]; bad: string[] }) {
  return <div className="mt-6 grid gap-4 sm:grid-cols-2"><div className="rounded-md border border-emerald-500/25 bg-emerald-500/5 p-4"><h3 className="text-xs font-semibold text-emerald-700 dark:text-emerald-400">适合使用</h3><ul className="mt-3 space-y-2">{good.map((item) => <li key={item} className="flex gap-2 text-xs leading-5"><CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />{item}</li>)}</ul></div><div className="rounded-md border border-border bg-muted/25 p-4"><h3 className="text-xs font-semibold text-foreground">先不要使用</h3><ul className="mt-3 space-y-2">{bad.map((item) => <li key={item} className="flex gap-2 text-xs leading-5"><X className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />{item}</li>)}</ul></div></div>;
}

function Trouble({ title, answer }: { title: string; answer: string }) {
  return <details className="group p-4"><summary className="flex cursor-pointer list-none items-center justify-between gap-4 text-sm font-medium text-foreground"><span>{title}</span><ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" /></summary><p className="mt-3 pr-8 text-xs leading-6 text-muted-foreground">{answer}</p></details>;
}
