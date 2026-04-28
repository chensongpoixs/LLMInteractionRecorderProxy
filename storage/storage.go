/******************************************************************************
 *  Copyright (c) 2025 The LLM Interaction Recorder & Proxy — 大模型交互日志与数据沉淀代理 project authors. All Rights Reserved.
 *
 *  Please visit https://chensongpoixs.github.io for detail
 *
 *  Use of this source code is governed by a BSD-style license
 *  that can be found in the LICENSE file in the root of the source
 *  tree. An additional intellectual property rights grant can be found
 *  in the file PATENTS.  All contributing project authors may
 *  be found in the AUTHORS file in the root of the source tree.
 ******************************************************************************/
 /*****************************************************************************
                   Author: chensong
                   date:  2026-04-26
输赢不重要，答案对你们有什么意义才重要。

光阴者，百代之过客也，唯有奋力奔跑，方能生风起时，是时代造英雄，英雄存在于时代。或许世人道你轻狂，可你本就年少啊。 看护好，自己的理想和激情。


我可能会遇到很多的人，听他们讲好2多的故事，我来写成故事或编成歌，用我学来的各种乐器演奏它。
然后还可能在一个国家遇到一个心仪我的姑娘，她可能会被我帅气的外表捕获，又会被我深邃的内涵吸引，在某个下雨的夜晚，她会全身淋透然后要在我狭小的住处换身上的湿衣服。
3小时候后她告诉我她其实是这个国家的公主，她愿意向父皇求婚。我不得已告诉她我是穿越而来的男主角，我始终要回到自己的世界。
然后我的身影慢慢消失，我看到她眼里的泪水，心里却没有任何痛苦，我才知道，原来我的心被丢掉了，我游历全世界的原因，就是要找回自己的本心。
于是我开始有意寻找各种各样失去心的人，我变成一块砖头，一颗树，一滴水，一朵白云，去听大家为什么会失去自己的本心。
我发现，刚出生的宝宝，本心还在，慢慢的，他们的本心就会消失，收到了各种黑暗之光的侵蚀。
从一次争论，到嫉妒和悲愤，还有委屈和痛苦，我看到一只只无形的手，把他们的本心扯碎，蒙蔽，偷走，再也回不到主人都身边。
我叫他本心猎手。他可能是和宇宙同在的级别 但是我并不害怕，我仔细回忆自己平淡的一生 寻找本心猎手的痕迹。
沿着自己的回忆，一个个的场景忽闪而过，最后发现，我的本心，在我写代码的时候，会回来。
安静，淡然，代码就是我的一切，写代码就是我本心回归的最好方式，我还没找到本心猎手，但我相信，顺着这个线索，我一定能顺藤摸瓜，把他揪出来。

 ******************************************************************************/

package storage

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/**
 * @struct  MessageLog
 * @author  chensong
 * @date   2026-04-26
 * @brief   对话链中的单条消息记录
 *
 * @details
 * 表示一次对话中的单条消息，包含角色、文本内容、推理内容（reasoning）以及工具调用信息。
 * 该结构体用于在 RequestLog.Messages 字段中保存完整的对话上下文，便于后续对数据集
 * 进行分析、训练或评测。
 *
 * 角色类型说明：
 * - "system":    系统提示词，定义 AI 的行为和角色
 * - "user":      用户输入的提问或指令
 * - "assistant": 模型的回复，可能包含 reasoning 内容和工具调用
 * - "tool":      工具调用的执行结果，由外部工具/函数返回
 *
 * @field Role      string        消息角色 (system/user/assistant/tool)
 * @field Content   string        消息正文文本内容
 * @field Reasoning string        推理/思考内容 (仅在 assistant 角色且模型支持时存在)
 * @field ToolCalls []ToolCallLog 工具调用列表 (仅在 assistant 角色执行工具调用时存在)
 *
 * @note 对于 OpenAI 风格的请求，Reasoning 字段来源于 reasoning_content 或 reasoning 字段；
 *       对于 Anthropic 风格的请求，Reasoning 来源于 thinking 类型的 content block。
 */
type MessageLog struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	Reasoning string        `json:"reasoning,omitempty"`
	ToolCalls []ToolCallLog `json:"tool_calls,omitempty"`
}

/**
 * @struct  ToolCallLog
 * @author  chensong
 * @date   2026-04-26
 * @brief   工具调用日志记录
 *
 * @details
 * 记录模型中单次工具调用的详细信息，通常嵌套在 assistant 角色的消息中。
 * 当模型判断需要调用外部函数时（如搜索、计算、API 调用等），会生成一条或多条
 * ToolCallLog，每条包含调用的唯一标识、类型和具体的函数调用参数。
 *
 * @field ID       string         工具调用的唯一标识符 (由模型生成)
 * @field Type     string         工具调用类型 (通常为 "function")
 * @field Function FunctionCallLog 函数调用详细信息
 *
 * @note 工具调用上下文对于训练数据集非常重要，它记录了模型在何时、如何决定
 *       使用外部工具来辅助完成任务。
 */
type ToolCallLog struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function FunctionCallLog `json:"function"`
}

/**
 * @struct  FunctionCallLog
 * @author  chensong
 * @date   2026-04-26
 * @brief   函数调用详细信息
 *
 * @details
 * 记录工具调用中具体函数的信息，包括函数名称和传入的参数。
 * Arguments 以 JSON 字符串形式存储，可在后续处理中反序列化为具体的参数结构。
 *
 * @field Name      string 被调用的函数名称
 * @field Arguments string JSON 格式的函数参数字符串
 *
 * @note Arguments 字段存储的是原始 JSON 字符串而非解析后的对象，
 *       这保证了与各种 LLM API 格式的兼容性，避免因参数结构差异导致解析失败。
 */
type FunctionCallLog struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

/**
 * @struct  RequestLog
 * @author  chensong
 * @date   2026-04-26
 * @brief   单次请求/响应对的完整日志记录
 *
 * @details
 * RequestLog 是存储层的核心数据结构，记录了一次完整的 LLM API 调用从请求到响应的
 * 全过程。它不仅保存了原始的请求体和响应体，还提取了结构化的对话上下文信息
 * （系统提示词、消息链），以便后续进行数据集构建和分析。
 *
 * 数据流说明：
 * 1. 代理收到客户端请求 -> 解析请求体 -> 提取 Messages/SystemPrompt
 * 2. 转发请求到 LLM 服务商 -> 接收响应 -> 解析响应体 -> 提取 Token 用量
 * 3. 组装完整的 RequestLog -> 序列化为 JSONL -> 追加写入文件
 *
 * @field ID                string                 请求唯一标识 (UUID 格式)
 * @field Timestamp         time.Time              请求时间戳
 * @field SessionID         string                 会话标识，同一用户/对话窗口共享
 * @field ConversationID    string                 对话组标识，用于关联多轮对话所属的同一对话主题
 * @field TurnIndex         int                    当前对话在 Conversation 中的轮次序号 (从1开始)
 * @field Endpoint          string                 请求的 API 端点路径 (如 /v1/chat/completions)
 * @field Method            string                 HTTP 请求方法 (GET/POST)
 * @field Model             string                 模型名称 (如 gpt-4o, claude-sonnet-4-20250514)
 * @field Provider          string                 LLM 服务商名称 (如 openai, anthropic, deepseek)
 * @field SystemPrompt      string                 系统提示词内容
 * @field Messages          []MessageLog           提取后的消息链 (不含 system 消息)
 * @field RequestBody       map[string]interface{} 原始请求体 JSON
 * @field StatusCode        int                    HTTP 响应状态码
 * @field ResponseBody      map[string]interface{} 原始响应体 JSON (非流式) 或首个 chunk (流式)
 * @field AggregatedResponse map[string]interface{} 流式响应聚合后的完整响应体 (仅流式请求)
 * @field Stream            bool                   是否为流式请求
 * @field Duration          string                 请求耗时 (如 "1.523s")
 * @field Error             string                 错误信息 (仅在请求失败时存在)
 * @field TokensUsed        map[string]int         Token 用量统计 (prompt_tokens/completion_tokens/total_tokens)
 *
 * @note
 * 1. SystemPrompt 字段从 Messages 中提取后单独存储，Messages 列表中不再包含 system 角色的消息，
 *    这是为了便于后续构建数据集时的 id/problem/thinking/solution 四元组格式。
 * 2. AggregatedResponse 仅在流式请求场景下填充，它将所有 SSE chunk 的 delta 内容聚合为
 *    完整的响应体，便于一次性读取完整回复。
 * 3. 非流式请求中 ResponseBody 直接包含完整响应；流式请求中 ResponseBody 保存的是
 *    第一个 chunk 的快照，完整响应在 AggregatedResponse 中。
 */
type RequestLog struct {
	ID                 string                 `json:"id"`
	Timestamp          time.Time              `json:"timestamp"`
	SessionID          string                 `json:"session_id"`
	ConversationID     string                 `json:"conversation_id,omitempty"`
	TurnIndex          int                    `json:"turn_index,omitempty"`
	Endpoint           string                 `json:"endpoint"`
	Method             string                 `json:"method"`
	Model              string                 `json:"model"`
	Provider           string                 `json:"provider"`
	SystemPrompt       string                 `json:"system_prompt,omitempty"`
	Messages           []MessageLog           `json:"messages,omitempty"`
	RequestBody        map[string]interface{} `json:"request_body"`
	StatusCode         int                    `json:"status_code,omitempty"`
	ResponseBody       map[string]interface{} `json:"response_body,omitempty"`
	AggregatedResponse map[string]interface{} `json:"aggregated_response,omitempty"`
	Stream             bool                   `json:"stream"`
	Duration           string                 `json:"duration"`
	Error              string                 `json:"error,omitempty"`
	TokensUsed         map[string]int         `json:"tokens_used,omitempty"`
}

/**
 * @struct  ResponseStream
 * @author  chensong
 * @date   2026-04-26
 * @brief   流式响应的单个数据块记录
 *
 * @details
 * 记录 SSE（Server-Sent Events）流式响应中的每一个 chunk 数据块。
 * 当 LLM 以流式方式返回响应时，内容被分为多个小块依次发送，每个块被记录为
 * 一条 ResponseStream。这些记录存储在专门的 streams 子目录中，用于：
 * 1. 还原完整的流式响应过程
 * 2. 分析流式响应的时序特性
 * 3. 调试流式传输中的异常情况
 *
 * @field ID        string    数据块所属请求的唯一标识
 * @field Chunk     string    数据块的文本内容 (SSE data 字段的原始值)
 * @field Timestamp time.Time 数据块生成的时间戳
 * @field SessionID string    所属会话的标识
 * @field Index     int       数据块在流中的序号 (从 0 开始递增)
 *
 * @note Chunk 字段保存的是 SSE 事件中的 data 字段内容，包含完整的 JSON 字符串，
 *       而不是仅提取 delta 文本。这保证了原始数据的完整性，便于后续回放分析。
 */
type ResponseStream struct {
	ID        string    `json:"id"`
	Chunk     string    `json:"chunk"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	Index     int       `json:"index"`
}

/**
 * @struct  Logger
 * @author  chensong
 * @date   2026-04-26
 * @brief   文件日志记录器，负责请求/响应数据的持久化
 *
 * @details
 * Logger 是存储模块的核心组件，封装了所有文件 I/O 操作。它管理存储目录结构，
 * 提供线程安全的文件写入接口，并支持 gzip 压缩和文件轮转策略。
 *
 * 并发安全设计：
 * Logger 使用 sync.RWMutex 保护所有文件写入操作，确保多个 goroutine 同时
 * 调用 SaveRequest/SaveStreamChunk 时不会产生数据竞争或文件损坏。同时，
 * JSON 序列化和 gzip 压缩等 CPU 密集型操作在锁外完成，以减少锁持有时间，
 * 提高并发吞吐量。
 *
 * 文件命名规则：{会话ID}_{日期YYYYMMDD}.jsonl[.gz]
 * 例如：session_gpt4o_20260425.jsonl.gz
 *
 * @field mu      sync.RWMutex 读写锁，保护文件写入操作的并发安全
 * @field dir     string       日志文件存储根目录 (如 ./data)
 * @field format  string       日志格式 (保留字段，当前固定为 JSONL)
 * @field rotate  string       轮转策略 (保留字段，如 "daily")
 * @field maxSize string       文件最大大小 (保留字段，如 "100MB")
 * @field gzip    bool         是否启用 gzip 压缩存储
 *
 * @note
 * 1. format/rotate/maxSize 字段目前已保留但未实现完整的轮转逻辑，
 *    当前默认按日期分目录进行组织，这是一种隐式的日轮转策略。
 * 2. gzip 压缩模式下文件扩展名为 .jsonl.gz，使用 BestCompression 级别。
 * 3. ReadLog 方法不持有互斥锁，读取到的内容可能与正在写入的数据存在短暂不一致，
 *    这在使用量统计仪表盘等非严格一致性场景下是可接受的。
 */
type Logger struct {
	mu      sync.RWMutex
	dir     string
	format  string
	rotate  string
	maxSize string
	gzip    bool
}

/**
 * @function NewLogger
 * @author   chensong
 * @date   2026-04-26
 * @brief    创建并初始化一个新的文件日志记录器
 *
 * @details
 * 工厂函数，用于创建 Logger 实例。执行以下初始化步骤：
 * 1. 检查并创建主存储目录（如果不存在），使用 0755 权限
 * 2. 检查并创建 streams 子目录，用于存储流式响应数据
 * 3. 构造并返回 Logger 实例，保留配置参数供后续使用
 *
 * @param dir       string 日志存储根目录路径
 * @param format    string 日志格式标识（保留字段，当前固定为 JSONL）
 * @param rotate    string 轮转策略（保留字段，如 "daily"）
 * @param maxSize   string 文件最大大小限制（保留字段，如 "100MB"）
 * @param compress  bool   是否启用 gzip 压缩
 *
 * @return *Logger 初始化完成的 Logger 实例
 * @return error   目录创建失败时返回错误信息，成功时返回 nil
 *
 * @note
 * 1. os.MkdirAll 支持递归创建目录，如果目录已存在也不会报错。
 * 2. 返回的 Logger 实例可以安全地在多个 goroutine 中并发使用。
 */
func NewLogger(dir, format, rotate, maxSize string, compress bool) (*Logger, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	// Create streams subdirectory
	if err := os.MkdirAll(filepath.Join(dir, "streams"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create streams directory: %w", err)
	}

	return &Logger{
		dir:     dir,
		format:  format,
		rotate:  rotate,
		maxSize: maxSize,
		gzip:    compress,
	}, nil
}

/**
 * @function Logger.SaveRequest
 * @author   chensong
 * @date   2026-04-26
 * @brief    将单次请求/响应日志持久化保存到文件
 *
 * @details
 * 将 RequestLog 结构体序列化为 JSON 并追加写入对应日期的日志文件中。
 * 完整处理流程如下：
 *
 * 1. 文件名构建：调用 buildFileName 方法，根据时间戳和会话 ID 生成目标文件路径，
 *    自动按日期分目录（如 data/20260425/session_xxx_20260425.jsonl）
 * 2. 序列化：使用 json.Marshal 将 RequestLog 序列化为 JSON 字节数组
 * 3. 压缩（可选）：如果启用了 gzip，在锁外执行 BestCompression 级别的 gzip 压缩
 * 4. 获取写锁：调用 l.mu.Lock() 获取互斥锁，确保文件写入的原子性
 * 5. 文件写入：以追加模式打开文件（O_CREATE|O_WRONLY|O_APPEND），写入数据后关闭
 * 6. 释放写锁：使用 defer 模式确保在任何错误路径下都能正确释放锁
 *
 * 性能优化说明：
 * - JSON 序列化和 gzip 压缩在锁外执行，避免阻塞其他并发写入者
 * - 文件以追加模式写入，使用操作系统的页缓存机制
 *
 * @param req *RequestLog 待保存的请求/响应日志记录
 *
 * @return error 序列化或文件写入失败时返回错误信息，成功时返回 nil
 *
 * @note
 * 1. 每次调用 SaveRequest 会写入一行 JSON（JSONL 格式），
 *    如果写入失败则整个请求日志丢失，不会重试。
 * 2. 在函数返回前确保锁已释放（在 OpenFile 失败时通过显式 Unlock 释放，
 *    Write 操作后通过结构化的 lock-write-close-unlock 流程保证）。
 * 3. 文件权限为 0644（owner 读写，group 和其他用户只读）。
 */
func (l *Logger) SaveRequest(req *RequestLog) error {
	fileName := l.buildFileName(req)

	// Marshal and optionally compress outside the lock to avoid
	// blocking concurrent callers during CPU-heavy work.
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request log: %w", err)
	}

	var payload []byte
	if l.gzip {
		var buf bytes.Buffer
		writer, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return err
		}
		writer.Write(append(data, '\n'))
		writer.Close()
		payload = buf.Bytes()
	} else {
		payload = append(data, '\n')
	}

	l.mu.Lock()
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		l.mu.Unlock()
		return fmt.Errorf("failed to open log file: %w", err)
	}
	_, err = file.Write(payload)
	file.Close()
	l.mu.Unlock()
	return err
}

/**
 * @function Logger.SaveStreamChunk
 * @author   chensong
 * @date   2026-04-26
 * @brief    保存流式响应的单个数据块到文件
 *
 * @details
 * 将 SSE 流式响应的单个 chunk 持久化到 streams 子目录中。
 * 完整处理流程如下：
 *
 * 1. 日期提取：从 chunk.Timestamp 提取日期字符串（YYYYMMDD 格式），
 *    如果时间戳为零值则使用当前时间兜底
 * 2. 目录创建：在 streams/日期/ 路径下创建子目录（如 data/20260425/streams/），
 *    使用 os.MkdirAll 确保递归创建路径上所有不存在的目录
 * 3. 会话 ID 兜底：如果 chunk.SessionID 为空，使用 "default" 作为默认值
 * 4. 文件名生成：格式为 {sessionID}_{dateStr}.jsonl
 * 5. 序列化：在锁外将 ResponseStream 序列化为 JSON，追加换行符
 * 6. 获取写锁：调用 l.mu.Lock() 保护文件写入操作
 * 7. 文件写入：以创建+只写+追加模式打开文件，写入 payload 后关闭
 * 8. 释放写锁：调用 l.mu.Unlock()
 *
 * @param chunk *ResponseStream 待保存的流式响应数据块
 *
 * @return error 目录创建失败、序列化失败或文件写入失败时返回错误信息
 *
 * @note
 * 1. 流式 chunks 保存在与主日志分开的 streams/ 子目录中，避免大量碎片化
 *    的 chunk 数据干扰主要请求/响应日志的检索。
 * 2. 流式 chunk 文件不启用 gzip 压缩（与主日志不同），因为流式数据
 *    通常具有更高的写入频率，gzip 全文件压缩不适合追加写入模式。
 * 3. 零值时间戳 (0001-01-01) 会被替换为当前时间，避免生成不正确的日期目录。
 */
func (l *Logger) SaveStreamChunk(chunk *ResponseStream) error {
	dateStr := chunk.Timestamp.Format("20060102")
	if dateStr == "00010101" {
		dateStr = time.Now().Format("20060102")
	}
	streamDir := filepath.Join(l.dir, dateStr, "streams")
	if err := os.MkdirAll(streamDir, 0755); err != nil {
		return err
	}

	sessionID := chunk.SessionID
	if sessionID == "" {
		sessionID = "default"
	}
	fileName := filepath.Join(streamDir, fmt.Sprintf("%s_%s", sessionID, dateStr)) + ".jsonl"

	// Marshal outside the lock.
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	payload := append(data, '\n')

	l.mu.Lock()
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		l.mu.Unlock()
		return err
	}
	_, err = file.Write(payload)
	file.Close()
	l.mu.Unlock()
	return err
}

/**
 * @function Logger.ListLogs
 * @author   chensong
 * @date   2026-04-26
 * @brief    列出存储目录中匹配条件的日志文件列表
 *
 * @details
 * 递归遍历存储根目录，查找所有 .jsonl 和 .jsonl.gz 文件，支持按会话 ID 和
 * 日期进行过滤。完整处理流程如下：
 *
 * 1. 目录遍历：使用 filepath.WalkDir 递归遍历存储根目录下的所有文件
 * 2. 扩展名过滤：只保留 .jsonl 和 .gz 扩展名的文件，
 *    同时排除不以 .jsonl.gz 结尾的 .gz 文件（如 .tar.gz 等非日志文件）
 * 3. 会话 ID 过滤（可选）：如果指定了 sessionID，检查文件名（不含路径）
 *    是否包含该 ID 字符串（使用 strings.Contains 模糊匹配）
 * 4. 日期过滤（可选）：如果指定了 date，检查完整路径（转为正斜杠格式）
 *    是否包含该日期字符串
 * 5. 结果收集：将匹配的文件路径追加到结果切片中
 *
 * @param sessionID string 会话 ID 过滤条件，为空表示不过滤
 * @param date      string 日期过滤条件（如 "20260425"），为空表示不过滤
 *
 * @return []string 匹配的日志文件路径列表
 * @return error    目录遍历失败时返回错误信息
 *
 * @note
 * 1. 过滤条件为模糊匹配（Contains），而非精确匹配，这给了用户更大的灵活性，
 *    例如可以通过部分会话 ID 或部分日期进行搜索。
 * 2. WalkDir 回调函数中的 path 参数总是使用操作系统默认的分隔符，
 *    通过 filepath.ToSlash 统一为正斜杠格式以支持跨平台日期匹配。
 * 3. 如果遍历过程中遇到访问权限错误，WalkDir 会在回调中传递 walkErr，
 *    此时直接返回错误终止遍历。
 */
func (l *Logger) ListLogs(sessionID, date string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(l.dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		if ext != ".jsonl" && ext != ".gz" {
			return nil
		}
		if ext == ".gz" && !strings.HasSuffix(path, ".jsonl.gz") {
			return nil
		}

		base := filepath.Base(path)
		full := filepath.ToSlash(path)
		if sessionID != "" && !strings.Contains(base, sessionID) {
			return nil
		}
		if date != "" && !strings.Contains(full, date) {
			return nil
		}

		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

/**
 * @function Logger.ReadLog
 * @author   chensong
 * @date   2026-04-26
 * @brief    读取并解析日志文件中的所有 JSONL 条目
 *
 * @details
 * 从指定路径读取日志文件，自动处理 gzip 压缩格式，逐行解析 JSONL 并返回
 * 原始 JSON 消息数组。完整处理流程如下：
 *
 * 1. 文件读取：使用 os.ReadFile 将整个文件内容读入内存
 * 2. gzip 解压（条件性）：如果文件扩展名为 .gz，使用 gzip.NewReader 解压数据
 * 3. 逐行扫描：使用 bufio.Scanner 按行扫描文件内容
 *    - 设置自定义缓冲区：初始容量 128KB，最大容量 2MB
 *    - 支持处理超长行（多轮对话日志单行可能超过 600KB）
 * 4. JSON 验证：对每一行执行 json.Unmarshal 验证是否为有效的 JSON，
 *    有效的 JSON 条目追加到结果切片中，无效的行静默跳过
 * 5. 返回结果：返回包含所有有效 JSON 条目的切片
 *
 * @param filePath string 日志文件的完整路径（支持 .jsonl 和 .jsonl.gz）
 *
 * @return []json.RawMessage 解析后的 JSON 原始消息数组，每条消息为一行 JSONL 的完整 JSON
 * @return error             文件不存在、读取失败或 gzip 解压失败时返回错误信息
 *
 * @note
 * 1. ReadLog 不持有 Logger 的互斥锁，因此在读取过程中可能看到不完整的数据
 *   （如果同时有 SaveRequest 正在写入）。对于使用量仪表盘等非严格一致性场景，
 *   这种设计是可以接受的，因为它避免了读锁阻塞写操作。
 * 2. bufio.Scanner 的内存分配策略：
 *    - 默认最大行长度为 64KB（MaxScanTokenSize）
 *    - 通过自定义 Buffer 将最大行长度扩展到 2MB，以支持包含大量消息的
 *      长对话链（多轮对话可超过 600KB/行）
 * 3. JSON 解析失败的行被静默跳过而不报错，这意味着部分损坏的数据不会
 *    阻止其余有效数据的读取，但也不会向调用者报告数据质量问题。
 * 4. 对于 gzip 文件，必须先读取全部压缩数据再解压，此方法会将整个文件
 *    加载到内存中，超大文件（GB 级别）可能导致内存压力。
 */
func (l *Logger) ReadLog(filePath string) ([]json.RawMessage, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Handle gzipped files
	if filepath.Ext(filePath) == ".gz" {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		data, err = io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
	}

	var entries []json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Split(bufio.ScanLines)
	// Set a larger buffer to handle long lines (default 64KB max is too small
	// for multi-turn conversation logs that can exceed 600KB per line).
	buf := make([]byte, 0, 128*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) > 0 {
			var entry json.RawMessage
			if err := json.Unmarshal(line, &entry); err == nil {
				entries = append(entries, entry)
			}
		}
	}

	return entries, nil
}

/**
 * @function ExtractMessagesFromRequest
 * @author   chensong
 * @date   2026-04-26
 * @brief    从请求体中提取结构化的消息链、系统提示词和清洗后的请求体
 *
 * @details
 * 解析 OpenAI 风格和 Anthropic 风格的 LLM API 请求体，提取以下三类信息：
 * 1. MessageLog 数组：去掉 system 消息后的完整对话链，包含 reasoning 和 tool_calls
 * 2. systemPrompt 字符串：系统提示词（从消息链中提取后单独返回）
 * 3. cleanedBody map：原始请求体的副本，移除了 messages 字段
 *
 * 完整处理流程如下：
 *
 * 第一步 - 预处理：
 *   - 检查 reqBody 是否为 nil，如果为空则直接返回空值
 *   - 创建 reqBody 的浅拷贝 cleanedBody，避免修改原始数据
 *
 * 第二步 - OpenAI 风格解析：
 *   - 提取 reqBody["messages"] 数组
 *   - 遍历每条消息：提取 role 和 content（通过 extractTextContent 处理多种格式）
 *   - 对于 assistant 角色：提取 reasoning_content/reasoning 字段作为思考内容
 *   - 对于 assistant 角色：提取 tool_calls 数组，构建 ToolCallLog 列表
 *   - 对于 system 角色：将内容保存到 systemPrompt，不加入 messages 列表
 *   - 从 cleanedBody 中删除 messages 字段
 *
 * 第三步 - Anthropic 风格 system 字段提取（如果 systemPrompt 仍为空）：
 *   - 尝试读取 reqBody["system"] 字符串字段
 *
 * 第四步 - Anthropic 风格 content blocks 解析（如果第二步未解析到任何消息）：
 *   - 使用 extractAnthropicTextContent 从 content blocks 中提取文本
 *   - 对于 assistant 角色：使用 extractAnthropicThinkingContent 提取 thinking 内容
 *
 * @param reqBody map[string]interface{} 原始请求体的 JSON 解析结果
 *
 * @return messages     []MessageLog           提取后的消息链 (不含 system 消息)
 * @return systemPrompt string                 系统提示词文本
 * @return cleanedBody  map[string]interface{} 移除了 messages 字段的请求体副本
 *
 * @note
 * 1. 该函数会先尝试 OpenAI 风格解析（messages 数组中的 content 是字符串或 content parts 数组），
 *    如果未解析到消息，再回退到 Anthropic 风格解析（content blocks 中的 text/thinking 类型块）。
 * 2. reasonin_content 和 reasoning 字段名都支持（兼容不同模型/API 的命名差异）。
 * 3. 返回的 cleanedBody 是 reqBody 的浅拷贝，内部嵌套的 map/slice 仍然共享底层数据，
 *    但删除了顶层的 messages 键。
 * 4. 该函数不对原始请求体做任何修改，所有修改发生在 cleanedBody 副本上。
 * 5. tool_calls 中的 function 对象如果不是 map 类型（如 nil），tool call 的 Function 字段保持零值。
 */
func ExtractMessagesFromRequest(reqBody map[string]interface{}) (messages []MessageLog, systemPrompt string, cleanedBody map[string]interface{}) {
	if reqBody == nil {
		return nil, "", nil
	}

	// Copy to avoid mutating the original
	cleanedBody = make(map[string]interface{}, len(reqBody))
	for k, v := range reqBody {
		cleanedBody[k] = v
	}

	// Try OpenAI-style: request.messages[]
	if msgsRaw, exists := reqBody["messages"]; exists {
		if msgsArr, ok := msgsRaw.([]interface{}); ok {
			messages = make([]MessageLog, 0, len(msgsArr))
			for _, m := range msgsArr {
				msg, ok := m.(map[string]interface{})
				if !ok {
					continue
				}
				role, _ := msg["role"].(string)
				content := extractTextContent(msg["content"])

				ml := MessageLog{Role: role, Content: content}

				// Extract reasoning/thinking from assistant messages
				if role == "assistant" {
					if rc, ok := msg["reasoning_content"].(string); ok && rc != "" {
						ml.Reasoning = rc
					} else if rc, ok := msg["reasoning"].(string); ok && rc != "" {
						ml.Reasoning = rc
					}
					// Extract tool calls
					if tcRaw, exists := msg["tool_calls"]; exists {
						if tcArr, ok := tcRaw.([]interface{}); ok {
							toolCalls := make([]ToolCallLog, 0, len(tcArr))
							for _, tc := range tcArr {
								tcMap, ok := tc.(map[string]interface{})
								if !ok {
									continue
								}
								tcl := ToolCallLog{
									ID:   getStringField(tcMap, "id"),
									Type: getStringField(tcMap, "type"),
								}
								if fnRaw, exists := tcMap["function"]; exists {
									if fnMap, ok := fnRaw.(map[string]interface{}); ok {
										tcl.Function = FunctionCallLog{
											Name:      getStringField(fnMap, "name"),
											Arguments: getStringField(fnMap, "arguments"),
										}
									}
								}
								toolCalls = append(toolCalls, tcl)
							}
							ml.ToolCalls = toolCalls
						}
					}
				}

				if role == "system" && systemPrompt == "" {
					systemPrompt = content
					continue // don't add system to messages chain
				}
				messages = append(messages, ml)
			}
			delete(cleanedBody, "messages")
		}
	}

	// Try Anthropic-style: system field (could be string or content block)
	if systemPrompt == "" {
		if sys, ok := reqBody["system"].(string); ok && sys != "" {
			systemPrompt = sys
		}
	}

	// Try Anthropic-style: request.messages[] with content blocks
	if len(messages) == 0 {
		if msgsRaw, exists := reqBody["messages"]; exists {
			if msgsArr, ok := msgsRaw.([]interface{}); ok {
				messages = make([]MessageLog, 0, len(msgsArr))
				for _, m := range msgsArr {
					msg, ok := m.(map[string]interface{})
					if !ok {
						continue
					}
					role, _ := msg["role"].(string)
					content := extractAnthropicTextContent(msg["content"])
					ml := MessageLog{Role: role, Content: content}

					if role == "assistant" {
						thinking := extractAnthropicThinkingContent(msg["content"])
						if thinking != "" {
							ml.Reasoning = thinking
						}
					}
					messages = append(messages, ml)
				}
			}
		}
	}

	return messages, systemPrompt, cleanedBody
}

/**
 * @function extractTextContent
 * @author   chensong
 * @date   2026-04-26
 * @brief    从多种 content 字段格式中提取纯文本内容
 *
 * @details
 * 处理 OpenAI 风格的 content 字段，该字段可能是以下三种类型之一：
 * 1. 字符串：直接返回字符串内容
 * 2. 数组（content parts）：遍历数组，提取 type 为 "text" 或空字符串的块的 text 字段，
 *    用换行符拼接后返回
 * 3. 其他类型：返回空字符串
 *
 * 对于数组类型（content parts），典型的格式如下：
 * @code
 *   [
 *     {"type": "text", "text": "Hello, world!"},
 *     {"type": "image_url", "image_url": {"url": "https://..."}}
 *   ]
 * @endcode
 * 本函数只会提取 type 为 "text" 或 type 为空（向后兼容）的块中的 text 字段内容。
 *
 * @param content interface{} 可能是字符串、[]interface{} 或其他类型的 content 字段值
 *
 * @return string 提取到的纯文本内容，多个文本块用换行符拼接
 *
 * @note
 * 1. type 为空字符串的块也被当作文本处理，这是为了兼容某些不规范的 API 实现。
 * 2. 非文本类型的块（如 image_url）会被静默忽略。
 * 3. 该函数是非导出函数（小写开头），仅在本包内使用。
 */
func extractTextContent(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, item := range c {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType != "text" && blockType != "" {
				continue
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

/**
 * @function extractAnthropicTextContent
 * @author   chensong
 * @date   2026-04-26
 * @brief    从 Anthropic 风格的 content blocks 中提取纯文本内容
 *
 * @details
 * 处理 Anthropic Messages API 的 content 字段格式，该字段可能是：
 * 1. 字符串：直接返回
 * 2. Content blocks 数组：遍历数组，提取 type 为 "text" 或空字符串且 text 非空的块，
 *    用换行符拼接后返回
 * 3. 其他类型：返回空字符串
 *
 * Anthropic content blocks 的典型格式如下：
 * @code
 *   [
 *     {"type": "text", "text": "Hello!"},
 *     {"type": "tool_use", "id": "tool_001", "name": "get_weather", "input": {...}},
 *     {"type": "thinking", "thinking": "Let me think..."}
 *   ]
 * @endcode
 * 本函数只提取 type 为 "text" 的块；thinking 和 tool_use 类型的块由其他函数处理。
 *
 * 与 extractTextContent 的差异：
 * - 额外检查 text 字段去除空白后是否为空，跳过空白内容块
 * - 对于未被识别类型的块（type 为空），同样放行作为文本处理
 *
 * @param content interface{} 可能是字符串、Content Block 数组或其他类型的 content 字段值
 *
 * @return string 提取到的纯文本内容
 */
func extractAnthropicTextContent(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, item := range c {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType != "text" && blockType != "" {
				continue
			}
			if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

/**
 * @function extractAnthropicThinkingContent
 * @author   chensong
 * @date   2026-04-26
 * @brief    从 Anthropic content blocks 中提取 thinking（思考）内容
 *
 * @details
 * 专门处理 Anthropic Messages API 响应中的 thinking 类型 content block。
 * 遍历 content 数组，查找 type 为 "thinking" 的块，提取其 thinking 或 text 字段的内容：
 *
 * 1. 类型检查：如果 content 不是 []interface{} 类型，直接返回空字符串
 * 2. 遍历 blocks：对每个块检查 type 是否为 "thinking"
 * 3. 内容提取：优先读取 thinking 字段，如果为空则回退读取 text 字段
 * 4. 拼接：所有 thinking 块的内容用换行符拼接后返回
 *
 * 典型的 thinking block 格式：
 * @code
 *   {"type": "thinking", "thinking": "I need to analyze the user's question..."}
 * @endcode
 *
 * 与 extractAnthropicTextContent 的差异：
 * - 仅处理 type == "thinking" 的块（而非 type == "text"）
 * - 优先提取 "thinking" 键（而非 "text" 键），兼容个别实现中使用 "text" 键的情况
 *
 * @param content interface{} Content blocks 数组（[]interface{}）
 *
 * @return string 提取到的思考内容文本
 *
 * @note
 * 1. 如果 content 不是数组类型，直接返回空字符串而不报错。
 * 2. thinking 和 text 字段都尝试读取，增加了对不同 API 版本的兼容性。
 * 3. 空白的 thinking 内容会被跳过（通过 strings.TrimSpace 检查）。
 */
func extractAnthropicThinkingContent(content interface{}) string {
	blocks, ok := content.([]interface{})
	if !ok {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		block, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		if blockType != "thinking" {
			continue
		}
		if text, ok := block["thinking"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		} else if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

/**
 * @function getStringField
 * @author   chensong
 * @date   2026-04-26
 * @brief    安全地从 map[string]interface{} 中提取字符串字段
 *
 * @details
 * 封装了从任意 JSON 解析后的 map 中安全读取字符串字段的逻辑：
 * 1. 检查 key 是否存在于 map 中
 * 2. 类型断言检查该值是否为 string 类型
 * 3. 如果任一检查失败，返回空字符串而不触发 panic
 *
 * 使用此函数可以避免手动进行两次检查：
 * @code
 *   v, ok := m["key"]; if ok { s, ok := v.(string); if ok { return s } }
 * @endcode
 *
 * @param m   map[string]interface{} 源 map
 * @param key string                 要提取的键名
 *
 * @return string 提取到的字符串值，如果键不存在或类型不匹配则返回空字符串
 *
 * @note
 * 该函数安全且简洁，不会因类型断言失败而 panic。
 */
func getStringField(m map[string]interface{}, key string) string {
	if v, exists := m[key]; exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

/**
 * @function Logger.buildFileName
 * @author   chensong
 * @date   2026-04-26
 * @brief    根据请求日志生成目标日志文件路径
 *
 * @details
 * 根据 RequestLog 中的时间戳和会话 ID 构建日志文件的完整路径。
 * 完整处理流程如下：
 *
 * 1. 日期提取：从 req.Timestamp 提取日期字符串（YYYYMMDD 格式），
 *    如果时间戳为零值（0001-01-01），使用当前时间兜底
 * 2. 目录创建：在存储根目录下创建按日期命名的子目录（如 data/20260425/），
 *    如果目录创建失败则回退到 "default" 目录
 * 3. 会话 ID 兜底：如果 req.SessionID 为空，使用 "default" 作为默认值
 * 4. 扩展名选择：根据 gzip 配置选择 .jsonl 或 .jsonl.gz 作为文件扩展名
 * 5. 路径拼接：格式为 {storageDir}/{dateDir}/{sessionID}_{dateStr}.{ext}
 *
 * 生成的文件路径示例：
 * - 未压缩: ./data/20260425/session_gpt4o_20260425.jsonl
 * - 压缩后: ./data/20260425/session_gpt4o_20260425.jsonl.gz
 *
 * @param req *RequestLog 请求日志记录（主要使用 Timestamp 和 SessionID 字段）
 *
 * @return string 生成的完整文件路径
 *
 * @note
 * 1. 该方法是 Logger 的私有方法（小写开头），仅供 SaveRequest 内部调用。
 * 2. 零值时间戳的检测基于 Go 的 time.Time 零值 "0001-01-01 00:00:00 +0000 UTC"，
 *    格式化为 "20060102" 后等于 "00010101"。
 * 3. MkdirAll 失败后的回退机制确保了即使无法创建日期目录，数据也不会丢失，
 *    而是写入到 "default" 目录中。
 * 4. 文件名包含日期后缀，即使移动到其他路径也能根据文件名识别数据所属日期。
 */
func (l *Logger) buildFileName(req *RequestLog) string {
	dateStr := req.Timestamp.Format("20060102")
	if dateStr == "00010101" {
		dateStr = time.Now().Format("20060102")
	}

	// Store logs by date directory for easier investigation and retention.
	dir := filepath.Join(l.dir, dateStr)
	if err := os.MkdirAll(dir, 0755); err != nil {
		dir = filepath.Join(l.dir, "default")
		os.MkdirAll(dir, 0755)
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "default"
	}

	extension := ".jsonl"
	if l.gzip {
		extension = ".jsonl.gz"
	}

	return filepath.Join(dir, fmt.Sprintf("%s_%s%s", sessionID, dateStr, extension))
}
