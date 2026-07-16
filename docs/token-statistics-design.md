# Token 统计与请求/响应保存设计文档

## 1. 概述

proxy-llm 代理服务需要完整记录每次 LLM 调用的：
- **请求体** (request_body): 原始请求 JSON，包含 messages/system_prompt 等
- **响应体** (response_body): 原始响应 JSON，包含 choices/content 等
- **Token 统计** (tokens_used): prompt_tokens / completion_tokens / total_tokens
- **对话上下文** (messages, system_prompt, conversation_id): 多轮对话追踪

## 2. 请求路径总览

```
客户端
  │
  ├─ /v1/chat/completions        ──► handleRequest (非流式/流式)
  ├─ /v1/completions             ──► handleRequest
  ├─ /v1/embeddings              ──► handleRequest
  ├─ /v1/api/chat                ──► handleRequest
  │
  ├─ /v1/messages                ──► handleAnthropicMessages
  │      ├─ 有 base_url_anthropic ──► handleAnthropicPassthrough (非流式)
  │      ├─ 有 base_url_anthropic ──► handleAnthropicPassthroughStream (流式)
  │      └─ 无 anthropic base ──► OpenAI 转换 (非流式/流式)
  │
  └─ /api/chat/agent             ──► handleAgentChat (ReAct 智能体)
```

## 3. Token 提取机制

### 3.1 非流式: extractTokens

```go
func (p *Proxy) extractTokens(resp map[string]interface{}) map[string]int {
    // 1. 从 resp["usage"] 提取 usage 对象
    // 2. normalizeTokenKeys(usage) 规范化字段名
    // 3. 返回 {prompt_tokens, completion_tokens, total_tokens}
}
```

**支持的字段名映射：**

| 上游格式 | 规范化后 |
|---------|---------|
| prompt_tokens | prompt_tokens |
| input_tokens | prompt_tokens |
| completion_tokens | completion_tokens |
| output_tokens | completion_tokens |
| total_tokens | total_tokens |

### 3.2 流式: extractTokenFromChunk

在每个 SSE chunk 上调用，支持两种数据来源：
1. `chunk["usage"]` → 标准 usage 对象
2. `chunk["timings"]` → llama.cpp 特有（当 usage 不可用时）

**注意**: llama.cpp 的 timings 是**累积值**，流式场景中需要在每个 chunk 上取最大值而非累加。

### 3.3 响应格式适配

| 上游 | 响应格式 | 提取方式 |
|------|---------|---------|
| OpenAI | `{"usage": {"prompt_tokens": N}}` | ✅ 直接 |
| llama.cpp | `{"usage": {"prompt_tokens": N}, "timings": {...}}` | ✅ 直接 |
| Anthropic | `{"usage": {"input_tokens": N, "output_tokens": M}}` | ✅ fallback |

## 4. 各路径详细数据流

### 4.1 handleRequest (非流式) — `/v1/chat/completions`

```
请求体 → reqBody map
    │
    ├─► extractTokens(respParsed)  // 从 resp["usage"] 提取
    │
    └─► logRequestFull(
            reqBody,           ← ✅ 完整保存
            conversationMessages,  ← ✅ 对话历史
            systemPrompt,        ← ✅ 系统提示
            tokensUsed,          ← ✅ token 统计
        )
```

### 4.2 handleRequest (流式) — handleStreaming()

```
SSE chunks → 逐个处理
    │
    ├─► extractTokenFromChunk(streamTokens, chunk)  // 累积
    ├─► SaveStreamChunk(chunk)  // streams/ 目录
    └─► logRequestFull(
            reqBody,
            AggregatedResponse,  ← 聚合完整响应
            tokensUsed,          ← 流结束后赋值
        )
```

### 4.3 handleAnthropicPassthrough (非流式)

```
原始 Anthropic 请求 → reqBody
    │
    ├─► extractTokens(respParsed)  // Anthropic: input_tokens → prompt_tokens
    │
    └─► logRequestFull(
            reqBody,           ← ✅ 原始 Anthropic 格式
            conversationMessages,
            systemPrompt,
            tokensUsed,
        )
```

### 4.4 handleAnthropicPassthroughStream (流式)

```
Anthropic SSE → 逐个处理
    │
    ├─► extractTokenFromChunk(passthroughTokens, evt)
    ├─► SaveStreamChunk
    └─► logRequestFull(
            reqBody,
            AggregatedResponse,
            tokensUsed,
        )
```

### 4.5 handleAnthropicMessages (Anthropic→OpenAI 转换，非流式)

```
Anthropic 请求 → reqBody
    │
    ├─► convertAnthropicMessagesToOpenAI() → chatReq
    │
    ├─► OpenAI 端点调用
    │
    ├─► normalizeTokens(respBody)  // input_tokens↔prompt_tokens 转换
    │
    ├─► extractTokens(openAIResp)
    │
    └─► logRequest(           ⚠️ 使用的是 logRequest 而非 logRequestFull
            chatReq,          ← ⚠️ OpenAI 格式，不是原始 Anthropic
            normalizedBody,
            tokensUsed,
        )
```

**问题**: 不保存 Messages / SystemPrompt / ConversationID

### 4.6 handleAnthropicMessages (流式)

```
Anthropic SSE → normalizeStreamChunk → 累积
    │
    └─► logRequestFull(
            reqBody (原始 Anthropic),
            conversationMessages,
            systemPrompt,
            tokensUsed,
        )
```

### 4.7 handleAgentChat (ReAct 智能体)

```
用户消息 → ReAct 循环
    │
    ├─► callLLM() → (content, usageJSON, fullResponse)
    │
    ├─► normalizeTokenKeys 后累加到 totalUsageJSON
    │
    └─► 循环结束后:
            tokens = normalizeTokenKeys(totalUsageJSON)
            reqLog.TokensUsed = tokens
            SaveRequest()
```

## 5. 存储格式

### 5.1 主日志文件 (JSONL)

```
data/YYYYMMDD/{sessionID}_{date}.jsonl
```

每条记录包含:
```json
{
    "id": "req_xxx",
    "timestamp": "2026-07-16T...",
    "session_id": "session_modelname",
    "conversation_id": "conv_xxx",
    "turn_index": 1,
    "endpoint": "chat/completions",
    "method": "POST",
    "model": "llama3.1",
    "provider": "llama3.1",
    "system_prompt": "...",
    "messages": [{"role": "user", "content": "..."}],
    "request_body": {"messages": [...]},
    "response_body": {"choices": [...], "usage": {...}},
    "aggregated_response": null,
    "stream": false,
    "status_code": 200,
    "duration": "1.5s",
    "error": "",
    "tokens_used": {
        "prompt_tokens": 1820,
        "completion_tokens": 42,
        "total_tokens": 1862
    }
}
```

### 5.2 流式 chunk 文件

```
data/YYYYMMDD/streams/{sessionID}_{date}.jsonl
```

每条记录:
```json
{
    "id": "req_xxx",
    "chunk": "data: {...}",
    "timestamp": "...",
    "session_id": "session_modelname_stream",
    "index": 0
}
```

## 6. 已知问题与修复计划

### P0 - 必须修复

| # | 问题 | 路径 | 影响 | 修复方案 |
|---|------|------|------|---------|
| 1 | **流式 token 统计可能重复累加 llama.cpp timings** | handleStreaming | 流式响应中 llama.cpp 的 timings 是累积值，但 extractTokenFromChunk 做 += 累加 | 改用取最大值而非累加，或只在有 usage 对象时读取 |
| 2 | **Anthropic 转换路径 (非流式) 使用 logRequest 而非 logRequestFull** | handleAnthropicMessages | 不保存 Messages / SystemPrompt / ConversationID | 改用 logRequestFull |

### P1 - 建议改进

| # | 问题 | 路径 | 影响 | 修复方案 |
|---|------|------|------|---------|
| 3 | **Anthropic 转换路径保存的是 OpenAI 格式请求体** | handleAnthropicMessages | 提示词浏览时看不到原始 Anthropic 格式 | 同时保存原始 Anthropic 请求体 (需要扩展 RequestLog) |
