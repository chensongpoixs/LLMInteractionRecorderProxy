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
| **每日导出** | 将某一自然日目录下的请求日志合并导出；支持 `reasoning`（reasoning 训练格式）、`messages`（OpenAI 微调格式）、`dataset`（工业标准 4 目录结构）三种导出格式。 |
| **工业标准数据集** | `dataset` 格式输出 `prompts/`、`responses/`、`revisions/`、`feedback/` + `metadata.csv`，附带自动语言检测、质量评分、任务分类与去重。 |
| **ModelScope 上传** | 每日导出后自动通过 Git 推送至 ModelScope 数据集仓库（`modelscope.cn`），支持定时与 Dashboard 手动触发。 |
| **HuggingFace 上传** | 同时支持 Git 推送至 HuggingFace 数据集仓库（`huggingface.co`），与 ModelScope 并行、独立配置。 |
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

主要代码包：`cmd/server`（入口与定时导出）、`config`、`proxy`、`storage`、`exporter`、`logger`、`uploader`（Git 数据集上传）。更细的协作说明见仓库根目录 [CLAUDE.md](./CLAUDE.md)。

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
| `file_prefix` | 文件名前缀，最终形如 `{prefix}{YYYYMMDD}.jsonl`（`reasoning`/`messages` 格式使用） |
| `export_format` | 导出格式：`"reasoning"`（reasoning 训练单行 JSONL）、`"messages"`（OpenAI 微调格式）、`"dataset"`（工业标准 4 目录结构，见下方章节） |
| `run_hour` / `run_minute` | 在 `timezone` 所表示时区的每天触发时刻 |
| `timezone` | 如 `Local`、`Asia/Shanghai`；无效时回退 `Local` |

任务逻辑：在指定时刻导出 **前一自然日** 在 `storage.directory` 下该日目录中的请求类 JSONL（排除各日目录下的 `streams/`），根据 `export_format` 调用对应的导出函数。`reasoning` 格式输出含 `id`/`problem`/`thinking`/`solution` 等字段的 [DatasetRow](./exporter/exporter.go)；`messages` 格式输出 OpenAI 兼容的 `{"messages": [...]}` 结构；`dataset` 格式输出工业标准 4 目录结构（详见下方「工业标准数据集导出格式」章节）。

### `modelscope` / `huggingface`（数据集平台自动上传）

两个配置段结构相同，分别控制推送到 ModelScope 和 HuggingFace 的行为：

| 字段 | 含义 |
|------|------|
| `enable` | 是否启用该平台自动上传 |
| `repo_url` | 数据集 Git 仓库地址，支持 `${ENV_VAR}` 占位符（用于注入 token） |
| `repo_dir` | 本地 clone 仓库的目录 |
| `git_user` / `git_email` | Git 提交使用的身份信息 |
| `data_subdir` | 仓库中存放数据的子目录名（默认 `data`） |
| `upload_delay` | 导出完成后等待秒数再推送（确保文件落盘完毕） |
| `git_branch` | 推送目标分支（ModelScope 用 `master`，HuggingFace 用 `main`） |

上传流程：每日定时导出完成后，对每个启用的平台依次执行 `git clone/pull` → 复制当日导出文件至仓库 → `git add/commit/push`。也可通过 Dashboard 或 API 手动触发上传。

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
| `/api/export/day` | GET | 手动触发单日数据导出（`?day=20260428&format=dataset`） |
| `/api/prompts/dates` | GET | 返回按月/天分组的提示词统计（`{"months":[{"month":"202604","days":[{"date":"20260428","count":34}]}]}`） |
| `/api/prompts` | GET | 按天分页查询提示词列表（`?date=20260428&page=1&per_page=20`），含思考过程+回复 |
| `/api/modelscope/upload` | POST | 手动触发 ModelScope 数据集上传 |
| `/api/huggingface/upload` | POST | 手动触发 HuggingFace 数据集上传 |
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

### 提示词浏览（Prompt Browser）

Dashboard 第三个 Tab「**提示词浏览**」提供按日浏览全部用户提问与大模型回复的能力：

- **左侧日期树**：按月份分组折叠/展开，每天显示拦截到的提示词数量
- **右侧 Q&A 卡片**：选中日期后，每条交互以卡片展示
  - 点击卡片展开完整内容
  - 🧠 **思考过程**：紫色背景区块，展示大模型内部推理链（reasoning/thinking），等宽字体，独立滚动
  - 💬 **模型回复**：蓝色背景区块，展示最终回复正文
  - 语言标签（zh/en/mixed）、模型名称、token 消耗一并展示
- 支持分页浏览，每页 20 条

API 层面：
- `GET /api/prompts/dates` — 获取有数据的月份/日期树及每日条数
- `GET /api/prompts?date=20260428&page=1` — 按天分页查询，返回提问文本 + 思考过程 + 回复内容

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

## 工业标准数据集导出格式（`export_format: dataset`）

当 `daily_export.export_format` 设置为 `"dataset"` 时，导出器将本地日志按工业标准 4 目录结构组织，适合直接用于监督微调、偏好对齐等训练流程。

### 目录结构

```
exports/20260428/
├── prompts/
│   └── prompts.jsonl       # 原始用户提示，保留真实任务语境
├── responses/
│   └── responses.jsonl     # 模型原始输出（含模型名称、参数快照）
├── revisions/
│   └── revisions.jsonl     # 多轮交互修订记录与 diff 追踪
├── feedback/
│   └── feedback.jsonl      # 自动推断的质量信号
└── metadata.csv            # 聚合统计与字段说明
```

### 字段说明

**`prompts/prompts.jsonl`** — 每行一个 [PromptRecord](./exporter/exporter.go)：

| 字段 | 说明 |
|------|------|
| `id` | SHA256 去重哈希（16 位 hex） |
| `prompt` | 清洗后的用户提示文本（去除 system-reminder 标签） |
| `system_prompt` | 系统提示（如有） |
| `language` | 自动检测的语言：`zh`/`en`/`mixed` |
| `task_category` | 任务分类：`code`/`reasoning`/`creative`/`general`/`instruction` |
| `token_count` | Prompt token 消耗 |
| `timestamp` | 请求时间戳 |

**`responses/responses.jsonl`** — 每行一个 [ResponseRecord](./exporter/exporter.go)：

| 字段 | 说明 |
|------|------|
| `model` / `provider` | 模型名称与上游服务商标识 |
| `thinking` | 推理链（如果模型输出包含 reasoning/thinking） |
| `response` | 模型正文输出 |
| `prompt_tokens` / `completion_tokens` / `total_tokens` | Token 用量快照 |
| `latency_ms` | 请求延迟（毫秒） |
| `stream` / `status_code` | 是否流式请求、HTTP 状态码 |

**`revisions/revisions.jsonl`** — 每行一个 [RevisionRecord](./exporter/exporter.go)：

检测多轮对话中的 `multi_turn` 和 `repeated_prompt` 模式，记录同一会话内 prompt/response 的演变轨迹：`session_id`、`prompt_hash`、`turn_index`、`total_turns`、`revision_type`。

**`feedback/feedback.jsonl`** — 每行一个 [FeedbackRecord](./exporter/exporter.go)：

自动质量评分（0-1 scale），基于内容完整度、推理链比例、错误状态等信号：`quality_score`、`response_completeness`、`has_thinking`、`thinking_ratio`、`has_error`。

**`metadata.csv`** — 汇总行记录本次导出的聚合统计（total_records、filtered_out、by_language、by_model、avg_quality_score 等）。

### 数据过滤

导出过程中执行 4 层过滤，确保数据集质量：

1. **完整性** — 剔除错误且无响应体的记录（流式记录不受此限制）
2. **HTTP 状态** — 仅保留 `status_code < 400` 的记录
3. **Prompt 质量** — 清洗后提示文本长度 ≥ 5 字符
4. **Response 质量**（仅非流式） — 响应或 thinking 内容至少 5 字符

### 与 reasoning/messages 格式对比

| 维度 | `reasoning` | `messages` | `dataset` |
|------|-------------|------------|-----------|
| 输出形式 | 单个 JSONL 文件 | 单个 JSONL 文件 | 4 目录 + metadata.csv |
| 数据过滤 | 无 | 无 | 4 层质量控制 |
| 质量评分 | 无 | 无 | 自动评分（0-1） |
| 语言检测 | 无 | 无 | 自动 CJK/Latin 检测 |
| 去重 | 无 | 无 | SHA256 内容哈希 |
| 修订追踪 | 无 | 无 | 多轮对话 revision 检测 |
| 适用场景 | 推理训练 | OpenAI 微调 | 全场景训练数据集 |

## 数据集平台自动上传

导出完成后，系统支持通过 Git 将数据集自动推送至 ModelScope 和/或 HuggingFace 平台。两个平台独立配置、并行上传。

### ModelScope

仓库地址：<https://www.modelscope.cn/datasets/chensongpoixs/llm_interaction_corpus>

配置 `config.yaml` 中 `modelscope.enable: true` 后，每日凌晨导出完成后自动执行：

1. `git clone` 或 `git pull` 同步仓库到 `repo_dir`
2. 将 `exports/` 下当日导出文件递归复制到仓库的 `data_subdir/YYYYMMDD/` 目录
3. 生成 `metadata.csv` 汇总文件
4. `git add -A && git commit && git push origin master`

前提条件：在 [ModelScope 个人设置](https://modelscope.cn/my/myaccesstoken) 生成 Git 访问令牌，填入 `repo_url` 的认证部分。

### HuggingFace

仓库地址：<https://huggingface.co/datasets/chensongpoixs/llm_interaction_corpus>

配置 `config.yaml` 中 `huggingface.enable: true` 后，上传流程与 ModelScope 相同，区别在于推送目标分支为 `main`（通过 `git_branch` 配置）。

前提条件：在 [HuggingFace Token 设置](https://huggingface.co/settings/tokens) 生成 Access Token，填入 `repo_url`（推荐使用 `${HF_TOKEN}` 环境变量占位符）。

### 手动触发

除定时任务外，还可通过以下方式手动触发上传：

- **Dashboard**：Usage Dashboard → 数据导出 Tab → 下拉选择导出格式 → 点击「上传 ModelScope」/「上传 HuggingFace」
- **API**：`POST /api/modelscope/upload` 或 `POST /api/huggingface/upload`（需先完成当日导出）

### Token 安全

`repo_url` 中支持 `${ENV_VAR}` 环境变量占位符，系统在加载配置时自动展开。推荐将 token 存放在环境变量中而非明文写入配置文件：

```yaml
huggingface:
  repo_url: "https://${HF_USERNAME}:${HF_TOKEN}@huggingface.co/datasets/${HF_USERNAME}/llm_interaction_corpus.git"
```

## 环境变量

| 变量 | 行为 |
|------|------|
| `PROXY_STORAGE_DIR` | 若设置，加载配置时覆盖 `storage.directory` |
| `PROXY_PORT` | 代码中若检测到非空会写入端口（当前实现为固定回退值）；**建议以 `config.yaml` 的 `server.port` 为准** |
| `HF_TOKEN` / `HF_USERNAME` / `HF_EMAIL` | HuggingFace 配置中 `${...}` 占位符引用的环境变量（按需设置） |
| `MODELSCOPE_GIT_TOKEN` | ModelScope 配置中 `${...}` 占位符引用的环境变量（按需设置） |
| `DEEPSEEK_API_KEY` | 模型配置中 `api_key: "${DEEPSEEK_API_KEY}"` 的展开源 |

## 许可证

若仓库未单独提供许可证文件，以项目所有者后续补充为准。
