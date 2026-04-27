# LLM Interaction Recorder & Proxy — 大模型交互日志与数据沉淀代理

该组件为基于 Go 构建的高性能 HTTP 反向代理，专用于将客户端请求透明转发至兼容 OpenAI 或 Anthropic 协议的上游服务。在转发的同时，代理以无侵入方式完整捕获每次会话的请求与响应，包括流式传输中产生的所有数据分片，并以 JSONL 格式实时持久化至本地存储。这一机制使每一轮交互均可被精确追溯，从而满足合规审计、行为分析及训练语料构建等需求。系统还提供可选的按日历日合并导出功能，可将当日全部会话记录重组织为统一的行式数据集，直接服务于监督微调、偏好对齐等训练数据的准备工作
## 功能概览

| 能力 | 说明 |
|------|------|
| **多上游** | 在 `config.yaml` 的 `models` 中配置多条路由；请求体里的 `model` 与配置项 `name` 对应，实际调用上游时使用该项的 `model` 与 `base_url`。 |
| **OpenAI 兼容** | `/v1/chat/completions`、`/v1/completions`、`/v1/embeddings`、`/v1/models`，以及 llama.cpp 常用的 `/v1/api/chat`（按 chat completions 处理）。 |
| **Anthropic 原生** | `POST /v1/messages`：支持两种模式 — ① **透传模式**（推荐）：配置 `base_url_anthropic` 后，原始 Anthropic 请求直接转发，不做协议转换；② **兼容模式**（回退）：未配置时自动转换为 OpenAI chat/completions 格式。 |
| **流式** | 可配置开启 SSE；流式分片单独写入按日目录下的 `streams` 子目录。 |
| **落盘** | 非流式/元数据与流式 chunk 分文件追加写入 JSONL。 |
| **可观测** | 健康检查、Prometheus 指标；日志可记录请求体、响应体及「客户端→代理→上游」HTTP 摘要。 |
| **每日导出** | 将某一自然日目录下的请求日志合并为单行 JSONL（`id` / `problem` / `thinking` / `solution` 等字段），见 `daily_export`。 |
| **运维** | 支持 TLS；`SIGINT` / `SIGTERM` 触发优雅关闭（约 10s 超时）。 |

## 架构

```
客户端 (Claude Code / curl / SDK)
        │
        ▼
  proxy-llm (cmd/server + proxy)
        │
        ├──► 上游 LLM (OpenAI 兼容 / Ollama / 自建网关等)
        │
        └──► 本地 data/ … JSONL 与可选 exports/ … 合并文件
```

主要代码包：`cmd/server`（入口与定时导出）、`config`、`proxy`、`storage`、`exporter`、`logger`。更细的协作说明见仓库根目录 [CLAUDE.md](./CLAUDE.md)。

## OpenAI 兼容 API 与 Anthropic 兼容 API

本代理同时支持两种上游 API 协议，通过模型配置中的两个独立 base_url 字段区分：

| 配置项 | 作用 | 示例值 |
|--------|------|--------|
| `base_url` | OpenAI 兼容端点 | `https://api.deepseek.com` |
| `base_url_anthropic` | Anthropic 兼容端点 | `https://api.deepseek.com/anthropic` |

### 路由策略

```
客户端请求                          代理行为
──────────────────────────────────────────────────────────
POST /v1/chat/completions   ──►  base_url + /chat/completions   (OpenAI 透传)
POST /v1/completions         ──►  base_url + /completions        (OpenAI 透传)
POST /v1/embeddings          ──►  base_url + /embeddings         (OpenAI 透传)
GET  /v1/models              ──►  返回配置中的模型列表

POST /v1/messages            ──►  若配置 base_url_anthropic:
                                  → base_url_anthropic + /messages  (Anthropic 原生透传)
                                  若未配置 base_url_anthropic:
                                  → 转换为 OpenAI 格式后发往 base_url (兼容模式)
```

### OpenAI 兼容 API 使用方式

配置模型后，可使用任何 OpenAI 兼容的 SDK 或工具直连代理：

```bash
# curl 示例 — 非流式
curl http://127.0.0.1:20901/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-your-key" \
  -d '{
    "model": "DeepSeekV4",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": false
  }'

# curl 示例 — 流式 (SSE)
curl -N http://127.0.0.1:20901/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-your-key" \
  -d '{
    "model": "DeepSeekV4",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

**OpenAI Python SDK 示例：**

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:20901/v1",
    api_key="sk-your-key",
)

response = client.chat.completions.create(
    model="DeepSeekV4",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)
```

### Anthropic 兼容 API 使用方式

配置 `base_url_anthropic` 后，可使用 Anthropic SDK 直连代理，请求/响应保持原生 Anthropic Messages 格式：

```bash
# curl 示例 — Anthropic Messages API
curl http://127.0.0.1:20901/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-your-key" \
  -d '{
    "model": "DeepSeekV4",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

**Anthropic Python SDK 示例：**

```python
import anthropic

client = anthropic.Anthropic(
    base_url="http://127.0.0.1:20901/v1",
    api_key="sk-your-key",
)

message = client.messages.create(
    model="DeepSeekV4",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello!"}],
)
print(message.content[0].text)
```

**Claude Code 配置示例**（`~/.claude/settings.json`）：

```json
{
  "apiKeyHelper": null,
  "env": {
    "ANTHROPIC_API_KEY": "sk-your-key",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:20901/v1"
  }
}
```

### DeepSeek 双模式配置示例

DeepSeek 同时提供 OpenAI 和 Anthropic 两套兼容端点，推荐同时配置：

```yaml
models:
  - name: "DeepSeekV4"
    base_url: "https://api.deepseek.com"              # OpenAI 兼容
    base_url_anthropic: "https://api.deepseek.com/anthropic"  # Anthropic 兼容
    api_key: "${DEEPSEEK_API_KEY}"
    model: "deepseek-v4-pro"
    timeout: 120s
```

这样配置后，无论客户端使用 OpenAI SDK 还是 Anthropic SDK，都可以无缝接入。

## 环境要求

- Go **1.25**（见 `go.mod`）
- 依赖：`gopkg.in/yaml.v3`

## 构建与运行

```bash
go mod download
go build -o proxy-llm ./cmd/server
```

默认配置文件路径为 `config.yaml`，也可显式指定：

```bash
./proxy-llm --config config.yaml
```

Windows PowerShell 示例：

```powershell
go build -o proxy-llm.exe .\cmd\server
.\proxy-llm.exe --config config.yaml
```

查看版本信息：

```bash
./proxy-llm --version
```

仓库内示例 `config.yaml` 中监听地址为 `0.0.0.0:20901`，请以你本地配置为准。

## 配置说明（`config.yaml`）

### `server`

| 字段 | 含义 |
|------|------|
| `host` / `port` | 监听地址与端口 |
| `tls_cert` / `tls_key` | 非空时启用 HTTPS（`ListenAndServeTLS`） |

### `models`

列表项示例字段：`name`（客户端请求的模型别名）、`base_url`（OpenAI 兼容端点）、`base_url_anthropic`（Anthropic 兼容端点，可选）、`api_key`（支持 `${ENV_VAR}` 占位）、`model`（发给上游的真实模型名）、`timeout`、`max_retries`。可选 llama.cpp 相关：`llama_api`、`llama_api_key`、`llama_model`。

### `storage`

| 字段 | 含义 |
|------|------|
| `directory` | 根目录，默认 `./data` |
| `format` | 当前为 `jsonl` |
| `compress` | 是否 gzip（`.jsonl.gz`） |
| `rotate` / `max_size` | 轮转策略与体积上限（字符串，如 `100mb`） |

### `proxy`

流式开关、请求体大小上限、速率限制（0 表示不限）、CORS、可选 `X-API-Key` 等简易鉴权。

### `monitoring`

`enable_health`、`enable_metrics`、`metrics_path`（默认 `/metrics`）。

### `logging`

| 字段 | 含义 |
|------|------|
| `level` | `debug` / `info` / `warn` / `error` |
| `file`、`max_size_mb`、`max_backups`、`max_age_days`、`compress` | 文件日志滚动 |
| `console` | 是否同时输出到控制台 |
| `request_log`、`request_body_log`、`response_body_log` | 应用日志中的请求/响应记录开关 |
| `upstream_http_log` | 为 `true` 时，在 INFO 级别打印客户端→代理、代理→上游的 HTTP 信息；非流式可含完整响应体（受 `upstream_log_max_bytes` 截断）。流式场景通常只记录状态行与响应头，体为分块转发。 |
| `upstream_log_max_bytes` | 单条上游相关日志的最大字节数；`0` 时内部按 **256 KiB** 生效。 |

### `daily_export`

| 字段 | 含义 |
|------|------|
| `enable` | 是否启用后台定时任务 |
| `output_dir` | 合并文件输出目录 |
| `file_prefix` | 文件名前缀，最终形如 `{prefix}{YYYYMMDD}.jsonl` |
| `run_hour` / `run_minute` | 在 `timezone` 所表示时区的每天触发时刻 |
| `timezone` | 如 `Local`、`Asia/Shanghai`；无效时回退 `Local` |

任务逻辑：在指定时刻导出 **前一自然日** 在 `storage.directory` 下该日目录中的请求类 JSONL（排除各日目录下的 `streams/`），调用 `exporter.ExportDay` 写入一行一条的 [DatasetRow](./exporter/exporter.go) 结构（含从对话中抽取的 `problem` / `thinking` / `solution` 等）。

## HTTP 路由

| 路径 | 方法 | 说明 |
|------|------|------|
| `/v1/chat/completions` | POST | Chat Completions 转发 |
| `/v1/messages` | POST | Anthropic Messages → 上游 Chat Completions |
| `/v1/completions` | POST | 文本补全转发 |
| `/v1/embeddings` | POST | 嵌入转发 |
| `/v1/models` | GET | 返回已配置模型列表 |
| `/api/usage/summary` | GET | Token 用量聚合统计（支持 `?days=7/30/90`） |
| `/api/usage/stream` | GET | SSE：约每秒推送一次与 summary 同结构的 `usage` 事件（`?days=` 同上） |
| `/usage` | GET | Vue 前端 Usage Dashboard（近似 Cursor Usage 结构） |
| `/v1/api/chat` | POST | 与 chat completions 同类处理（兼容 llama.cpp server） |
| `/health` | GET | 健康检查（可在配置中关闭） |
| `/metrics` | GET | Prometheus 指标（路径可由 `metrics_path` 修改） |

未注册的其它路径会由中间件记录告警日志并返回 **404** JSON（内含当前已注册端点列表），不会自动透传到上游。

## Usage Dashboard（Vue）

服务启动后，直接访问：

- `http://127.0.0.1:20901/usage`（端口按你的 `config.yaml` 为准）

页面默认通过 **SSE**（`EventSource`）连接 `GET /api/usage/stream?days=...`，约每秒更新；也可在页面上切换回轮询模式。另可直接请求：

- `GET /api/usage/summary?days=30`（一次性 JSON）
- `GET /api/usage/stream?days=30`（SSE，`event: usage`，data 为 JSON）

返回字段包含：

- 总览：`total_requests`、`success_rate`、`prompt_tokens`、`completion_tokens`、`total_tokens`、`estimated_cost_usd`、`avg_latency_ms`
- 趋势：`daily[]`
- 模型分布：`by_model[]`
- 最近请求：`recent[]`（默认 20 条）

说明：`estimated_cost_usd` 使用行业常见「每百万 token 单价」进行粗略估算，主要用于趋势对比，不等同于真实账单。

### 快速自测（请替换端口与 `model` 为配置中的 `name`）

非流式：

```bash
curl http://127.0.0.1:20901/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d "{\"model\": \"gemma3-4b\", \"messages\": [{\"role\": \"user\", \"content\": \"hi\"}]}"
```

流式（SSE）：

```bash
curl -N http://127.0.0.1:20901/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d "{\"model\": \"gemma3-4b\", \"messages\": [{\"role\": \"user\", \"content\": \"hi\"}], \"stream\": true}"
```

## 数据目录与 JSONL 格式

根目录由 `storage.directory` 决定。当前实现按 **请求时间戳所在自然日** 分子目录：

```
data/
└── 20260425/                          # YYYYMMDD
    ├── session_xxx_20260425.jsonl     # 单次请求/响应元数据（非流式或汇总信息）
    └── streams/
        └── session_xxx_20260425.jsonl   # 流式 chunk 行（ResponseStream）
```

非流式主日志行对应结构体 [RequestLog](./storage/storage.go)（字段包括 `id`、`timestamp`、`session_id`、`endpoint`、`method`、`model`、`provider`、`request_body`、`status_code`、`response_body`、`stream`、`duration`、`error`、`tokens_used` 等）。

流式 chunk 行包含 `id`、`chunk`、`timestamp`、`session_id`、`index` 等。

合并导出文件位于 `daily_export.output_dir`，行格式见 [DatasetRow](./exporter/exporter.go)。

## 环境变量

| 变量 | 行为 |
|------|------|
| `PROXY_STORAGE_DIR` | 若设置，加载配置时覆盖 `storage.directory` |
| `PROXY_PORT` | 代码中若检测到非空会写入端口（当前实现为固定回退值）；**建议以 `config.yaml` 的 `server.port` 为准** |

## 许可证

若仓库未单独提供许可证文件，以项目所有者后续补充为准。
