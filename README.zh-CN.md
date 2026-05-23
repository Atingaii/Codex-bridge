# Codex Bridge 中文接入指南

Codex Bridge 让浏览器远程访问私有机器上的 Codex CLI。Hub 是公网入口和 Web UI，Bridge 从私有机器反向连接 Hub，所以 Hub 不需要保存 `OPENAI_API_KEY`，也不需要直连你的工作目录。

## 普通用户接入 SparkAPI Hub

目标 Hub：`https://sparkapi.tech`

1. 打开 `https://sparkapi.tech`，登录或注册账号。
2. 进入设置，点击“添加 CLI 端”，复制页面生成的两行命令。
3. 在要接入的终端里执行这两行命令。

WSL2/Linux 终端一般就是：

```bash
curl -fsSL https://sparkapi.tech/install.sh | sh
CB_CWD="${PWD:-.}"; CB_DIR="$(basename "$CB_CWD")"; CB_HASH="$(printf '%s' "$CB_CWD" | cksum | awk '{print $1}')"; CB_LOG_DIR="$HOME/.codex-bridge/logs"; CB_LOG="$CB_LOG_DIR/${CB_HASH}.log"; mkdir -p "$HOME/.codex-bridge/machines" "$CB_LOG_DIR"; nohup ~/.local/bin/codex-bridge connect --cwd "$CB_CWD" --name "${HOSTNAME:-cli}-${CB_DIR}-${CB_HASH}" --machine-id-file "$HOME/.codex-bridge/machines/${CB_HASH}" '<TOKEN>' > "$CB_LOG" 2>&1 & CB_PID=$!; echo "codex-bridge started in background: pid=$CB_PID log=$CB_LOG"
```

页面生成的连接命令默认用 `nohup` 后台启动 Bridge，并把日志写到
`~/.codex-bridge/logs/<当前目录hash>.log`。`connect` 默认连接
`https://sparkapi.tech`，默认使用当前目录作为工作目录，默认 runner 是
`codex`。如果要前台调试，可以手动执行：

```bash
~/.local/bin/codex-bridge connect '<TOKEN>' --cwd "$PWD" --name wsl2-main --runner codex
```

token 由网页生成，默认 24 小时内有效。一个 token 绑定一个 CLI 端；不同用户、不同终端会在页面里显示为不同的 CLI 端，并可在顶部选择切换。

## 普通用户前置条件

在 WSL2/Linux CLI 端需要先准备好 Codex CLI，并在该终端完成 OpenAI/Codex 的本地认证。Hub 不会接触你的 `OPENAI_API_KEY`。

最短接入路径不需要 clone 仓库，也不需要本地编译：

```bash
curl -fsSL https://sparkapi.tech/install.sh | sh
CB_CWD="${PWD:-.}"; CB_DIR="$(basename "$CB_CWD")"; CB_HASH="$(printf '%s' "$CB_CWD" | cksum | awk '{print $1}')"; CB_LOG_DIR="$HOME/.codex-bridge/logs"; CB_LOG="$CB_LOG_DIR/${CB_HASH}.log"; mkdir -p "$HOME/.codex-bridge/machines" "$CB_LOG_DIR"; nohup ~/.local/bin/codex-bridge connect --cwd "$CB_CWD" --name "${HOSTNAME:-cli}-${CB_DIR}-${CB_HASH}" --machine-id-file "$HOME/.codex-bridge/machines/${CB_HASH}" '<TOKEN>' > "$CB_LOG" 2>&1 & CB_PID=$!; echo "codex-bridge started in background: pid=$CB_PID log=$CB_LOG"
```

如果只是验证连接链路，可以用 echo runner：

```bash
~/.local/bin/codex-bridge connect '<TOKEN>' --runner echo
```

## 界面使用

- 登录后在设置里可以看到自己已接入的 CLI 端。
- 在线/离线状态会显示在 CLI 端列表里。
- 主对话和编排页顶部都有 CLI 端选择器，可以在多个 WSL2/服务器终端之间切换。
- 非管理员只能看到自己接入的 CLI 端；管理员可以看到所有 CLI 端。
- Orchestrate 页面只有点击“新运行”才会开启新的编排会话；在当前任务框继续输入会沿用当前 run，并把历史事件压缩成上下文继续运行。

## 自建 Hub

构建单个 Go 二进制：

```bash
/usr/local/go/bin/go test ./...
CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .
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
  allow_registration: true
```

启动 Hub 前创建管理员：

```bash
APP_ENV=prod CODEX_BRIDGE_CONFIG_DIR=/opt/codex-bridge/configs codex-bridge user --username admin --password 'change-me'
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
AUTH_ALLOW_REGISTRATION=true
```

用户在网页里自己注册、自己生成 CLI token，然后执行：

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
/usr/local/go/bin/go run . user --username admin --password 'change-me'
TOKEN=$(/usr/local/go/bin/go run . enroll | tail -n1)
BRIDGE_TOKEN="$TOKEN" /usr/local/go/bin/go run . bridge
```

另开一个终端：

```bash
/usr/local/go/bin/go run . hub
```

浏览器打开 `http://127.0.0.1:8088`。

## 常用环境变量

```bash
APP_HOST=127.0.0.1
APP_PORT=8088
HUB_DB_PATH=/opt/codex-bridge/data/codex-bridge.db
HUB_BRIDGE_DOWNLOAD_URL='https://your-release-url/codex-bridge-linux-amd64'
JWT_SECRET='32-byte-random-secret'
AUTH_ALLOW_REGISTRATION=true
BRIDGE_HUB_URL='https://sparkapi.tech'
BRIDGE_TOKEN='enroll-token'
BRIDGE_RUNNER=codex
BRIDGE_CWD='/path/to/workspace'
BRIDGE_MODEL='gpt-5.1-codex-max'
BRIDGE_SANDBOX=workspace-write
BRIDGE_APPROVAL_POLICY=never
```

## 排查

```bash
curl https://sparkapi.tech/health
~/.local/bin/codex-bridge connect '<TOKEN>' --runner echo
/usr/local/go/bin/go test ./...
```

常见问题：

- Bridge 不在线：确认 token 没过期、没有被其他机器消费，并且终端能访问 Hub。
- Codex 无法执行：确认 WSL2/服务器里已经安装并登录 Codex CLI。
- 工作目录不对：用 `--cwd /path/to/workspace` 指定。
- HTTPS Cookie 异常：生产环境设置 `hub.cookie_secure: true`，本地 HTTP 设置为 `false`。
