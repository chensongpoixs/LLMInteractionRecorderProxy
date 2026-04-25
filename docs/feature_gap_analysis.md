# proxy-llm 功能完善路线图

## 概述

本文档基于业界主流 LLM 代理服务（Ollama、vLLM、llama.cpp、LiteLLM、Open WebUI）的功能对标，详细列出 proxy-llm 当前缺失或需要完善的功能模块。

---

## 一、请求路由与负载均衡

### 1.1 负载均衡策略（缺失）

**现状**：当前项目仅通过请求体中的 `model` 字段选择后端，不支持同一模型的多节点负载分担。

**竞品参考**：
- **LiteLLM**：支持 Round Robin、Least Connections、Weighted、Random 四种策略
- **vLLM**：内置请求队列和动态负载均衡

**需要实现的功能**：

```yaml
# 建议的配置格式
load_balancing:
  strategy: "round_robin"  # round_robin, least_connections, weighted, random
  health_check_interval: 30s
  failover_timeout: 10s
  nodes:
    - url: "http://backend1:8080"
      weight: 1
      priority: 1
    - url: "http://backend2:8080"
      weight: 1
      priority: 2  # 备用节点
```

**实现要点**：
- 每个模型支持多节点配置
- 健康检查：定期 ping 后端节点，标记不可用节点
- 故障转移：主节点失败时自动切换备用节点
- 权重分配：支持按权重分配请求比例

---

### 1.2 请求重试与退避（部分缺失）

**现状**：配置中有 `max_retries` 字段，但代理层未实现重试逻辑。

**需要实现的功能**：
- 指数退避重试（Exponential Backoff）：`retry_delay = base_delay * 2^attempt`
- 重试条件可配置：仅重试特定 HTTP 状态码（502, 503, 504）
- 令牌桶限流重试：避免重试风暴
- 重试预算：每日/每小时最大重试次数

---

## 二、速率限制与流量控制

### 2.1 速率限制（配置存在但未实现）

**现状**：`proxy.rate_limit` 配置项存在，值为 0（无限制），但代码中无任何限流逻辑。

**竞品参考**：
- **LiteLLM**：支持 per-user、per-model、全局三种粒度的速率限制
- **Open WebUI**：支持 Token 级别的速率限制

**需要实现的功能**：

```yaml
rate_limiting:
  global:
    requests_per_minute: 100
    tokens_per_minute: 50000
  per_user:
    enabled: true
    requests_per_minute: 30
    tokens_per_minute: 10000
  per_model:
    enabled: true
    gpt-4:
      requests_per_minute: 50
    claude-3:
      requests_per_minute: 40
  strategies:
    overflow: "reject"  # reject, queue, fallback
    queue_max_size: 1000
    queue_timeout: 30s
```

**实现要点**：
- 使用令牌桶（Token Bucket）或漏桶（Leaky Bucket）算法
- 支持 Redis 作为分布式共享计数器
- 请求排队而非直接拒绝（可选）
- 返回 429 Too Many Requests 并包含 `Retry-After` 头

---

### 2.2 并发连接限制（缺失）

**现状**：HTTP Transport 配置了 `MaxIdleConns` 和 `MaxIdleConnsPerHost`，但无并发请求限制。

**需要实现的功能**：
- 最大并发请求数限制
- 请求队列深度限制
- 连接池大小动态调整

---

## 三、缓存层

### 3.1 响应缓存（完全缺失）

**现状**：每次请求都会转发到上游，无任何缓存机制。

**竞品参考**：
- **Open WebUI**：支持相同对话历史的结果缓存
- **vLLM**：PagedAttention 实现 KV Cache（底层优化）

**需要实现的功能**：

```yaml
cache:
  enabled: true
  backend: "memory"  # memory, redis, memcached
  redis:
    url: "redis://localhost:6379"
    db: 0
  ttl: 3600  # 秒
  max_size: 10000  # 条目数
  key_generation:
    # 缓存键生成策略：基于请求内容
    include_headers: ["Authorization"]
    normalize: true  # 归一化请求参数
  scope:
    cache_by_model: true
    cache_by_system_prompt: true
    cache_by_tools: true
  hit_response:
    headers:
      X-Cache: "HIT"
    latency_improvement_tracking: true
```

**缓存键生成逻辑**：
1. 哈希 `model` + `messages` + `system_prompt` + `temperature` + `max_tokens`
2. 忽略 `stream` 参数（缓存原始结果）
3. 忽略 `request_id` 等唯一标识

**命中缓存时**：
- 直接返回缓存的响应
- 添加 `X-Cache: HIT` 响应头
- 记录缓存命中率指标

---

### 3.2 KV Cache 复用（缺失）

**现状**：每次请求从头开始生成。

**需要实现的功能**：
- 对于相同 system prompt 的请求，复用已计算的 KV Cache
- 需要后端支持（llama.cpp 通过 `--context` 参数支持）

---

## 四、安全与认证

### 4.1 认证体系（仅基础实现）

**现状**：仅支持简单的 Bearer Token 认证，不支持 OAuth、API Key 轮换等。

**需要实现的功能**：

```yaml
auth:
  enabled: true
  providers:
    - type: "api_key"
      header: "X-API-Key"
      keys:
        - key: "${API_KEY_1}"
          user_id: "user_1"
          permissions: ["chat", "completions"]
          rate_limit: 100
        - key: "${API_KEY_2}"
          user_id: "user_2"
          permissions: ["chat"]
          rate_limit: 50
    - type: "jwt"
      secret: "${JWT_SECRET}"
      issuer: "proxy-llm"
      audience: "clients"
      expiration: 24h
    - type: "oauth2"
      provider: "github"  # github, google, oidc
      client_id: "${OAUTH_CLIENT_ID}"
      client_secret: "${OAUTH_CLIENT_SECRET}"
      scopes: ["openid", "profile", "email"]
  mfa:
    enabled: false
    provider: "totp"  # totp, sms
```

---

### 4.2 输入验证与过滤（缺失）

**现状**：请求体直接透传，无输入验证。

**需要实现的功能**：
- 消息长度限制（防止超长输入导致 OOM）
- 系统提示注入检测
- 恶意内容过滤（PII 识别、敏感词检测）
- JSON Schema 验证
- 最大消息数限制（防止上下文污染）

---

### 4.3 审计日志（部分缺失）

**现状**：请求/响应被记录到 JSONL，但无独立的安全审计日志。

**需要实现的功能**：
- 独立的安全事件日志（认证失败、越权访问、注入尝试）
- SIEM 集成（Syslog、HTTP 推送）
- 合规性报告生成（GDPR、HIPAA）

---

## 五、可观测性增强

### 5.1 分布式追踪（缺失）

**现状**：有 `request_id` 和 `call_id`，但未集成分布式追踪。

**需要实现的功能**：

```yaml
tracing:
  enabled: true
  provider: "opentelemetry"  # opentelemetry, jaeger, zipkin
  opentelemetry:
    endpoint: "http://otel-collector:4317"
    sampling_rate: 0.1
    resource_attributes:
      service.name: "proxy-llm"
      service.version: "1.0.0"
  span_attributes:
    include_request_id: true
    include_model: true
    include_tokens: true
```

**追踪维度**：
- 客户端 → 代理 → 上游的完整调用链
- 每个阶段的延迟分布
- 错误传播追踪

---

### 5.2 指标增强（部分缺失）

**现状**：基本的请求计数和延迟指标。

**需要补充的指标**：

| 指标类型 | 指标名称 | 说明 |
|----------|----------|------|
| 缓存 | `cache_hits_total` | 缓存命中次数 |
| 缓存 | `cache_misses_total` | 缓存未命中次数 |
| 缓存 | `cache_hit_ratio` | 缓存命中率 |
| 速率限制 | `rate_limit_rejected_total` | 被速率限制拒绝的请求数 |
| 重试 | `retries_total` | 重试次数 |
| 重试 | `retries_by_error_total` | 按错误类型统计的重试 |
| 并发 | `active_requests_gauge` | 当前活跃请求数 |
| 连接 | `upstream_connection_errors_total` | 上游连接错误数 |
| 流式 | `streaming_duration_seconds` | 流式响应持续时间 |
| 格式转换 | `format_conversion_errors_total` | 格式转换错误数 |

---

### 5.3 健康检查增强（部分缺失）

**现状**：仅返回 `OK` 字符串。

**需要实现的功能**：

```yaml
health:
  endpoints:
    - path: "/health"
      type: "liveness"
    - path: "/ready"
      type: "readiness"
    - path: "/health/verbose"
      type: "verbose"
  upstream:
    check_interval: 60s
    timeout: 10s
    failure_threshold: 3
    success_threshold: 2
  verbose:
    show_upstream_status: true
    show_cache_status: true
    show_rate_limit_status: true
```

**就绪检查**：
- 上游节点可用性
- 存储写入权限
- 缓存连接状态

---

## 六、流式处理增强

### 6.1 流式缓冲控制（缺失）

**现状**：SSE 流直接透传，无缓冲控制。

**需要实现的功能**：
- 流式响应缓冲大小控制
- 流式超时检测（长时间无数据自动断开）
- 流式错误恢复（网络中断后重连）
- 流式 chunk 批处理（减少网络往返）

---

### 6.2 流式预处理（缺失）

**现状**：流式数据直接转发。

**需要实现的功能**：
- 流式内容安全过滤（实时检测有害输出）
- 流式 Token 计数实时报告
- 流式响应头信息注入（如 `X-Stream-Status`）

---

## 七、数据存储增强

### 7.1 存储后端多样化（缺失）

**现状**：仅支持本地文件系统 JSONL 存储。

**需要实现的功能**：

```yaml
storage:
  backend: "filesystem"  # filesystem, s3, postgres, elasticsearch, mongodb
  filesystem:
    directory: "./data"
    compress: true
    rotate: "daily"
  s3:
    bucket: "llm-data"
    region: "us-east-1"
    prefix: "logs/"
    credentials:
      access_key: "${AWS_ACCESS_KEY_ID}"
      secret_key: "${AWS_SECRET_ACCESS_KEY}"
  elasticsearch:
    url: "http://localhost:9200"
    index: "proxy-llm-{date}"
    bulk_size: 1000
    flush_interval: 5s
  postgres:
    dsn: "${DATABASE_URL}"
    table: "request_logs"
    batch_size: 500
    flush_interval: 10s
```

---

### 7.2 数据生命周期管理（缺失）

**现状**：通过 `rotate` 按日期组织，但无自动清理。

**需要实现的功能**：
- 自动删除过期数据
- 数据归档到冷存储
- 数据压缩策略调整
- 存储配额告警

---

### 7.3 数据导出增强（部分缺失）

**现状**：每日导出为单一 JSONL 文件。

**需要实现的功能**：
- 实时导出到消息队列（Kafka、RabbitMQ）
- 增量导出（仅导出变更部分）
- 多格式导出（JSONL、Parquet、CSV）
- 导出过滤（按模型、时间、状态）
- 导出回调通知

---

## 八、模型管理增强

### 8.1 动态模型发现（缺失）

**现状**：模型配置需要在启动时静态配置。

**需要实现的功能**：
- 从上游 API 动态获取可用模型列表
- 模型热加载（无需重启）
- 模型健康状态自动检测
- 模型性能基线记录

---

### 8.2 模型路由策略（缺失）

**现状**：简单通过 `model` 字段匹配。

**需要实现的功能**：

```yaml
model_routing:
  enabled: true
  strategies:
    - name: "cost_optimized"
      description: "根据成本选择最优模型"
      rules:
        - condition: "prompt_tokens < 1000"
          prefer: "fast_cheaper_model"
        - condition: "requires_reasoning"
          prefer: "strong_reasoning_model"
    - name: "latency_optimized"
      description: "根据延迟要求选择模型"
      rules:
        - condition: "max_latency < 1s"
          prefer: "fastest_model"
  fallback:
    enabled: true
    default_model: "fallback_model"
    retry_on_failure: true
```

---

### 8.3 模型性能监控（缺失）

**现状**：无模型性能对比。

**需要实现的功能**：
- 响应时间分布统计（P50、P90、P99）
- 首 Token 延迟（TTFT）监控
- Token 生成速度（tokens/sec）
- 错误率趋势
- 模型质量评分（基于用户反馈）

---

## 九、插件与扩展系统

### 9.1 中间件系统（缺失）

**现状**：仅有 CORS 和认证中间件。

**需要实现的功能**：

```yaml
middleware:
  - name: "request_modifier"
    enabled: true
    config:
      add_headers:
        X-Proxy-Version: "1.0.0"
      modify_model:
        rules:
          - pattern: "gpt-4-turbo"
            replace: "gpt-4-1106-preview"
  - name: "response_transformer"
    enabled: true
    config:
      strip_fields: ["_meta", "debug_info"]
      add_fields:
        proxy_timestamp: "{now}"
  - name: "usage_analytics"
    enabled: true
    config:
      batch_size: 100
      flush_interval: 30s
      destination: "clickhouse"
  - name: "content_filter"
    enabled: true
    config:
      provider: "custom"  # custom, openai_moderation, together_ai
      model: "text-moderation-stable"
      action: "block"  # block, warn, replace
```

---

### 9.2 Hook 系统（缺失）

**现状**：无事件钩子。

**需要实现的功能**：
- `before_request`：请求前处理（修改、验证、缓存检查）
- `after_request`：请求后处理（日志、通知、缓存存储）
- `on_error`：错误处理钩子
- `on_stream_chunk`：流式分块处理钩子

---

## 十、Web UI 增强

### 10.1 当前仪表盘功能

**现状**：`/usage` 和 `/api/usage/summary` 提供基本的 Token 使用统计。

**需要补充的功能**：

| 功能 | 优先级 | 说明 |
|------|--------|------|
| 实时请求监控 | P0 | 显示当前正在处理的请求 |
| 请求详情查看 | P0 | 点击查看完整请求/响应 |
| 模型性能对比 | P1 | 不同模型的延迟/质量对比 |
| 费用趋势图 | P1 | 按时间维度的费用趋势 |
| 异常检测 | P1 | 自动检测异常请求模式 |
| 用户行为分析 | P2 | 不同用户的使用模式 |
| 模型推荐 | P2 | 基于使用场景推荐模型 |
| 请求回放 | P2 | 重新发送历史请求 |

---

## 十一、高可用与部署

### 11.1 多实例协调（缺失）

**现状**：单实例设计，无集群支持。

**需要实现的功能**：
- 共享配置同步（etcd、Consul）
- 分布式锁（协调定时任务）
- 共享存储后端
- 服务发现（Kubernetes、DNS）

---

### 11.2 Kubernetes 支持（缺失）

**现状**：无 K8s 原生支持。

**需要实现的功能**：
- Helm Chart 打包
- HPA（Horizontal Pod Autoscaler）配置
- Pod Disruption Budget
- Liveness/Readiness Probe
- Resource Limits/Requests
- Network Policies

---

### 11.3 配置热更新（缺失）

**现状**：配置修改需要重启服务。

**需要实现的功能**：
- 监听配置文件变更（inotify）
- 热加载模型配置
- 热更新日志级别
- 热更新速率限制

---

## 十二、性能优化

### 12.1 连接复用优化（部分缺失）

**现状**：每个模型创建独立的 HTTP Client，但无连接池监控。

**需要实现的功能**：
- HTTP/2 多路复用支持
- 连接池监控和告警
- 连接预热（启动时建立连接）
- 空闲连接回收策略

---

### 12.2 内存优化（部分缺失）

**现状**：请求体完整读入内存。

**需要实现的功能**：
- 流式请求体读取（减少内存峰值）
- 响应体分块处理
- 对象池复用（避免 GC 压力）
- 内存使用监控和告警

---

### 12.3 异步处理（部分缺失）

**现状**：日志写入为同步操作。

**需要实现的功能**：
- 异步日志写入（批量 + 异步）
- 后台数据导出
- 指标聚合后台任务
- 异步通知发送

---

## 十三、安全增强（续）

### 13.1 TLS 增强（部分缺失）

**现状**：支持 TLS 但配置简单。

**需要实现的功能**：
- mTLS（双向 TLS 认证）
- TLS 1.3 强制
- 证书自动轮换（Let's Encrypt）
- 证书透明度监控

---

### 13.2 数据加密（缺失）

**现状**：存储的数据无加密。

**需要实现的功能**：
- 静态数据加密（AES-256）
- 传输中数据加密（TLS）
- 密钥管理系统集成（HashiCorp Vault、AWS KMS）

---

## 十四、可移植性与标准化

### 14.1 OpenTelemetry 集成（缺失）

**现状**：无标准可观测性集成。

**需要实现的功能**：
- OpenTelemetry SDK 集成
- 标准指标导出（Prometheus、OTLP）
- 标准日志格式（JSON）
- 标准追踪上下文传播

---

### 14.2 API 标准化（部分完成）

**现状**：支持 OpenAI 和 Anthropic 格式。

**需要补充的功能**：
- Google Gemini API 格式支持
- AWS Bedrock 格式支持
- Azure OpenAI 格式支持
- 自定义格式转换插件

---

## 十五、功能优先级矩阵

| 功能模块 | 优先级 | 工作量估计 | 业务价值 |
|----------|--------|-----------|----------|
| 速率限制实现 | P0 | 中 | 高 |
| 响应缓存 | P0 | 高 | 高 |
| 分布式追踪 | P1 | 中 | 高 |
| 指标增强 | P1 | 低 | 高 |
| 输入验证 | P1 | 中 | 高 |
| 存储后端多样化 | P1 | 高 | 中 |
| 模型路由策略 | P2 | 高 | 中 |
| 插件系统 | P2 | 高 | 中 |
| 多实例协调 | P2 | 高 | 中 |
| Kubernetes 支持 | P2 | 中 | 中 |
| KV Cache 复用 | P3 | 高 | 低 |
| 流式预处理 | P3 | 中 | 低 |

---

## 附录：竞品功能对比

| 功能 | proxy-llm | LiteLLM | Ollama | vLLM | Open WebUI |
|------|-----------|---------|--------|------|------------|
| 多提供商支持 | ✅ | ✅ | ❌ | ❌ | ✅ |
| 格式转换 | ✅ | ✅ | ❌ | ❌ | ✅ |
| 流式处理 | ✅ | ✅ | ✅ | ✅ | ✅ |
| 速率限制 | ❌ | ✅ | ❌ | ❌ | ✅ |
| 响应缓存 | ❌ | ❌ | ✅ | ✅ | ✅ |
| 负载均衡 | ❌ | ✅ | ❌ | ✅ | ❌ |
| 分布式追踪 | ❌ | ✅ | ❌ | ❌ | ❌ |
| 认证体系 | 基础 | ✅ | ❌ | ❌ | ✅ |
| 数据导出 | ✅ | ✅ | ❌ | ❌ | ✅ |
| Web UI | 基础 | ❌ | ❌ | ❌ | ✅ |
| 插件系统 | ❌ | ✅ | ❌ | ❌ | ❌ |
| Kubernetes | ❌ | ✅ | ❌ | ✅ | ❌ |
