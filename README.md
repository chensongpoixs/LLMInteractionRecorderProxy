# LLM Interaction Recorder & Proxy — 大模型交互日志与数据沉淀代理

基于 Go 构建的高性能 LLM API 反向代理，透明转发客户端请求至兼容 OpenAI / Anthropic 协议的上游服务商，同时以无侵入方式完整捕获每次会话的请求与响应（含流式分片），JSONL 实时落盘。支持按日历日导出为工业标准训练数据集，并通过 Git 自动推送至 ModelScope / HuggingFace 平台。

---

## 目录

1. [架构总览](#1-架构总览)
2. [包级设计](#2-包级设计)
3. [请求处理管线](#3-请求处理管线)
4. [协议转换详解](#4-协议转换详解)
5. [数据存储管线](#5-数据存储管线)
6. [ReAct Agent 子系统](#6-react-agent-子系统)
7. [数据集导出系统](#7-数据集导出系统)
8. [可观测性体系](#8-可观测性体系)
9. [配置参考](#9-配置参考)
10. [API 端点全集](#10-api-端点全集)
11. [构建与部署](#11-构建与部署)
12. [安全模型](#12-安全模型)
13. [性能特征](#13-性能特征)

---

## 1. 架构总览

```
                         ┌──────────────────────────┐
                         │    Storage Layer          │
                         │  JSONL + gzip + daily     │
                         │  subdirectory rotation    │
                         └──────────┬───────────────┘
                                    │ SaveRequest / SaveStreamChunk
                                    │
  Client ───► Proxy (HTTP mux) ─────┤
    │            │                   │
    │            ├── Middleware chain (CORS → 404-logger → Auth)
    │            │
    │            ├── /v1/chat/completions ──► Upstream OpenAI
    │            ├── /v1/messages ──────────► Upstream Anthropic (passthrough)
    │            │                        └─► Upstream OpenAI (converted)
    │            ├── /api/chat/agent ───────► Upstream LLM (ReAct loop)
    │            │
    │            └── Background goroutine:
    │                 ┌── Daily export (exporter)
    │                 └── Git push (uploader → ModelScope / HuggingFace)
    │
    └── Usage Dashboard (Vue 3 SPA, SSE, embedded via go:embed)
```

### 核心设计决策

| 决策 | 理由 |
|------|------|
| **双协议并存** | `base_url` + `base_url_anthropic` 独立配置，避免协议转换损耗；仅在未配置 Anthropic 端点时才回退到 OpenAI 转换 |
| **无客户端超时** | 上游 HTTP Client 不设 `Timeout`，因为 SSE 流式连接可持续数分钟，设置超时会截断长流 |
| **两阶段 YAML 加载** | `Unmarshal → ExpandEnv → Re-marshal → Unmarshal` 流程，确保 `${ENV_VAR}` 展开发生在值级别，避免 Go 类型系统将 `"${PORT}"` 字符串拒绝为 int 字段 |
| **每会话 Mutex** | `sync.Map` 本身不足以保证 Load-Modify-Store 原子性；每个 session 绑定独立的 `conversationState`（含专属 `sync.Mutex`） |
| **JSON/gzip 在锁外** | `storage.Logger.SaveRequest` 将序列化与压缩放在 `mu.Lock()` 之前，仅文件写入持有锁，最大化并发吞吐 |
| **环形缓冲区指标** | `Metrics` 使用 10000 容量 ring buffer，无论总请求量多大，内存占用量保持恒定 |
| **Git 作为分发协议** | ModelScope 和 HuggingFace 均原生支持 Git 数据集管理；使用 shallow clone 和最小 diff |
| **Go 1.25 embed** | 前端 Vue SPA 在编译时嵌入二进制，零外部依赖部署 |

---

## 2. 包级设计

### 包依赖图

```
cmd/server/main.go
  ├── config          YAML 加载 + ${ENV_VAR} 展开 + 默认值
  ├── logger          分级结构化日志（控制台 + 文件滚动）
  ├── storage         JSONL 持久化（按日分目录 + gzip + 流式 chunk）
  ├── proxy
  │     ├── proxy.go          HTTP Server + 路由 + 中间件 + 代理转发 + 协议转换
  │     ├── handler.go        /api/usage/* + /api/prompts/* + /health 处理器
  │     ├── agent.go          ReAct Agent SSE 端点 + 5 工具沙箱
  │     ├── metrics.go        环形缓冲区 + Prometheus 文本格式暴露
  │     ├── dashboard.go      go:embed Vue SPA 服务
  │     ├── upstream_httplog.go  客户端→代理→上游 全链路 HTTP 日志
  │     └── web/usage.html    Vue 3 单页应用（编译时嵌入）
  ├── exporter        reasoning / messages / dataset 三格式导出
  └── uploader        Git 推送至 ModelScope / HuggingFace
```

### 各包职责

#### `cmd/server/main.go` — 入口

- 解析 `--config` / `--version` CLI 参数
- 按依赖顺序初始化各组件（logger → storage → metrics → exporter → proxy）
- 在 goroutine 中启动 HTTP Server
- 在 goroutine 中启动每日定时导出调度器
- 监听 SIGINT/SIGTERM，优雅关闭（10s 超时）

#### `config/config.go` — 配置系统

- **两阶段加载**：`yaml.Unmarshal → expandEnvVars → yaml.Marshal → yaml.Unmarshal`，确保类型安全的同时支持 `${VAR}` 占位
- **运行时覆盖**：`PROXY_PORT` / `PROXY_STORAGE_DIR` 环境变量在加载后直接修改结构体字段值
- **默认值**：`DefaultConfig()` 提供生产级合理默认值

核心结构体：

| 结构体 | 说明 |
|--------|------|
| `ModelConfig` | 单个上游模型定义：name、base_url（OpenAI）、base_url_anthropic、api_key、model（真实名）、timeout、max_retries |
| `StorageConfig` | 存储目录、JSONL 格式、gzip 压缩、轮转策略（daily/weekly/size） |
| `ProxyConfig` | 流式开关、请求体大小上限、速率限制、CORS、鉴权 |
| `DailyExportConfig` | 启用、输出目录、文件前缀、导出格式（reasoning/messages/dataset）、定时时刻、时区 |
| `DatasetRepoConfig` | ModelScope/HuggingFace 共用：启用、仓库 URL、本地目录、Git 身份、推送分支 |
| `AgentConfig` | 工作区目录、最大迭代次数、bash 超时秒数 |

#### `logger/logger.go` — 结构化日志

- 全局单例（`sync.Once`），线程安全
- 双输出：控制台 + 文件（支持滚动）
- `WithRequestID(id)` 创建子 Logger，自动为每条日志附加 `[REQ:<id>]` 前缀
- 每请求专用结构化方法：`LogRequest` / `LogResponse` / `LogProxyForward` / `LogStorage` / `LogStreaming` / `LogServer`
- 工具函数：`TruncateString`（截断）、`MaskString`（脱敏）

#### `proxy/proxy.go` — 核心代理（约 3000 行）

主要职能：
1. HTTP Server 生命周期管理
2. 路由注册与中间件链组装（CORS → 404-Logger → Auth）
3. 请求解析 → 模型路由 → 名称翻译 → 上游请求构造 → 流式/非流式分发
4. Anthropic ↔ OpenAI 协议双向转换
5. Token 字段规范化（llama.cpp timings → OpenAI usage 格式）
6. 多轮对话会话历史管理（per-session mutex）
7. 安全响应 Writer（捕获 ServeMux 404 的默认 HTML 输出）
8. 手动导出/上传 API 端点

#### `proxy/handler.go` — 用量与提示词 API

- `HandleUsageSummary` — 扫描全部 JSONL 日志，聚合 token 用量、成功率、延迟、费用估算
- `HandleUsageStream` — SSE 实时推送（每 1s 一个 `usage` 事件）
- `HandlePromptDates` — 返回按月/天分组的提示词数据树
- `HandlePromptList` — 按天分页查询提示词+思考+回复，支持去重
- `HandleHealthCheck` — 健康检查，返回请求/错误计数

#### `proxy/agent.go` — ReAct Agent

独立子章节 [§6](#6-react-agent-子系统)。

#### `proxy/metrics.go` — 指标采集

- 环形缓冲区：`maxMetricSamples = 10000` 条延迟样本 + 响应体大小样本
- 请求/错误计数器（按 `{path}_{statusCode}` 键分组）
- Prometheus 文本格式暴露：`/metrics`

#### `storage/storage.go` — JSONL 持久化

- 按请求时间戳所属自然日分目录：`data/YYYYMMDD/`
- 非流式元数据 → `session_xxx_YYYYMMDD.jsonl[.gz]`
- 流式分片 → `data/YYYYMMDD/streams/session_xxx_YYYYMMDD.jsonl`
- 线程安全：`sync.RWMutex` 保护文件路径计算逻辑；序列化/压缩在锁外执行
- `ExtractMessagesFromRequest` — 同时解析 OpenAI 和 Anthropic 消息格式

#### `exporter/exporter.go` — 数据集导出

独立子章节 [§7](#7-数据集导出系统)。

#### `uploader/modelscope.go` — Git 上传

- 统一抽象：`DatasetUploader`，`platform` 字段区分 ModelScope / HuggingFace
- 流程：`syncRepo`（clone 或 pull） → 复制导出文件 → `generateMetadataCSV` → `git add/commit/push`
- 支持日期子目录树递归复制

---

## 3. 请求处理管线

### 3.1 中间件链

```
Request
  │
  ▼
createCORSMiddleware ─── 若 enable_cors，设置 Access-Control-* 响应头；
                          对 OPTIONS 预检请求直接返回 204
  │
  ▼
create404Logger ──────── 包装 ResponseWriter 以捕获状态码；
                          对未匹配路由返回 JSON 404 + 已注册端点列表
  │
  ▼
createAuthMiddleware ─── 若 enable_auth，验证 Authorization / X-API-Key 头；
                          不匹配则返回 401 JSON
  │
  ▼
Route Handler ────────── handleRequest / handleAnthropicMessages / handleAgentChat / ...
```

### 3.2 非流式请求处理

```
handleRequest(endpoint)
  │
  ├── 1. 限制 BodyReader → io.LimitReader(maxBodySize)
  ├── 2. 读取并解析 JSON 请求体
  ├── 3. 从 JSON 中提取 "model" 字段 → getModelByProxyName() 查找配置
  ├── 4. translateModelNameInBody() 将代理名替换为上游真实模型名
  ├── 5. 构造上游 *http.Request (method + URL + headers + body)
  ├── 6. extractAndUpdateConversation() 多轮对话历史注入
  ├── 7. client.Do(upstreamReq)
  ├── 8. 读取完整响应体 → normalizeTokens (llama.cpp 兼容)
  ├── 9. extractTokens() 提取 token 用量
  ├── 10. appendConversation() 记录用户+助手消息
  ├── 11. logRequestFull() → storage.SaveRequest()
  ├── 12. 将响应体写回客户端
```

### 3.3 流式请求处理（SSE）

```
handleStreaming()
  │
  ├── 1. 设置 SSE 响应头 (text/event-stream)
  ├── 2. client.Do(upstreamReq)
  ├── 3. 循环读取 upstream.Body (bufio.Scanner, 1MB 缓冲区)
  │     ├── 解析 SSE 行 (data: {...})
  │     ├── normalizeStreamChunk() 规范化 token 字段
  │     ├── 写入 ResponseWriter + Flush()
  │     ├── storage.SaveStreamChunk() 逐 chunk 落盘
  │     └── 累积 content/delta/text 到 aggregatedResponse
  ├── 4. 流结束后构造最终 RequestLog (含 aggregatedResponse)
  ├── 5. storage.SaveRequest() 写入汇总行
  └── 6. 发送最终事件 (非 [DONE] 标记时补发)
```

### 3.4 Anthropic 透传模式（流式）

```
handleAnthropicPassthroughStream()
  │
  ├── 1. 原始 Anthropic 请求体直接转发至 base_url_anthropic + /messages
  ├── 2. client.Do(upstreamReq)
  ├── 3. 循环读取上游 SSE
  │     ├── 累积 delta.text → content
  │     ├── 累积 delta.thinking → reasoning
  │     ├── 捕获 usage 事件 → tokens
  │     └── 逐行写入客户端 + Flush()
  ├── 4. 流结束后构造 RequestLog (含聚合的 content/reasoning/tokens)
  └── 5. storage.SaveRequest()
```

---

## 4. 协议转换详解

### 4.1 Anthropic → OpenAI 转换

转换在 `handleAnthropicMessages` 中触发，条件为模型**未配置** `base_url_anthropic`。

```
Anthropic Messages Request                  OpenAI Chat Completions Request
──────────────────────────────────          ──────────────────────────────
system (string or array)              →     messages[0] = {role: "system", content: "..."}
messages[].role = "user"             →     {role: "user", content: [...]}
  content[].type = "text"            →       text content blocks
  content[].type = "tool_result"     →       tool role messages
messages[].role = "assistant"        →     {role: "assistant", content: "...", tool_calls: [...]}
  content[].type = "text"            →       content string
  content[].type = "thinking"        →       reasoning_content (DeepSeek/Claude 兼容)
  content[].type = "tool_use"        →       tool_calls[{function: {name, arguments}}]
tools[]                              →     tools[] = [{type: "function", function: {name, description, parameters}}]
thinking/reasoning                   →     reasoning_effort (透传)
```

### 4.2 OpenAI → Anthropic 转换（流式 SSE）

转换在 `handleAnthropicMessagesStream` 中执行，将 OpenAI SSE chunk 翻译为 Anthropic SSE 事件序列：

```
OpenAI SSE Chunk                           Anthropic SSE Event
──────────────────                         ────────────────────
[首个 chunk]                         →     event: message_start
                                            data: {"type":"message_start","message":{"id":"...","model":"...","role":"assistant",...}}

choices[0].delta.content             →     event: content_block_delta
                                            data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"..."}}

choices[0].delta.reasoning_content   →     event: content_block_delta
                                            data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"..."}}

choices[0].delta.tool_calls[]        →     event: content_block_delta
                                            data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"..."}}

choices[0].finish_reason             →     event: content_block_stop
                                      →     event: message_delta (含 stop_reason + usage)
                                      →     event: message_stop
                                            data: {"type":"message_stop"}
```

### 4.3 llama.cpp Token 规范化

llama.cpp server 使用非标准 `timings` 对象而非 `usage`，代理在转发时自动规范化：

```
llama.cpp 字段                    OpenAI 标准字段
─────────────────                ─────────────────
timings.prompt_n           →     usage.prompt_tokens → usage.input_tokens
timings.predicted_n        →     usage.completion_tokens → usage.output_tokens
timings.* (整个对象)        →     从响应体中移除
```

流式场景中还处理 `timings.prompt_n` / `timings.predicted_n` 在 stream chunk 中的出现。

---

## 5. 数据存储管线

### 5.1 存储路径

```
{storage.directory}/
└── YYYYMMDD/                                    # 请求时间戳的自然日
    ├── {session}_{provider}_{YYYYMMDD}.jsonl     # 非流式元数据（每请求一行）
    ├── {session}_{provider}_stream_{YYYYMMDD}.jsonl  # 流式汇总行
    └── streams/
        └── {session}_{provider}_stream_{YYYYMMDD}.jsonl  # 流式分片（每 chunk 一行）
```

### 5.2 JSONL 行格式

**非流式 / 流式汇总行** (`storage.RequestLog`)：

```json
{
  "id": "req_1745800000_12345",
  "timestamp": "2026-04-28T12:00:00+08:00",
  "session_id": "session_claude-haiku-4-5-20251001_20260428",
  "conversation_id": "conv_abc123",
  "turn_index": 3,
  "endpoint": "/v1/chat/completions",
  "method": "POST",
  "model": "claude-haiku-4-5-20251001",
  "provider": "deepseek",
  "system_prompt": "",
  "messages": [
    {"role": "user", "content": "帮我写一个排序算法"},
    {"role": "assistant", "content": "以下是快速排序的实现...", "reasoning": "用户需要一个排序算法..."}
  ],
  "request_body": { /* 完整原始请求体 */ },
  "status_code": 200,
  "response_body": { /* 完整原始响应体 */ },
  "aggregated_response": "流式场景下的聚合文本内容",
  "stream": true,
  "duration": 1234567890,
  "tokens_used": {"prompt_tokens": 150, "completion_tokens": 500},
  "error": ""
}
```

**流式分片行** (`storage.ResponseStream`)：

```json
{
  "id": "req_1745800000_12345",
  "chunk": "data: {\"choices\":[{\"delta\":{\"content\":\"排序\"},\"index\":0}]}",
  "timestamp": "2026-04-28T12:00:01+08:00",
  "session_id": "session_claude-haiku-4-5-20251001_20260428",
  "index": 15
}
```

### 5.3 线程安全保证

```
SaveRequest(reqLog) 调用方
  │
  ├── json.Marshal(reqLog)          ← 无锁，CPU 密集型
  ├── gzip.Compress(jsonBytes)      ← 无锁（若 compress: true）
  │
  └── logger.mu.Lock()
        ├── 计算目标文件路径（按 reqLog.Timestamp 日期）
        ├── os.MkdirAll（幂等）
        ├── os.OpenFile(O_APPEND|O_CREATE|O_WRONLY)
        ├── file.Write(payload)
        ├── file.Close()
        └── logger.mu.Unlock()
```

关键：序列化和 gzip 压缩在锁外完成，仅短临的文件打开-写入-关闭持有互斥锁。

---

## 6. ReAct Agent 子系统

### 6.1 架构

```
POST /api/chat/agent
  │
  ▼
handleAgentChat(w, r)
  │
  ├── 解析请求体: {model, message, tools[]}
  ├── 查找模型配置
  ├── 设置 SSE 响应头
  │
  └── ReAct 循环 (max ≤ max_iterations):
        │
        ├── 1. callLLM() ─ 非流式调用，response_format: json_object
        │       └── 返回原始 JSON 字符串
        │
        ├── 2. extractJSON() ─ 从 Markdown 代码块或裸文本中提取 JSON
        │       └── 解析为 agentLLMResponse {thought, action?, final_answer?}
        │
        ├── 3. 发送 SSE "thinking" 事件
        │
        ├── 4. 若 final_answer 存在:
        │       ├── 发送 SSE "response" 事件
        │       └── break (退出循环)
        │
        ├── 5. 若 action 存在:
        │       ├── 发送 SSE "action" 事件
        │       ├── executeTool(tool, args) ─ 工具沙箱执行
        │       ├── 发送 SSE "observation" 事件（含工具返回值）
        │       └── 将 tool_use + tool_result 追加到消息历史
        │
        └── 6. 循环结束 │ 错误发生:
                └── 发送 SSE "done" 或 "error" 事件
```

### 6.2 System Prompt

Agent 的实现使用内嵌的 ReAct 提示词，指导 LLM 遵循 Think → Act → Observe 循环，输出固定 JSON 格式：

```json
{"thought": "推理过程...", "action": {"tool": "bash", "arguments": {"command": "ls"}}}
```
或最终答案：
```json
{"thought": "已获得足够信息", "final_answer": "回答内容..."}
```

### 6.3 工具沙箱

5 个工具共享路径沙箱机制：

| 工具 | 参数 | 沙箱机制 | 输出限制 |
|------|------|---------|---------|
| `read_file` | `path` | `filepath.Join(workspace, cleanPath)` → 验证绝对路径前缀 = workspace | 64 KB |
| `write_file` | `path`, `content` | 同上 + 自动创建父目录 | — |
| `bash` | `command` | 黑名单（14 条危险命令） + `context.WithTimeout` | 8 KB |
| `grep` | `pattern`, `path` | 路径沙箱 + `--exclude-dir=.git` | 4 KB |
| `glob` | `pattern` | 路径沙箱 + `filepath.Glob` | 2 KB（最多 100 条） |

**bash 命令黑名单**：`rm -rf`、`sudo`、`chmod 777`、`mkfs`、`dd if=`、`fork bomb (:(){)`、`curl`、`wget`、`shutdown`、`reboot`、`kill`、`chown`、`passwd`、输出重定向到 `/dev/*`

**路径沙箱实现** (`sandboxPath`)：

```go
func sandboxPath(workspace, userPath string) (string, error) {
    clean := filepath.Clean(userPath)
    // 拒绝绝对路径中可能逃逸的 ..
    abs := filepath.Join(workspace, clean)
    // 验证 Join 结果的前缀等于 workspace
    if !strings.HasPrefix(abs, workspace) {
        return "", fmt.Errorf("path traversal denied: %s", userPath)
    }
    return abs, nil
}
```

### 6.4 SSE 事件类型

| 事件 | 方向 | 载荷 |
|------|------|------|
| `thinking` | 服务端→客户端 | `{"iteration": N, "thought": "推理过程..."}` |
| `action` | 服务端→客户端 | `{"iteration": N, "tool": "bash", "arguments": {...}, "description": "列出目录文件"}` |
| `observation` | 服务端→客户端 | `{"iteration": N, "result": "stdout...", "truncated": false}` |
| `response` | 服务端→客户端 | `{"content": "最终回答..."}` |
| `done` | 服务端→客户端 | `{"iterations": 3, "tokens_used": {"prompt": 1500, "completion": 800}}` |
| `error` | 服务端→客户端 | `{"message": "错误描述..."}` |

---

## 7. 数据集导出系统

### 7.1 三种导出格式

| 格式 | 输出 | 过滤 | 去重 | 评分 | 适用场景 |
|------|------|------|------|------|---------|
| `reasoning` | 单个 `{prefix}YYYYMMDD.jsonl` | 无 | 无 | 无 | 推理链训练（Opus 4.6 风格） |
| `messages` | 单个 `{prefix}YYYYMMDD.jsonl` | 无 | 无 | 无 | OpenAI 微调 API 直接使用 |
| `dataset` | 4 目录 + `metadata.csv` | 4 层质量控制 | SHA256 | 0-1 自动评分 | 全场景工业标准训练数据集 |

### 7.2 Dataset 格式（推荐）

```
exports/20260428/
├── prompts/
│   └── prompts.jsonl       # PromptRecord: id, prompt, system_prompt, language, task_category, token_count, timestamp
├── responses/
│   └── responses.jsonl     # ResponseRecord: model, provider, thinking, response, prompt/completion/total_tokens, latency_ms, stream, status_code
├── revisions/
│   └── revisions.jsonl     # RevisionRecord: session_id, prompt_hash, turn_index, total_turns, revision_type (multi_turn/repeated_prompt)
├── feedback/
│   └── feedback.jsonl      # FeedbackRecord: quality_score, response_completeness, has_thinking, has_error, thinking_ratio, latency_ms, total_tokens
└── metadata.csv            # 聚合统计：total_records, filtered_out, by_language, by_model, avg_quality_score
```

### 7.3 数据质量控制（dataset 格式）

**4 层过滤**：
1. **完整性** — 剔除有 error 且无 response_body 的记录（流式记录例外）
2. **HTTP 状态** — 仅保留 `status_code < 400` 的记录
3. **Prompt 质量** — 清洗后（去除 `<system-reminder>` 标签）的 prompt 文本 ≥ 5 字符
4. **Response 质量** — 非流式响应中，response 或 thinking 内容至少 5 字符

**自动标注**：
- **语言检测** — CJK 字符占比 > 30% → `zh`；ASCII > 70% → `en`；其余 → `mixed`/`unknown`
- **任务分类** — 关键词匹配：code（函数名/关键字）、math（数学符号）、creative（故事/诗）、general
- **质量评分**（0-1 连续值）— 综合 response_completeness、thinking_ratio、error 状态
- **难度推断** — 模型名关键词 + 回复长度启发式
- **SHA256 去重** — `problem + thinking + solution` 的 SHA256 前 16 位 hex

### 7.4 定时导出调度

```go
// runDailyExport 后台循环
for {
    // 计算距离下一次 runHour:runMinute 的等待时间
    sleepDuration := time.Until(nextRunTime)
    select {
    case <-time.After(sleepDuration):
        // 导出前一自然日的数据
        yesterday := time.Now().In(tz).AddDate(0, 0, -1)
        exporter.ExportDatasetDay(storageDir, yesterday, outputDir)
        // 等待 upload_delay 后触发上传
        time.AfterFunc(uploadDelay, func() {
            uploader.UploadDay(ctx, storageDir, exportDir, prefix, yesterday)
        })
    case <-ctx.Done():
        return
    }
}
```

### 7.5 Git 上传流程

```
syncRepo()
  ├── .git 不存在 → git clone --depth 1  (浅克隆)
  └── .git 已存在 → git pull --rebase    (增量同步)
        │
copyDir(exports/YYYYMMDD/ → repo/data/YYYYMMDD/)
  └── 递归复制，覆盖已存在文件
        │
generateMetadataCSV()
  └── 扫描全部日期子目录，聚合统计，写入 metadata.csv
        │
gitAddCommitPush()
  ├── git add -A
  ├── git diff --cached --quiet  (无变更则跳过)
  ├── git commit -m "dataset: YYYYMMDD — N records"
  └── git push origin <branch>
```

---

## 8. 可观测性体系

### 8.1 日志系统

**分级输出**（`logger.Level`）：

| 级别 | 用途 | 示例 |
|------|------|------|
| DEBUG | 开发调试、全量请求/响应体 | 请求体内容、上游 HTTP 头 |
| INFO | 运行时关键事件 | 请求摘要、导出完成、上传状态 |
| WARN | 需关注的异常 | 404 路由、上游返回 4xx |
| ERROR | 调用失败 | 上游连接失败、序列化错误 |
| FATAL | 致命错误后退出 | 配置加载失败 |

**上游 HTTP 日志**（`upstream_httplog.go`，通过 `logging.upstream_http_log` 开关控制）：

- **客户端→代理**：方法、路径、头（Authorization 脱敏）、请求体（截断至 `upstream_log_max_bytes`）
- **代理→上游**：完整 URL、头（API Key 脱敏）、请求体
- **上游→代理（非流式）**：状态码、头、完整响应体
- **上游→代理（流式）**：状态码、头（体为分块转发，不记录完整内容）

### 8.2 指标系统

**`/metrics` 端点**（Prometheus 文本格式）：

```
# HELP proxy_requests_total Total number of requests by endpoint and status
# TYPE proxy_requests_total counter
proxy_requests_total{endpoint="chat_completions",status="200"} 1523
proxy_requests_total{endpoint="chat_completions",status="500"} 3
proxy_requests_total{endpoint="messages",status="200"} 89

# HELP proxy_errors_total Total number of errors by endpoint
# TYPE proxy_errors_total counter
proxy_errors_total{endpoint="chat_completions"} 3

# HELP proxy_latency_seconds Request latency in seconds
# TYPE proxy_latency_seconds summary
proxy_latency_seconds{quantile="0.5"} 1.2
proxy_latency_seconds{quantile="0.95"} 5.8
proxy_latency_seconds{quantile="0.99"} 12.3

# HELP proxy_bytes_sent_total Total bytes sent to clients
# TYPE proxy_bytes_sent_total counter
proxy_bytes_sent_total 125829120
```

**`/health` 端点**：

```json
{"status": "healthy", "requests": 1615, "errors": 3}
```

### 8.3 Usage Dashboard

内嵌 Vue 3 SPA（`proxy/web/usage.html`，约 1300 行），通过 `//go:embed` 编译进二进制。

**四个功能 Tab**：

1. **用量总览** — 汇总卡片（请求数、Token 量、费用估算、成功率、平均延迟）、按日趋势柱状图（Chart.js）、模型分布、最近请求列表。支持 SSE 实时更新或轮询切换。
2. **数据导出** — 按日选择导出格式、下载 / 推送至 ModelScope / HuggingFace。
3. **提示词浏览** — 左侧按月分组日期树、右侧 Q&A 卡片（思考过程紫色背景 + 模型回复蓝色背景）、语言/模型/Token 标签。
4. **Agent 聊天** — ReAct Agent 交互界面，模型选择器、5 工具复选框、折叠式 Think/Action/Observation 流式展示。

**前端技术栈**：
- Vue 3 CDN（Options API）
- Chart.js 4 CDN
- 原生 `fetch()` + `response.body.getReader()` 处理 POST SSE（因为 `EventSource` 不支持 POST）
- CSS 变量暗色主题，与 Cursor 控制台风格一致

---

## 9. 配置参考

### 9.1 完整配置项

```yaml
# =============================================================================
# 服务端
# =============================================================================
server:
  host: "0.0.0.0"       # 监听地址
  port: 20901            # 监听端口
  tls_cert: ""           # TLS 证书路径（非空启用 HTTPS）
  tls_key: ""            # TLS 私钥路径

# =============================================================================
# 上游模型（数组，可配置多个）
# =============================================================================
models:
  - name: "DeepSeekV4"                              # 客户端请求中 model 字段的值
    base_url: "https://api.deepseek.com"            # OpenAI 兼容端点
    base_url_anthropic: "https://api.deepseek.com/anthropic"  # Anthropic 兼容端点（可选）
    api_key: "${DEEPSEEK_API_KEY}"                  # 支持环境变量占位
    model: "deepseek-v4-pro"                        # 上游真实模型名
    timeout: 300s                                   # 连接超时（非请求总超时）
    max_retries: 3                                  # 当前预留字段（未在 Client 层实现自动重试）
    # llama.cpp 专用
    llama_api: ""                                   # llama.cpp 端点（可选）
    llama_api_key: ""                               # llama.cpp API Key（可选）
    llama_model: ""                                 # llama.cpp 模型名（可选）

# =============================================================================
# 存储
# =============================================================================
storage:
  directory: "./data"       # JSONL 根目录
  format: "jsonl"           # 固定 jsonl
  compress: false           # 是否 gzip 压缩（.jsonl.gz）
  rotate: "daily"           # daily | weekly | size
  max_size: ""              # rotate=size 时的体积上限（如 "100mb"）

# =============================================================================
# 代理行为
# =============================================================================
proxy:
  enable_stream: true       # 启用 SSE 流式转发
  max_body_size: "10mb"     # 请求体大小上限
  rate_limit: 0             # 每分钟请求限制（0 = 不限）
  enable_cors: true         # 启用跨域
  allowed_origins: ["*"]    # CORS 允许的 Origin
  enable_auth: false        # 启用 API Key 鉴权
  auth_header: "Authorization"  # 鉴权头名称
  auth_token: ""            # 期望的鉴权头值

# =============================================================================
# 监控
# =============================================================================
monitoring:
  enable_health: true       # 开启 /health
  enable_metrics: true      # 开启 /metrics
  metrics_path: "/metrics"  # 指标端点路径

# =============================================================================
# 日志
# =============================================================================
logging:
  level: "debug"            # debug | info | warn | error
  file: "./logs/app.log"    # 文件日志路径
  max_size_mb: 100          # 单文件大小上限（触发滚动）
  max_backups: 10           # 保留的滚动备份数
  max_age_days: 30          # 备份保留天数
  compress: true            # 滚动后是否 gzip 压缩备份
  console: true             # 是否同时输出到控制台
  request_log: true         # 是否记录请求级日志
  request_body_log: false   # 是否记录请求体（DEBUG 场景）
  response_body_log: false  # 是否记录响应体（DEBUG 场景）
  upstream_http_log: false  # 是否记录客户端→代理→上游的 HTTP 摘要
  upstream_log_max_bytes: 262144  # 上游日志单条最大字节（0 = 默认 256KB）

# =============================================================================
# 每日导出
# =============================================================================
daily_export:
  enable: true
  output_dir: "./exports"
  file_prefix: "Opus-4.6-Reasoning-3000x-filtered-"  # reasoning/messages 格式用
  export_format: "dataset"      # reasoning | messages | dataset
  run_hour: 0                   # 导出时刻（时）
  run_minute: 5                 # 导出时刻（分）
  timezone: "Asia/Shanghai"     # 时区

# =============================================================================
# ModelScope 数据集上传
# =============================================================================
modelscope:
  enable: true
  repo_url: "https://oauth2:${MODELSCOPE_GIT_TOKEN}@www.modelscope.cn/datasets/chensongpoixs/llm_interaction_corpus.git"
  repo_dir: "./modelscope_repo"
  git_user: "proxy-llm"
  git_email: "proxy-llm@example.com"
  data_subdir: "data"
  upload_delay: 10              # 导出完成后等待秒数
  git_branch: "master"

# =============================================================================
# HuggingFace 数据集上传
# =============================================================================
huggingface:
  enable: true
  repo_url: "https://${HF_USERNAME}:${HF_TOKEN}@huggingface.co/datasets/${HF_USERNAME}/llm_interaction_corpus.git"
  repo_dir: "./huggingface_repo"
  git_user: "proxy-llm"
  git_email: "proxy-llm@example.com"
  data_subdir: "data"
  upload_delay: 10
  git_branch: "main"

# =============================================================================
# ReAct Agent
# =============================================================================
agent:
  workspace_dir: "./agent_workspace"
  max_iterations: 15            # ReAct 循环最大次数
  bash_timeout: 30              # bash 工具超时秒数
```

### 9.2 环境变量

| 变量 | 作用 | 优先级 |
|------|------|--------|
| `PROXY_STORAGE_DIR` | 覆盖 `storage.directory` | 高于 YAML |
| `PROXY_PORT` | 覆盖 `server.port` | 高于 YAML |
| `DEEPSEEK_API_KEY` | `${DEEPSEEK_API_KEY}` 占位符展开 | — |
| `HF_TOKEN` | HuggingFace API Token | — |
| `HF_USERNAME` | HuggingFace 用户名 | — |
| `HF_EMAIL` | HuggingFace 提交邮箱 | — |
| `MODELSCOPE_GIT_TOKEN` | ModelScope Git 访问令牌 | — |

---

## 10. API 端点全集

### 代理端点

| 方法 | 路径 | 说明 | 流式支持 |
|------|------|------|---------|
| POST | `/v1/chat/completions` | OpenAI Chat Completions | ✅ `stream: true` |
| POST | `/v1/messages` | Anthropic Messages（透传或自动转换） | ✅ `stream: true` |
| POST | `/v1/completions` | Text Completions | ✅ |
| POST | `/v1/embeddings` | Embeddings | ❌ |
| GET | `/v1/models` | 返回配置的模型列表（OpenAI 格式） | — |
| POST | `/v1/api/chat` | llama.cpp 兼容端点 | ✅ |

### 用量与浏览 API

| 方法 | 路径 | 参数 | 返回 |
|------|------|------|------|
| GET | `/api/usage/summary` | `?days=30` | JSON：total_requests, success_rate, tokens, cost, daily[], by_model[], recent[] |
| GET | `/api/usage/stream` | `?days=30` | SSE (`event: usage`)，每秒推送 |
| GET | `/api/prompts/dates` | — | JSON：按月份分组的日期树 |
| GET | `/api/prompts` | `?date=20260428&page=1&per_page=20` | JSON：分页提示词+思考+回复列表 |

### 导出与上传 API

| 方法 | 路径 | 参数 | 说明 |
|------|------|------|------|
| GET | `/api/export/dates` | — | 返回可导出日期列表 |
| POST | `/api/export/day` | `?day=20260428&format=dataset` | 手动触发单日导出 |
| GET | `/api/export/download` | `?day=20260428` | 下载已导出文件 |
| POST | `/api/modelscope/upload` | `?day=20260428` | 手动触发 ModelScope 上传（202 Accepted） |
| POST | `/api/huggingface/upload` | `?day=20260428` | 手动触发 HuggingFace 上传（202 Accepted） |
| POST | `/api/dataset/upload` | `?day=20260428` | 同时触发两个平台上传 |

### Agent API

| 方法 | 路径 | 请求体 | 返回 |
|------|------|------|------|
| POST | `/api/chat/agent` | `{"model":"DeepSeekV4","message":"...","tools":["bash","read_file","write_file","grep","glob"]}` | SSE 流（thinking/action/observation/response/done/error 事件） |

### 运维端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查：`{"status":"healthy","requests":N,"errors":N}` |
| GET | `/metrics` | Prometheus 文本格式指标 |
| GET | `/usage` | Usage Dashboard HTML（内嵌 Vue SPA） |
| GET | `/api/logs` | 列出日志文件（`?session=&date=`） |
| GET | `/api/log` | 读取日志文件内容（`?file=path`） |

### 未匹配路由

任何未注册路径返回 **404 JSON**：

```json
{
  "error": "not found",
  "path": "/unknown/path",
  "method": "GET",
  "available_endpoints": [
    "POST /v1/chat/completions",
    "POST /v1/messages",
    "GET /v1/models",
    "GET /usage",
    "GET /health",
    "GET /metrics",
    "GET /api/usage/summary",
    "..."
  ]
}
```

---

## 11. 构建与部署

### 11.1 构建

```bash
# 设置 Go 代理（国内环境）
export GOPROXY=https://goproxy.cn,direct

# 下载依赖
go mod download

# 构建（Linux）
go build -o proxy-llm ./cmd/server

# 构建（Windows）
go build -o proxy-llm.exe ./cmd/server

# 代码检查
go fmt ./...
go vet ./...
```

**依赖**：
- Go ≥ 1.25
- `gopkg.in/yaml.v3`（唯一外部依赖）

### 11.2 运行

```bash
# 默认配置
./proxy-llm --config config.yaml

# 查看版本
./proxy-llm --version
```

### 11.3 与 Claude Code 集成

在 `~/.claude/settings.json` 中配置：

```json
{
  "env": {
    "ANTHROPIC_API_KEY": "sk-your-key",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:20901/v1"
  }
}
```

或通过环境变量：

```bash
export ANTHROPIC_API_KEY="sk-your-key"
export ANTHROPIC_BASE_URL="http://127.0.0.1:20901/v1"
```

### 11.4 与 OpenAI SDK 集成

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
```

### 11.5 与 Anthropic SDK 集成

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
```

---

## 12. 安全模型

### 12.1 鉴权

- `proxy.enable_auth: true` + `proxy.auth_token` 启用 Bearer Token 鉴权
- 中间件检查 `Authorization: Bearer <token>` 或可配置的自定义头（`proxy.auth_header`）
- 不匹配返回 `401 Unauthorized` JSON

### 12.2 Agent 沙箱

**路径逃逸防护**：
- 所有工具操作的路径通过 `sandboxPath()` 强制约束在 `workspace_dir` 内
- 先 `filepath.Clean()` 规范化，再 `filepath.Join(workspace, cleanPath)`，最后验证结果前缀 = workspace 的绝对路径

**命令注入防护**：
- bash 工具黑名单过滤器在命令执行前拦截包含危险模式的命令
- `context.WithTimeout` 限制执行时间（可配置，默认 30s）
- 输出截断至 8KB 防止内存膨胀

**网络隔离**：
- `curl` 和 `wget` 被列入黑名单，禁止 Agent 发起外部 HTTP 请求

### 12.3 敏感信息保护

- `config.yaml` 中的 `api_key`、`repo_url` 推荐使用 `${ENV_VAR}` 环境变量占位符，避免明文密钥入库
- `.gitignore` 排除 `config.yaml`（含密钥的运行时配置不入库）
- `config.yaml.example` 仅含 `${ENV_VAR}` 占位符，可安全入库
- 上游 HTTP 日志自动脱敏 `Authorization` 和 `X-API-Key` 头的值

### 12.4 请求体大小限制

- `proxy.max_body_size` 限制请求体大小（默认 10MB）
- 防止大 payload DoS 攻击

---

## 13. 性能特征

### 13.1 并发模型

- **每模型独立 HTTP Client**：`MaxIdleConnsPerHost=10`，`IdleConnTimeout=90s`，连接复用减少握手开销
- **无客户端超时**：避免长 SSE 连接被截断
- **存储锁粒度**：JSON 序列化/gzip 在锁外，仅文件追加写入持锁（微秒级）
- **每会话 Mutex**：多轮对话历史更新使用 per-session 锁，不同会话互不阻塞

### 13.2 内存管理

- 环形缓冲区限制指标样本数至 10000 条，不随请求量线性增长
- 流式 Scanner 缓冲区 1MB（`bufio.MaxScanTokenSize`），可处理大 SSE chunk
- 日志文件 Scanner 缓冲区 2MB，兼容长多轮对话 JSONL 行
- 编译时内嵌 Vue SPA（约 1300 行），零运行时前端文件 I/O

### 13.3 延迟特征

- 非流式代理：增加约 0.1-0.5ms（JSON 解析 + 日志写入 + Token 规范化）
- 流式代理：首个 chunk 延迟增加约 0.1ms；逐 chunk 增加 flush + 写入开销约 0.05ms/chunk
- 协议转换（Anthropic→OpenAI）：增加约 0.2-0.5ms（消息格式重组）
- 流式协议转换（OpenAI SSE→Anthropic SSE）：增加约 0.1ms/chunk

### 13.4 磁盘 I/O

- 每请求 1 次追加写入（非流式）或 N+1 次（流式，N = chunk 数 + 1 汇总行）
- gzip 压缩以 CPU 换取磁盘空间（约 10:1 压缩比用于 JSONL 文本）
- 按日分目录避免单目录文件数膨胀

---

## 许可证

若仓库未单独提供许可证文件，以项目所有者后续补充为准。

---

## 相关文档

- [CLAUDE.md](./CLAUDE.md) — Claude Code 协作指引
- [docs/claude-code-proxy-guide.md](./docs/claude-code-proxy-guide.md) — 与 Claude Code + llama.cpp 本地模型集成的详细指南
- [docs/feature_gap_analysis.md](./docs/feature_gap_analysis.md) — 与 LiteLLM / Ollama / vLLM / Open WebUI 的功能差距分析矩阵
