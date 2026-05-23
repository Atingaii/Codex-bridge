# Codex Bridge 中文接入指南

Codex Bridge 让浏览器远程访问一台私有机器上的 Codex CLI。Hub 是公网入口和 Web UI，Bridge 从私有机器反向连接 Hub，所以 Hub 不需要保存 `OPENAI_API_KEY`，也不需要直连你的工作目录。

## 最少命令

Hub 机器执行：

```bash
git clone <REPO_URL> codex-bridge && cd codex-bridge
cp configs/dev.yaml.example configs/dev.yaml
/usr/local/go/bin/go run . user --username admin --password 'change-me'
TOKEN=$(/usr/local/go/bin/go run . enroll | tail -n1)
sed -i "s|token:.*|token: ${TOKEN}|" configs/dev.yaml
/usr/local/go/bin/go run . hub
```

Bridge 私有机器执行：

```bash
git clone <REPO_URL> codex-bridge && cd codex-bridge
BRIDGE_HUB_URL='http://HUB_HOST:8088' BRIDGE_TOKEN='<TOKEN>' BRIDGE_RUNNER=codex BRIDGE_CWD='/path/to/workspace' /usr/local/go/bin/go run . bridge
```

浏览器打开 `http://HUB_HOST:8088`，用 `admin / change-me` 登录。生产环境请把 `change-me`、`JWT_SECRET` 和 `<TOKEN>` 换成自己的强随机值，并用 HTTPS。

如果 Hub 和 Bridge 是同一台机器，本地试用可以更短：

```bash
cp configs/dev.yaml.example configs/dev.yaml
/usr/local/go/bin/go run . user --username admin --password 'change-me'
TOKEN=$(/usr/local/go/bin/go run . enroll | tail -n1)
BRIDGE_TOKEN="$TOKEN" /usr/local/go/bin/go run . bridge
```

再开一个终端运行：

```bash
/usr/local/go/bin/go run . hub
```

## 角色说明

- Hub：公网 HTTP/WebSocket 服务，内置 Web UI，SQLite 保存会话和输出。
- Bridge：私有机器上的反向 WebSocket 客户端，持有 Codex/Claude 凭据和工作目录访问权。
- Browser：只连接 Hub，不接触 `OPENAI_API_KEY`。

## 配置文件

默认读取 `configs/${APP_ENV:-dev}.yaml`。也可以用环境变量覆盖，适合给其他用户做一行启动命令。

Hub 关键配置：

```yaml
gateway:
  host: 127.0.0.1
  port: 8088
hub:
  db_path: data/codex-bridge.db
  cookie_secure: false
auth:
  jwt_secret: "replace-with-32-byte-random-secret"
```

Bridge 关键配置：

```yaml
bridge:
  hub_url: http://HUB_HOST:8088
  token: paste-enroll-token-here
  runner: codex
  cwd: /path/to/workspace
  sandbox: workspace-write
  approval_policy: never
```

常用环境变量：

```bash
APP_HOST=127.0.0.1
APP_PORT=8088
HUB_DB_PATH=/opt/codex-bridge/data/codex-bridge.db
JWT_SECRET='32-byte-random-secret'
BRIDGE_HUB_URL='https://your-domain.example'
BRIDGE_TOKEN='enroll-token'
BRIDGE_RUNNER=codex
BRIDGE_CWD='/path/to/workspace'
BRIDGE_MODEL='gpt-5.1-codex-max'
BRIDGE_SANDBOX=workspace-write
BRIDGE_APPROVAL_POLICY=never
```

## 生产部署

构建单个 Go 二进制：

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .
```

Hub 服务器：

```bash
sudo mkdir -p /opt/codex-bridge/configs /opt/codex-bridge/data
sudo cp bin/codex-bridge /usr/local/bin/codex-bridge
sudo cp configs/dev.yaml.example /opt/codex-bridge/configs/prod.yaml
sudo sed -i 's/cookie_secure: false/cookie_secure: true/' /opt/codex-bridge/configs/prod.yaml
APP_ENV=prod CODEX_BRIDGE_CONFIG_DIR=/opt/codex-bridge/configs codex-bridge user --username admin --password 'change-me'
APP_ENV=prod CODEX_BRIDGE_CONFIG_DIR=/opt/codex-bridge/configs codex-bridge enroll --ttl 24h
```

把生成的 enroll token 填到 Bridge 机器的 `BRIDGE_TOKEN`。Hub 推荐放在 Caddy/Nginx 后面做 HTTPS，反代到 `127.0.0.1:8088`。

Bridge 机器：

```bash
BRIDGE_HUB_URL='https://your-domain.example' \
BRIDGE_TOKEN='<TOKEN>' \
BRIDGE_RUNNER=codex \
BRIDGE_CWD='/path/to/workspace' \
codex-bridge bridge
```

## 让其他用户接入

给用户只需要三样东西：

1. Hub 地址：`https://your-domain.example`
2. 登录账号密码：由 Hub 管理员创建
3. Bridge 接入命令：包含一次性 enroll token 和工作目录

推荐发给用户的最短版本：

```bash
BRIDGE_HUB_URL='https://your-domain.example' BRIDGE_TOKEN='<TOKEN>' BRIDGE_RUNNER=codex BRIDGE_CWD="$PWD" codex-bridge bridge
```

如果用户机器还没有二进制，可以给这一版：

```bash
curl -L -o codex-bridge '<DOWNLOAD_URL>' && chmod +x codex-bridge
BRIDGE_HUB_URL='https://your-domain.example' BRIDGE_TOKEN='<TOKEN>' BRIDGE_RUNNER=codex BRIDGE_CWD="$PWD" ./codex-bridge bridge
```

如果用户只想先试通链路，把 `BRIDGE_RUNNER=echo`，不需要 Codex 凭据。

## 编排会话行为

Orchestrate 页面中，只有点击“新运行”才会开启新的编排会话。若用户在当前编排框继续输入并点击开始，系统会沿用当前 run，把历史事件压缩成上下文后交给 Bridge，让 Codex/Claude 带着前文继续工作。压缩内容保留用户目标、已完成动作、命令结果、错误和未解决事项。

## 排查

```bash
curl http://127.0.0.1:8088/health
BRIDGE_RUNNER=echo BRIDGE_HUB_URL='http://HUB_HOST:8088' BRIDGE_TOKEN='<TOKEN>' codex-bridge bridge
/usr/local/go/bin/go test ./...
```

常见问题：

- 登录失败：确认 `user` 命令创建的用户名密码和当前 `APP_ENV`/配置目录一致。
- Bridge 不在线：确认 `BRIDGE_HUB_URL` 能从 Bridge 机器访问，token 没过期且未被消费。
- Cookie 在 HTTPS 下异常：生产环境设置 `hub.cookie_secure: true`，本地 HTTP 设置为 `false`。
- Codex 无法访问仓库：确认 `BRIDGE_CWD` 指向正确目录，sandbox 策略允许需要的文件访问。
