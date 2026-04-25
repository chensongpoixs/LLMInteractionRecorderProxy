# Proxy LLM - 大模型 API 代理服务

## 功能特性

- **API 转发**：兼容 OpenAI 格式的 API 请求转发
- **流式响应**：支持 SSE 流式响应
- **数据持久化**：自动保存每次请求/响应到本地 JSONL 文件
- **多模型支持**：可配置多个 LLM provider
- **流控制**：支持速率限制
- **监控指标**：提供健康检查和 metrics 端点

## 快速开始

### 1. 编译

```bash
go build -o proxy-llm ./cmd/server
```

### 2. 配置

编辑 `config.yaml`，设置你的 LLM API 密钥和端点。

### 3. 运行

```bash
./proxy-llm --config config.yaml
```

### 4. 测试

```bash
# 非流式请求
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello"}]
  }'

# 流式请求
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

## 数据存储

请求和响应数据保存在 `./data` 目录下，按 session 和日期组织：

```
data/
├── session_gpt-4/
│   ├── session_gpt-4_20240101_120000.jsonl
│   └── session_gpt-4_20240101_120500.jsonl
├── session_claude/
│   └── session_claude_20240101_121000.jsonl
└── streams/
    └── session_gpt-4_20240101.jsonl
```

每个 JSONL 文件包含：

```jsonl
{"id":"req_1234567890","timestamp":"2024-01-01T12:00:00Z","session_id":"session_gpt-4","endpoint":"chat/completions","method":"POST","model":"gpt-4","provider":"gpt-4","request_body":{"model":"gpt-4","messages":...},"status_code":200,"response_body":{"choices":...},"stream":false,"duration":"1.234s","tokens_used":{"prompt_tokens":10,"completion_tokens":50,"total_tokens":60}}
```

## API 端点

| 端点 | 方法 | 描述 |
|------|------|------|
| `/v1/chat/completions` | POST | 聊天完成（转发） |
| `/v1/completions` | POST | 文本补全（转发） |
| `/v1/embeddings` | POST | 向量嵌入（转发） |
| `/health` | GET | 健康检查 |
| `/metrics` | GET | Prometheus 指标 |

## 环境变量

| 变量 | 描述 |
|------|------|
| `PROXY_PORT` | 服务器端口（覆盖 config） |
| `PROXY_STORAGE_DIR` | 数据存储目录（覆盖 config） |
