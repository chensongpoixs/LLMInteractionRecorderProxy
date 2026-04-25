# Claude Code 使用本地 LLM 代理指南

## 概述

本文档说明如何配置 Claude Code 使用本地运行的 llama.cpp 模型作为后端，通过 proxy-llm 代理服务进行 API 转发。

## 架构

```
┌─────────────────┐     HTTP      ┌──────────────────┐     HTTP      ┌──────────────┐
│   Claude Code   │ ──────────►  │  proxy-llm 代理   │ ──────────►  │ llama.cpp    │
│   (客户端)      │ ◄──────────  │  (localhost:20901)│ ◄──────────  │  模型服务    │
└─────────────────┘              └──────────────────┘              └──────────────┘
                                                        │
                                                  ┌─────▼────────┐
                                                  │ Qwen3.6 模型  │
                                                  │ (gguf 文件)   │
                                                  └──────────────┘
```

## 第一步：启动 llama.cpp 模型服务

在你的服务器（js1.blockelite.cn）上执行：

```bash
nohup ./llama-server \
  -m /home/vipuser/Work/model/Qwen3.6-35B-A3B-Claude-4.6-Opus-Reasoning-Distilled.Q8_0.gguf \
  --host 0.0.0.0 \
  --port 57700 \
  -c 262000 \
  > server.log 2>&1 &
```

验证服务是否启动成功：

```bash
# 检查进程
ps aux | grep llama-server

# 检查端口
ss -tlnp | grep 57700

# 测试健康端点
curl http://localhost:57700/v1/models
```

## 第二步：启动 proxy-llm 代理

确保 config.yaml 中的配置正确：

```yaml
models:
  - name: "qwen3.6"
    base_url: "http://localhost:57700/v1"  # llama.cpp 地址
    api_key: "sk-not-required"              # llama.cpp 不需要
    model: "qwen3.6-35b-a3b-claude-4.6-opus-reasoning-distilled.q8_0"
    timeout: 300s
```

启动代理：

```bash
GOPROXY=https://goproxy.cn,direct go build -o proxy-llm ./cmd/server
./proxy-llm --config config.yaml
```

## 第三步：在 Claude Code 中配置模型

### 方法一：使用 `--base-url` 和 `--model` 参数

在 Claude Code 命令行中启动时指定：

```bash
claude --base-url http://192.168.3.2:20901/v1 --model qwen3.6
```

### 方法二：设置环境变量

在启动 Claude Code 之前设置：

```bash
export OPENAI_API_KEY=sk-not-required
export OPENAI_BASE_URL=http://localhost:20901/v1
claude
```

### 方法三：在 Claude Code 的 `.claude/settings.json` 中配置

创建或编辑 `~/.claude/settings.json`：

```json
{
  "anthropic": {
    "apiKey": "sk-not-required"
  },
  "proxy": {
    "baseURL": "http://localhost:20901/v1"
  }
}
```

### 方法四：使用 .env 文件

在项目根目录创建 `.env` 文件：

```env
OPENAI_API_KEY=sk-not-required
OPENAI_BASE_URL=http://localhost:20901/v1
```

然后在 `.claude/settings.json` 中引用：

```json
{
  "anthropic": {
    "apiKey": "${OPENAI_API_KEY}"
  },
  "proxy": {
    "baseURL": "${OPENAI_BASE_URL}"
  }
}
```

## 关键配置说明

| 配置项 | 值 | 说明 |
|--------|-----|------|
| Claude Code 模型名 | `qwen3.6` | 对应 config.yaml 中的 `models[].name` |
| 代理地址 | `http://localhost:20901` | proxy-llm 服务地址 |
| API Key | `sk-not-required` | 本地代理不需要真实 Key |
| llama.cpp 模型名 | `qwen3.6-35b-a3b-claude-4.6-opus-reasoning-distilled.q8_0` | 对应 config.yaml 中的 `models[].model` |

## 验证连接

### 1. 检查代理健康状态

```bash
curl http://localhost:20901/health
# 预期返回: OK
```

### 2. 测试聊天接口

```bash
curl http://localhost:20901/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.6",
    "messages": [{"role": "user", "content": "你好，请介绍一下自己"}],
    "max_tokens": 100
  }'
```

### 3. 查看代理日志

```bash
tail -f logs/app.log
```

## 数据存储

每次请求都会被保存到本地：

```
data/
├── qwen3.6/
│   ├── qwen3.6_20260425_100000.jsonl    # 按日期分文件
│   └── qwen3.6_20260425_100500.jsonl
└── streams/
    └── qwen3.6_20260425.jsonl          # 流式响应分块
```

## 常见问题

### Q: Claude Code 提示 "model not found"

A: 确保 config.yaml 中的 `models[].name` 与 Claude Code 使用的模型名完全一致（区分大小写）。

### Q: 连接超时

A: 检查 llama.cpp 服务是否正常运行，端口 57700 是否可达。

### Q: 响应内容为空

A: 增加 `timeout` 配置（当前为 300s），大模型推理可能需要更长时间。

### Q: 流式响应不工作

A: 确认 config.yaml 中 `proxy.enable_stream: true`。

## 性能调优建议

1. **增大 context 窗口**：llama.cpp 启动时 `-c 262000` 已经很大，根据显存调整
2. **增加代理超时**：大模型响应慢，建议 `timeout: 300s` 或更长
3. **启用日志**：设置 `logging.level: "debug"` 排查问题
4. **监控存储**：JSONL 文件会增长，定期清理或启用压缩
