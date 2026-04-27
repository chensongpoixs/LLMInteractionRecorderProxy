# CLAUDE.md

本文件为 Claude Code (claude.ai/code) 在此仓库中工作时提供指导。

## 项目概述

**proxy-llm** - 基于 Go 的高性能 LLM API 代理服务，将客户端请求透明转发至大语言模型服务商（OpenAI、Anthropic、DeepSeek 等），同时将所有请求/响应数据以数据集格式记录到本地文件。

## 架构

```
Client ──► proxy-llm (Go HTTP 服务) ──► LLM 服务商 API
                    │
                    ▼
              本地存储 (JSONL 文件)
```

### 核心包

| 包 | 路径 | 职责 |
|---------|------|----------------|
| `main` | `cmd/server/main.go` | 入口、CLI 参数、优雅关闭、每日导出调度 |
| `config` | `config/config.go` | YAML 配置加载 + 环境变量覆盖 |
| `proxy` | `proxy/proxy.go` | 核心 HTTP 代理逻辑、请求转发、流式处理、Anthropic↔OpenAI 协议转换 |
| `proxy` | `proxy/handler.go` | HTTP 处理器、用量统计 API、SSE 推送 |
| `proxy` | `proxy/metrics.go` | 请求指标采集与 HTTP 暴露 |
| `proxy` | `proxy/upstream_httplog.go` | 上游 HTTP 日志（客户端→代理→LLM） |
| `proxy` | `proxy/dashboard.go` | Usage Dashboard 前端页面（内嵌 Vue） |
| `storage` | `storage/storage.go` | JSONL 文件日志，请求/响应对的持久化 |
| `exporter` | `exporter/exporter.go` | 每日合并导出为训练数据集格式（id/problem/thinking/solution） |
| `logger` | `logger/logger.go` | 结构化日志系统，支持文件滚动、请求级日志 |

## 开发命令

```bash
# 构建
GOPROXY=https://goproxy.cn,direct go build -o proxy-llm ./cmd/server

# 运行
./proxy-llm --config config.yaml

# 显示版本
./proxy-llm --config config.yaml --version

# 测试（待添加）
go test ./...

# 代码检查
go fmt ./...
go vet ./...
```

## 配置说明

编辑 `config.yaml` 设置 LLM 服务商。主要配置项：

- `models[]`: LLM 服务商数组（name, base_url, api_key, model）
- `server.port`: HTTP 监听端口（默认: 8080）
- `storage.directory`: 请求/响应日志保存目录（默认: ./data）
- `proxy.enable_stream`: 启用 SSE 流式响应
- `proxy.enable_auth`: 启用 API Key 鉴权

环境变量覆盖：
- `PROXY_STORAGE_DIR` - 存储目录

## 数据存储格式

请求/响应对以 **JSONL**（每行一个 JSON 对象）保存：

```
data/
└── 20260425/                                    # 按日期 YYYYMMDD 分目录
    ├── session_model-name_20260425.jsonl         # 请求/响应元数据
    └── streams/
        └── session_xxx_20260425.jsonl             # 流式 chunk 数据
```

每条 JSONL 行包含：id, timestamp, session_id, endpoint, method, model, provider, request_body, status_code, response_body, stream (bool), duration, error (可选), tokens_used (可选)。

## API 端点

| 端点 | 方法 | 说明 |
|----------|--------|-------------|
| `/v1/chat/completions` | POST | Chat Completions 转发 |
| `/v1/messages` | POST | Anthropic Messages API → Chat Completions 转换转发 |
| `/v1/completions` | POST | 文本补全转发 |
| `/v1/embeddings` | POST | 嵌入转发 |
| `/v1/models` | GET | 返回已配置模型列表 |
| `/v1/api/chat` | POST | llama.cpp 兼容端点 |
| `/api/usage/summary` | GET | Token 用量聚合统计（?days=7/30/90） |
| `/api/usage/stream` | GET | SSE 实时推送用量统计 |
| `/usage` | GET | Vue 前端 Usage Dashboard |
| `/health` | GET | 健康检查 |
| `/metrics` | GET | Prometheus 风格指标 |

## 关键实现细节

1. **代理逻辑**: 读取完整请求体，创建指向目标 API 的新 HTTP 请求，将响应转发回客户端
2. **流式处理**: 使用 `http.ResponseWriter.Flusher` 接口实时转发 SSE chunks
3. **并发控制**: `sync.Mutex` 保护存储层文件写入；每个模型拥有独立的 HTTP 客户端
4. **优雅关闭**: 监听 SIGINT/SIGTERM，调用 `http.Server.Shutdown()` 并设置超时
5. **配置**: YAML 文件支持环境变量覆盖
6. **协议转换**: Anthropic Messages API → OpenAI Chat Completions 双向转换（含流式 SSE）
7. **Token 字段规范化**: llama.cpp 的 timings 对象转换为 OpenAI 标准 usage 格式
