# Codex Bridge 中文接入指南

[![CI](https://github.com/Atingaii/Codex-bridge/actions/workflows/ci.yml/badge.svg)](https://github.com/Atingaii/Codex-bridge/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-Linux-555)](docs/deployment.md)

[English](README.md) · [部署指南](docs/deployment.md) · [架构](docs/architecture.md)

Codex Bridge 让浏览器远程访问私有机器上的 Codex 和 Claude Code CLI：既可与单个 CLI 一对一聊天，也可通过编排在一个长驻的原生 Codex 会话与一个长驻的原生 Claude Code 会话之间逐轮中转（Bridge 只回传输出与轮次上下文，不注入额外的校验/补救轮次，因此你可以在工作目录里 `resume` 这些原生会话）。Hub 是公网入口和 Web UI，Bridge 从私有机器反向连接 Hub，所以 Hub 不需要保存 `OPENAI_API_KEY`，也不需要直连你的工作目录。

## Bridge / ACP / resume 简明版

用户侧执行网页里的 `curl .../install.sh | sh` 后，装到本机的核心就是
`~/.local/bin/codex-bridge`。兼容软链 `acp-bridge` 和
`agent-up.sh` / `agent-down.sh` 只负责旧命名与启停；Codex、Claude Code、
Gemini、CodeBuddy 等 agent CLI 仍然要用户自己安装和登录。Bridge 本身是通信与进程管理器：
它从私有机器主动连到 Hub 的反向 WebSocket，然后按会话在工作目录里 `exec` 本机 CLI，
所以 Hub 不需要访问你的工作目录，也不需要你的模型密钥。

ACP 是 Bridge 和本地 CLI/适配器之间的程序化协议：JSON-RPC over
stdin/stdout，不是人工 REPL。`bridge.runner: acp` 时，Bridge 会启动对应 ACP 适配器
（例如 Claude 用 `npx @zed-industries/claude-code-acp`，Codex 用 `codex-acp`），
执行 `initialize -> session/new` 或 `session/load`，之后每轮发
`session/prompt`。Hub 的 `open_session` 对应打开/恢复 ACP 会话，`prompt`
对应一轮提示，ACP 的 `session/update` 再被转成网页上的流式输出。

核心绑定关系是：

```text
浏览器会话 sid <-> Bridge sessionRouter <-> 一个常驻 CLI/ACP 子进程 <-> 一个 ACP session
```

同一个 `sid` 后续每轮都会查到同一个 router，复用同一个进程和同一个
ACP session，所以上下文在这个本地 CLI 会话里持续累积。只有前端显式新建会话、
让 Hub 使用新的 `sid` 发 `open_session`，才会新开本地进程并得到一段新的上下文。
如果把 `/new` 当普通消息发给网页，Bridge 不解析斜杠命令；它会原样转给同一个
ACP session，是否清空由 CLI 自己决定。真正决定“是不是同一个本地会话”的开关是 `sid`。

`/resume` 能看到真实对话不是因为 Hub 回放了网页消息，而是因为 CLI 自己在本机落盘。
Bridge 在 `cwd=/你的项目` 下驱动 ACP 会话，CLI 执行时会按自己的规则把对话写到本地
session 存储，并按项目目录归档。事后你在同一台机器执行
`cd /你的项目 && claude` 后用 `/resume`，或执行 `codex resume ...`，CLI 扫到的是它自己
写下的本地记录。也就是说：ACP 负责实时驱动和流式回传，CLI 本地 session 存储负责事后
原生 resume，两条链路是解耦的。

浏览器 WebSocket 断开时，Hub 不会立刻清掉 Bridge 侧会话，而是把该 `sid` 放进
`leaseIdleLeased` 租约状态并启动 TTL 计时器（默认由 `HUB_BROWSER_LEASE_TTL` /
`hub.browser_lease_ttl` 控制）。TTL 内重新打开同一个会话，Hub 走 `tryReattach`
取消计时器并再次发送同一个 `open_session{sid}`；Bridge 侧按 `sid` 命中已有
sessionRouter，继续复用原进程和 ACP session，不需要 `/resume`。TTL 过期后 Hub 才发送
`close_session`，Bridge 才真正释放本地会话。

整体链路可以压缩成：

```text
安装 codex-bridge
  -> Bridge 反向拨 WebSocket 连 Hub
前端打开会话 sid
  -> Bridge 在项目 cwd 下启动一个本地 CLI/ACP 会话，形成 1:1 绑定
前端每轮发消息
  -> 复用同一进程、同一 ACP session，上下文持续累积，并流式回显到网页
  -> CLI 同时把对话写到本机 session 存储
关闭浏览器再回来
  -> TTL 内同 sid 直接 reattach，不需要 /resume
事后在本机 cd 到项目目录运行 CLI
  -> 用 CLI 自己的 resume 能看到 bridge 跑过的真实对话
```

## 普通用户接入 SparkAPI Hub

目标 Hub：`https://sparkapi.tech`

1. 打开 `https://sparkapi.tech`，使用已经开通的账号登录。网页不开放新用户自助注册。
2. 进入设置，点击“添加 CLI 端”，复制页面生成的“安装并连接”命令。
3. 进入要接入的工作目录，用同一个运行 Codex CLI / Claude Code 的系统用户粘贴执行这一行命令。

WSL2/Linux 终端一般就是：

```bash
# 执行网页生成的单行“安装并连接”命令
```

页面生成的一键命令会先安装二进制，再优先安装并启动 `systemd --user` 服务，并
best-effort 开启 linger；在 user systemd 和 linger 可用时，机器重启后可自动恢复。
如果当前环境没有 user systemd，会退回 `nohup` 后台启动，但 `nohup` 不能跨机器重启。
日志写到 `~/.codex-bridge/logs/<当前目录hash>.log`。命令会把当前 shell 的
`HOME`、`PATH`、`CODEX_HOME`、Claude 配置目录、常见模型凭据和常见代理环境变量保存给
后台服务，避免 WSL/Linux 里前台能联网或能 resume、`systemd --user` 后台却使用了另一个
CLI/配置目录或直连超时。
生成的 user service 会设置 `OOMPolicy=continue`，避免 Isabelle/Coq 这类重型子进程被
OOM kill 时把 Bridge 父进程一起重启，导致网页端运行链路中断。
`connect` 默认连接
`https://sparkapi.tech`，默认使用当前目录作为工作目录，默认 runner 是 `codex`。
如果要前台调试，可以手动执行：

```bash
~/.local/bin/codex-bridge connect '<TOKEN>' --cwd "$PWD" --name wsl2-main --runner codex
```

token 由网页生成，默认 24 小时内有效。一个 token 绑定一个 CLI 端；不同用户、不同终端会在页面里显示为不同的 CLI 端，并可在顶部选择切换。

浏览器里产生的 Codex 原生会话会写在运行上述命令的同一个系统用户下。要在本机查看：

```bash
cd /home/zy/os
codex resume --include-non-interactive
```

如果要忽略 cwd 过滤再使用 `codex resume --all --include-non-interactive`。正常接入流程不使用 sudo/root 命令，因为那会把原生会话写到另一个用户的 HOME 下。

## 普通用户前置条件

在 WSL2/Linux CLI 端需要先准备好 Codex CLI 和 Claude Code，并在该终端完成本地认证。Hub 不会接触你的 `OPENAI_API_KEY`。请在同一个已完成认证的 shell 里运行网页生成的安装/修复命令；后台服务会把常见模型凭据环境变量（如 `OPENAI_API_KEY`、`CLAUDE_CODE_OAUTH_TOKEN`）保存到本机 0600 权限的 service env 文件中。

最短接入路径不需要 clone 仓库，也不需要本地编译：

```bash
# 执行网页生成的单行“安装并连接”命令
```

如果只是验证连接链路，可以用 echo runner：

```bash
~/.local/bin/codex-bridge connect '<TOKEN>' --runner echo
```

## 界面使用

- 登录后在设置里可以看到自己已接入的 CLI 端。
- 在线/离线状态会显示在 CLI 端列表里。
- 主对话和编排页顶部都有 CLI 端选择器，可以在多个 WSL2/服务器终端之间切换。
- “需要确认”权限策略会把 Codex 聊天、Codex 编排和 Claude Code 编排审批都展示到浏览器；“自动执行”权限策略会把 Claude Code 映射到原生 bypass 权限模式，并把 root 场景需要的 `IS_SANDBOX` 只注入 Bridge 管理的 Claude 子进程，不要求用户修改全局 Claude 配置。编排页会显示当前 CLI 端的能力矩阵。Hub 编排会在同一个选中的 Bridge 连接上轮流调用 Claude Code 和 Codex CLI，并把每轮摘要带给下一轮。
- 编排页有 `默认` / `形式化证明` 配置选择。形式化证明提示是显式选择后才启用，并随 run 持久化；默认配置不会因为关键词自动注入证明提示。编排事件使用结构化字段区分 CLI、Bridge、用户来源，并用单独的最终结论事件展示收尾结果。
- 每个 CLI 端可以展开“详情”并生成“修复连接”命令；旧版启动命令接入的端点可用该命令更新 Bridge、保留原 machine id 并重连同一个端点。
- 删除 CLI 端会让 Hub 通知在线 Bridge 停止对应本地服务并退出，同时让已消费 token
  失效；离线端点仍会从页面隐藏并撤销旧 token。
- 非管理员只能看到自己接入的 CLI 端；管理员可以看到所有 CLI 端。
- Orchestrate 页面只有点击“新运行”才会开启新的编排会话；在当前任务框继续输入会沿用当前 run，并把历史事件压缩成上下文继续运行。

## 自建 Hub

> 完整的多种部署方式（源码 / make / Docker / systemd + Caddy）、生产配置、
> 验证与排查见 **[docs/deployment.md](docs/deployment.md)**。

先获取代码：

```bash
git clone https://github.com/Atingaii/Codex-bridge.git
cd Codex-bridge
```

构建单个 Go 二进制（前置：Go 1.25+、Node 20+）。Web UI 已编译进二进制，从源码构建时
需要先构建前端，因此推荐用 `make build-all`：

```bash
make test            # 等价于 go test ./...
make build-all       # 先构建前端，再编译 Go 二进制 -> bin/codex-bridge
```

也可以用 Docker 跑 Hub：

```bash
docker build -t codex-bridge:local .          # 或 make docker
docker run --rm -p 8088:8088 \
  -v "$PWD/configs:/opt/codex-bridge/configs:ro" \
  -v codex-bridge-data:/opt/codex-bridge/data \
  codex-bridge:local hub
```

初始化生产配置：

```bash
sudo mkdir -p /opt/codex-bridge/configs /opt/codex-bridge/data
sudo cp bin/codex-bridge /usr/local/bin/codex-bridge
sudo cp configs/dev.yaml.example /opt/codex-bridge/configs/prod.yaml
```

关键配置：

```yaml
gateway:
  host: 127.0.0.1
  port: 8088
hub:
  db_path: /opt/codex-bridge/data/codex-bridge.db
  cookie_secure: true
  bridge_download_url: https://github.com/Atingaii/Codex-bridge/releases/latest/download/codex-bridge-linux-amd64
auth:
  jwt_secret: replace-with-32-byte-random-secret
  bootstrap_username: admin
```

启动 Hub 前创建管理员或其他已批准用户：

```bash
APP_ENV=prod CODEX_BRIDGE_CONFIG_DIR=/opt/codex-bridge/configs codex-bridge user --username admin --password 'change-me'
APP_ENV=prod CODEX_BRIDGE_CONFIG_DIR=/opt/codex-bridge/configs codex-bridge user --username alice --password 'change-me-too'
APP_ENV=prod CODEX_BRIDGE_CONFIG_DIR=/opt/codex-bridge/configs codex-bridge hub
```

建议把 Hub 放在 Caddy/Nginx 后面做 HTTPS，反代到 `127.0.0.1:8088`。生产环境必须替换 `change-me` 和 `auth.jwt_secret`。

## 给其他用户的接入配置

自建 Hub 默认会让 `/install.sh` 从当前 Hub 的
`/downloads/codex-bridge-linux-amd64` 下载正在运行的二进制。也可以配置
release 二进制下载地址：

```yaml
hub:
  bridge_download_url: https://github.com/Atingaii/Codex-bridge/releases/latest/download/codex-bridge-linux-amd64
```

也可以用环境变量覆盖：

```bash
HUB_BRIDGE_DOWNLOAD_URL='https://your-release-url/codex-bridge-linux-amd64'
```

先用 `codex-bridge user --username <name> --password <password>` 创建用户。用户登录后在网页里自己生成 CLI token，然后复制执行页面生成的单行“安装并连接”命令。该命令会把日志写到 `~/.codex-bridge/logs/`，保留当前 shell 的 `PATH`、解析到的 Codex/Claude CLI 路径和常见代理变量；如果当前 shell 里找不到任一 CLI，会在注册不可用端点前直接失败。只有 Bridge 日志出现 `[bridge] connected` 后才会提示已连接；否则会打印最近日志方便定位。

如果某个已添加 CLI 端没有上报能力矩阵，打开设置里的 `Agent 与运行时`，展开该端并点击 `生成修复命令`。修复命令会下载最新 Bridge，并用该端原有的 machine id、端名称和已知工作目录重启连接，避免误注册成新端点。

添加 CLI 端时有两种权限策略：

- `需要确认`：Codex 聊天和 Codex 编排使用 `codex app-server` runner，命令/文件审批会回传到网页端确认；Claude Code 编排通过权限提示 MCP hook 把审批回传到网页端。
- `无需授权`：保持当前可信机器模式，使用 `danger-full-access` 和 `approval_policy=never`，不会弹权限确认。

手动拆分时等价于：

```bash
curl -fsSL https://your-domain.example/install.sh | sh
~/.local/bin/codex-bridge connect --hub https://your-domain.example '<TOKEN>'
```

## 旧式手动接入

管理员仍可手动生成未绑定 token，用于本地开发或临时测试：

```bash
TOKEN=$(codex-bridge enroll --ttl 24h | tail -n1)
BRIDGE_HUB_URL='https://your-domain.example' BRIDGE_TOKEN="$TOKEN" BRIDGE_RUNNER=codex BRIDGE_CWD="$PWD" codex-bridge bridge
```

下载的 release 二进制也可以使用旧命令：

```bash
BRIDGE_HUB_URL='https://your-domain.example' BRIDGE_TOKEN="$TOKEN" codex-bridge bridge
```

## 本地开发

```bash
cp configs/dev.yaml.example configs/dev.yaml
go run . user --username admin --password 'change-me'
TOKEN=$(go run . enroll | tail -n1)
BRIDGE_TOKEN="$TOKEN" go run . bridge        # 或 make run-bridge
```

另开一个终端：

```bash
go run . hub                                  # 或 make run-hub
```

浏览器打开 `http://127.0.0.1:8088`。

## 常用环境变量

```bash
APP_HOST=127.0.0.1
APP_PORT=8088
HUB_DB_PATH=/opt/codex-bridge/data/codex-bridge.db
HUB_BRIDGE_DOWNLOAD_URL='https://your-release-url/codex-bridge-linux-amd64'
HUB_BROWSER_LEASE_TTL=5m
JWT_SECRET='32-byte-random-secret'
BRIDGE_HUB_URL='https://sparkapi.tech'
BRIDGE_TOKEN='enroll-token'
BRIDGE_RUNNER=codex
BRIDGE_CWD='/path/to/workspace'
BRIDGE_MODEL='gpt-5.1-codex-max'
BRIDGE_SANDBOX=workspace-write
BRIDGE_APPROVAL_POLICY=never
BRIDGE_LONG_COMMAND_OBSERVER_ENABLED=false
BRIDGE_LONG_COMMAND_OBSERVER_AFTER=2m
```

长命令观察器的完整 YAML 配置在 `bridge.long_command_observer`，详细说明见
`docs/dev-workflow.md`。

## 排查

```bash
curl https://sparkapi.tech/health
~/.local/bin/codex-bridge connect '<TOKEN>' --runner echo
go test ./...                                 # 或 make test
```

常见问题：

- Bridge 不在线：确认 token 没过期、没有被其他机器消费，并且终端能访问 Hub。
- Codex 无法执行：确认 WSL2/服务器里已经安装并登录 Codex CLI。
- 工作目录不对：用 `--cwd /path/to/workspace` 指定。
- HTTPS Cookie 异常：生产环境设置 `hub.cookie_secure: true`，本地 HTTP 设置为 `false`。
