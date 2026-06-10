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

package proxy

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"proxy-llm/config"
	"proxy-llm/exporter"
	"proxy-llm/logger"
	"proxy-llm/storage"
)

// Handler 提供代理服务的 HTTP 请求处理器集合，封装了所有 HTTP 端点的处理逻辑。
//
// 包含以下能力：
//   - 基础管理：健康检查、日志文件浏览/读取
//   - 指标采集：请求计数、错误计数、流量字节统计（使用原子操作保证并发安全）
//   - 中间件：HTTP 请求日志记录与指标上报
//   - 用量统计：聚合 Token 用量、模型维度统计、最近请求列表
//   - 提示词浏览：按日期分组浏览用户提示词，分页查询
//
// @author chensong
// @date   2026-04-26
// @brief HTTP 处理器总控结构体，聚合配置、存储、指标、日志等依赖组件
type Handler struct {
	config        *config.Config ///< 全局配置，包含服务器端口、存储目录、代理规则等
	storageLogger *storage.Logger ///< 存储层日志写入器，负责 JSONL 文件的读写操作
	metrics       *Metrics        ///< Prometheus 风格的请求指标采集器（指针，可能为 nil）
	appLogger     *logger.Logger  ///< 应用级结构化日志记录器

	requestCount   atomic.Int64 ///< 原子计数器，累计处理的请求总数（并发安全）
	errorCount     atomic.Int64 ///< 原子计数器，累计返回 4xx/5xx 的错误请求数（并发安全）
	totalBytesSent atomic.Int64 ///< 原子计数器，累计发送给客户端的响应字节数（并发安全）
}

// usageDailyPoint 表示单个日期的用量聚合数据点。
//
// 用于用量统计接口中 daily 字段，展示每日请求数、Token 消耗及费用估算。
//
// @author chensong
// @date   2026-04-26
// @brief 按日期维度的用量聚合数据
type usageDailyPoint struct {
	Date             string  `json:"date"`              ///< 日期字符串，格式 YYYY-MM-DD
	Requests         int     `json:"requests"`          ///< 当日请求总数
	PromptTokens     int64   `json:"prompt_tokens"`     ///< 当日输入 Token 总数
	CompletionTokens int64   `json:"completion_tokens"` ///< 当日输出 Token 总数
	TotalTokens      int64   `json:"total_tokens"`      ///< 当日 Token 总数（输入+输出）
	EstimatedCostUSD float64 `json:"estimated_cost_usd"` ///< 当日估算费用（美元），保留两位小数
}

// usageModelPoint 表示单个模型的用量聚合数据点。
//
// 用于用量统计接口中 by_model 字段，展示各模型的请求数、Token 消耗及费用估算，
// 按 TotalTokens 降序排列，方便识别消耗最大的模型。
//
// @author chensong
// @date   2026-04-26
// @brief 按模型维度的用量聚合数据
type usageModelPoint struct {
	Model            string  `json:"model"`             ///< 模型名称/标识（如 gpt-4o、claude-3.5-sonnet）
	Requests         int     `json:"requests"`          ///< 该模型的请求总数
	PromptTokens     int64   `json:"prompt_tokens"`     ///< 该模型的输入 Token 总数
	CompletionTokens int64   `json:"completion_tokens"` ///< 该模型的输出 Token 总数
	TotalTokens      int64   `json:"total_tokens"`      ///< 该模型的 Token 总数
	EstimatedCostUSD float64 `json:"estimated_cost_usd"` ///< 该模型的估算费用（美元），保留两位小数
}

// usageRecentItem 表示一条最近的请求记录摘要。
//
// 用于用量统计接口中 recent 字段，展示最近的请求明细，
// 最多返回 20 条，按时间戳降序排列。
//
// @author chensong
// @date   2026-04-26
// @brief 最近请求记录的摘要信息
type usageRecentItem struct {
	Timestamp        string  `json:"timestamp"`         ///< 请求时间戳，RFC3339 格式
	Model            string  `json:"model"`             ///< 使用的模型名称
	Endpoint         string  `json:"endpoint"`          ///< 请求的 API 端点路径
	StatusCode       int     `json:"status_code"`       ///< HTTP 响应状态码
	DurationMS       int64   `json:"duration_ms"`       ///< 请求耗时（毫秒）
	PromptTokens     int64   `json:"prompt_tokens"`     ///< 输入 Token 数量
	CompletionTokens int64   `json:"completion_tokens"` ///< 输出 Token 数量
	TotalTokens      int64   `json:"total_tokens"`      ///< Token 总数
	EstimatedCostUSD float64 `json:"estimated_cost_usd"` ///< 估算费用（美元），保留两位小数
}

// usageSummaryResponse 表示用量统计接口的完整响应结构。
//
// 包含汇总统计、按日聚合、按模型聚合、最近请求列表等维度的数据，
// 是 Usage Dashboard 前端页面的数据源。
//
// @author chensong
// @date   2026-04-26
// @brief 用量聚合统计的完整响应
type usageSummaryResponse struct {
	Days             int               `json:"days"`              ///< 统计天数范围
	GeneratedAt      string            `json:"generated_at"`      ///< 响应生成时间，RFC3339 格式
	TotalRequests    int               `json:"total_requests"`    ///< 统计范围内请求总数
	SuccessRequests  int               `json:"success_requests"`  ///< 成功请求数（状态码 1xx/2xx/3xx）
	SuccessRate      float64           `json:"success_rate"`      ///< 请求成功率（0.0~1.0），保留四位小数
	AvgLatencyMS     int64             `json:"avg_latency_ms"`    ///< 平均延迟（毫秒）
	PromptTokens     int64             `json:"prompt_tokens"`     ///< 输入 Token 总数
	CompletionTokens int64             `json:"completion_tokens"` ///< 输出 Token 总数
	TotalTokens      int64             `json:"total_tokens"`      ///< Token 总数
	EstimatedCostUSD float64           `json:"estimated_cost_usd"` ///< 总估算费用（美元），保留两位小数
	Daily            []usageDailyPoint `json:"daily"`             ///< 按日期维度聚合，按日期升序
	ByModel          []usageModelPoint `json:"by_model"`          ///< 按模型维度聚合，按 Token 数降序
	Recent           []usageRecentItem `json:"recent"`            ///< 最近请求记录，最多 20 条，按时间降序
}

// promptListItem 表示提示词列表中的单条记录。
//
// 包含请求 ID、时间戳、模型、语言、Token 数、提示词预览/全文、回复预览/全文。
// 预览字段截取前 200 个字符（按 rune 计数），方便列表展示。
//
// @author chensong
// @date   2026-04-26
// @brief 提示词列表数据项
type promptListItem struct {
	ID              string `json:"id"`               ///< 请求唯一标识
	Timestamp       string `json:"timestamp"`        ///< 请求时间，RFC3339 格式
	Model           string `json:"model"`            ///< 使用的模型名称
	Language        string `json:"language"`         ///< 检测到的提示词语种（如 zh、en）
	PromptTokens    int    `json:"prompt_tokens"`    ///< 输入 Token 数量
	TotalTokens     int    `json:"total_tokens"`     ///< Token 总数
	PromptPreview   string `json:"prompt_preview"`   ///< 提示词预览（截取前 200 字符）
	PromptFull      string `json:"prompt_full"`      ///< 完整提示词文本（已去除系统提示）
	ResponsePreview string `json:"response_preview"` ///< 回复预览（截取前 200 字符）
	ResponseFull    string `json:"response_full"`    ///< 完整回复文本（含思考过程和最终回复）
}

// promptListResponse 表示提示词列表的分页响应。
//
// 包含当前页数据项、总数、分页元信息，支持前端分页展示。
//
// @author chensong
// @date   2026-04-26
// @brief 提示词分页列表响应
type promptListResponse struct {
	Items      []promptListItem `json:"items"`       ///< 当前页提示词数据项列表
	Total      int              `json:"total"`       ///< 符合条件的总记录数
	Page       int              `json:"page"`        ///< 当前页码（从 1 开始）
	PerPage    int              `json:"per_page"`    ///< 每页记录数（默认 20，最大 100）
	TotalPages int              `json:"total_pages"` ///< 总页数
}

// promptDateDay 表示某个日期及其提示词数量。
//
// 用于提示词日期浏览接口，展示某一天有多少条用户提示词记录。
//
// @author chensong
// @date   2026-04-26
// @brief 单日提示词计数
type promptDateDay struct {
	Date  string `json:"date"`  ///< 日期字符串，格式 YYYYMMDD
	Count int    `json:"count"` ///< 当日有效提示词数量
}

// promptDateMonth 表示某个月份及其包含的日期明细。
//
// 用于提示词日期浏览接口，将日期按月份分组展示。
//
// @author chensong
// @date   2026-04-26
// @brief 按月份分组的日期计数
type promptDateMonth struct {
	Month string          `json:"month"` ///< 月份字符串，格式 YYYYMM
	Days  []promptDateDay `json:"days"`  ///< 该月份下各日期的提示词计数，按日期降序排列
}

// promptDatesResponse 表示提示词日期浏览接口的完整响应。
//
// 以月份为单位组织数据，包含每个月份下各日期的提示词数量。
//
// @author chensong
// @date   2026-04-26
// @brief 提示词日期列表响应
type promptDatesResponse struct {
	Months []promptDateMonth `json:"months"` ///< 月份列表，按月降序排列
}

// =============================================================================
// 对话浏览（DeepSeek 风格）相关结构体
// =============================================================================

// conversationListItem 代表对话列表中的单个对话项。
//
// 每个对话项对应一次或多次按 ConversationID / SessionID 分组的请求，
// 其中 title 由首条用户消息自动生成（截断至 120 字符）。
// 仅非流式请求参与对话分组；流式请求因缺少 ConversationID 上下文而被排除。
//
// @author chensong
// @date   2026-04-30
// @brief 对话列表项，用于侧栏展示
type conversationListItem struct {
	ID        string `json:"id"`         // ConversationID 或 SessionID
	Title     string `json:"title"`      // 首条用户消息文本（截断 120 字符）
	Timestamp string `json:"timestamp"`  // 对话首轮时间戳（RFC3339 格式）
	Model     string `json:"model"`      // 首轮使用的模型名称
	TurnCount int    `json:"turn_count"` // 对话轮数
	Preview   string `json:"preview"`    // 首条用户消息的前 100 字符预览
}

// conversationListResponse 封装对话列表的 API 响应。
//
// @author chensong
// @date   2026-04-30
// @brief 对话列表查询响应
type conversationListResponse struct {
	Conversations []conversationListItem  `json:"conversations"` // 对话列表，按时间降序（指定 date 时）
	Groups        []conversationDateGroup `json:"groups"`        // 按日期分组的对话列表（未指定 date 时）
	Date          string                  `json:"date"`          // 查询日期（YYYYMMDD），指定 date 时返回
}

// conversationDateGroup 按日期标签分组的对话列表。
//
// 用于 DeepSeek 风格侧栏：今天 / 昨天 / 本周 / 更早。
//
// @author chensong
// @date   2026-04-30
// @brief 按日期标签分组的对话组
type conversationDateGroup struct {
	Label         string                 `json:"label"`         // 日期标签："今天" / "昨天" / "本周" / "本月" / "更早"
	Date          string                 `json:"date"`          // 实际日期（YYYY-MM-DD 格式），"更早" 下可能为空
	Conversations []conversationListItem `json:"conversations"` // 该日期下的对话列表
}

// conversationTurn 表示对话中的单轮交互。
//
// Thinking 与 Response 作为独立字段返回（不复用拼接格式），
// 前端负责决定是否折叠 Thinking 以及样式化渲染。
//
// @author chensong
// @date   2026-04-30
// @brief 对话中的一论交互记录
type conversationTurn struct {
	TurnIndex    int    `json:"turn_index"`    // 轮次序号（从 1 开始）
	UserMessage  string `json:"user_message"`  // 用户消息文本
	Thinking     string `json:"thinking"`      // 思考过程（无推理时为空字符串）
	Response     string `json:"response"`      // 模型最终回复
	Model        string `json:"model"`         // 该轮使用的模型名
	PromptTokens int    `json:"prompt_tokens"` // 输入 token 数
	TotalTokens  int    `json:"total_tokens"`  // 总计 token 数
	Timestamp    string `json:"timestamp"`     // 该轮时间戳（RFC3339 格式）
}

// conversationDetailResponse 封装单个对话的完整详情 API 响应。
//
// @author chensong
// @date   2026-04-30
// @brief 对话详情查询响应
type conversationDetailResponse struct {
	ID        string             `json:"id"`         // 对话 ID
	Title     string             `json:"title"`      // 对话标题（首条用户消息）
	Timestamp string             `json:"timestamp"`  // 首轮时间戳
	Model     string             `json:"model"`      // 首轮模型
	TurnCount int                `json:"turn_count"` // 总轮数
	Turns     []conversationTurn `json:"turns"`      // 各轮交互详情，按 TurnIndex 升序
}

// NewHandler 创建并初始化一个新的 Handler 实例。
//
// 处理流程：
//  1. 记录初始化日志
//  2. 使用传入的配置、存储日志器、指标采集器和应用日志器构造 Handler 实例
//  3. 返回初始化完成的 Handler 指针
//
// @author chensong
// @date   2026-04-26
// @brief 构造 HTTP 处理器实例
// @param cfg *config.Config 全局配置对象
// @param storageLogger *storage.Logger 存储层日志写入器
// @param metrics *Metrics 请求指标采集器，可为 nil
// @param appLogger *logger.Logger 应用日志记录器
// @return *Handler 初始化完成的处理器实例
// @note Handler 的原子计数器（requestCount/errorCount/totalBytesSent）初始值为 0
func NewHandler(cfg *config.Config, storageLogger *storage.Logger, metrics *Metrics, appLogger *logger.Logger) *Handler {
	appLogger.Info("Initializing HTTP handler...")
	return &Handler{
		config:        cfg,
		storageLogger: storageLogger,
		metrics:       metrics,
		appLogger:     appLogger,
	}
}

// HandleHealthCheck 处理服务健康检查请求（GET /health）。
//
// 处理流程：
//  1. 记录请求来源地址的调试日志
//  2. 设置响应 Content-Type 为 application/json
//  3. 返回 HTTP 200 状态码
//  4. 编码返回 JSON 响应，包含服务状态、服务器时间戳、累计请求数、累计错误数
//
// @author chensong
// @date   2026-04-26
// @brief 健康检查端点处理器
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
// @note 此端点不进行鉴权，用于负载均衡器或监控系统探测服务可用性
func (h *Handler) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	h.appLogger.Debug("Health check requested from: %s", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"requests":  h.requestCount.Load(),
		"errors":    h.errorCount.Load(),
	})
}

// HandleListLogs 列出可用的日志文件（GET /api/logs）。
//
// 处理流程：
//  1. 记录请求来源地址的调试日志
//  2. 从查询参数中提取 session（会话 ID）和 date（日期）过滤条件
//  3. 调用存储层的 ListLogs 方法获取匹配的文件列表
//  4. 若查询失败，记录错误日志并返回 HTTP 500
//  5. 若查询成功，返回 JSON 响应包含文件列表和文件数量
//
// @author chensong
// @date   2026-04-26
// @brief 日志文件列表端点处理器
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
// @note 查询参数：session（可选）按会话 ID 过滤；date（可选）按日期目录过滤（格式 YYYYMMDD）
func (h *Handler) HandleListLogs(w http.ResponseWriter, r *http.Request) {
	h.appLogger.Debug("Listing log files requested from: %s", r.RemoteAddr)
	sessionID := r.URL.Query().Get("session")
	date := r.URL.Query().Get("date")

	files, err := h.storageLogger.ListLogs(sessionID, date)
	if err != nil {
		h.appLogger.Error("Failed to list log files: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.appLogger.Debug("Found %d log files", len(files))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"files": files,
		"count": len(files),
	})
}

// HandleGetLog 读取指定日志文件的内容（GET /api/log?file=xxx）。
//
// 处理流程：
//  1. 从查询参数中提取 file（文件路径）
//  2. 若 file 参数为空，记录警告日志并返回 HTTP 400
//  3. 调用存储层的 ReadLog 方法读取文件内容
//  4. 若读取失败，记录错误日志并返回 HTTP 500
//  5. 若读取成功，返回 JSON 响应包含文件路径和日志条目列表
//
// @author chensong
// @date   2026-04-26
// @brief 日志文件内容读取端点处理器
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
// @note 查询参数 file 为必填项，值为日志文件的相对路径
func (h *Handler) HandleGetLog(w http.ResponseWriter, r *http.Request) {
	h.appLogger.Debug("Retrieving log file from: %s", r.RemoteAddr)
	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		h.appLogger.Warn("Log file retrieval missing 'file' parameter from: %s", r.RemoteAddr)
		http.Error(w, "Missing 'file' parameter", http.StatusBadRequest)
		return
	}

	entries, err := h.storageLogger.ReadLog(filePath)
	if err != nil {
		h.appLogger.Error("Failed to read log file %s: %v", filePath, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.appLogger.Debug("Read %d entries from %s", len(entries), filePath)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"file":    filePath,
		"entries": entries,
	})
}

// IncrementRequestCount 递增请求计数器（原子操作，并发安全）。
//
// 处理流程：
//  1. 对 requestCount 原子变量执行加 1 操作
//
// @author chensong
// @date   2026-04-26
// @brief 请求计数递增
// @note 通常在 RequestLogger 中间件中被调用，每次 HTTP 请求到达时自动递增
func (h *Handler) IncrementRequestCount() {
	h.requestCount.Add(1)
}

// IncrementErrorCount 递增错误计数器（原子操作，并发安全）。
//
// 处理流程：
//  1. 对 errorCount 原子变量执行加 1 操作
//
// @author chensong
// @date   2026-04-26
// @brief 错误计数递增
// @note 当响应状态码 >= 400 时由 RequestLogger 中间件自动调用
func (h *Handler) IncrementErrorCount() {
	h.errorCount.Add(1)
}

// RecordBytesSent 累加已发送的响应字节数（原子操作，并发安全）。
//
// 处理流程：
//  1. 对 totalBytesSent 原子变量执行加法操作，累加本次发送的字节数
//
// @author chensong
// @date   2026-04-26
// @brief 响应流量字节数记录
// @param n int64 本次发送的字节数
func (h *Handler) RecordBytesSent(n int64) {
	h.totalBytesSent.Add(n)
}

// GetStats 返回代理服务的运行统计信息。
//
// 处理流程：
//  1. 读取 requestCount 原子变量值
//  2. 读取 errorCount 原子变量值
//  3. 读取 totalBytesSent 原子变量值
//  4. 计算约略的运行时间
//  5. 组装并返回包含所有统计数据的 map
//
// @author chensong
// @date   2026-04-26
// @brief 获取代理运行统计快照
// @return map[string]interface{} 包含 total_requests、total_errors、total_bytes_sent、uptime 字段
// @note uptime 字段为近似值，通过 requestCount 反推，并非精确的系统运行时间
func (h *Handler) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"total_requests":   h.requestCount.Load(),
		"total_errors":     h.errorCount.Load(),
		"total_bytes_sent": h.totalBytesSent.Load(),
		"uptime":           time.Since(time.Now().Add(-time.Duration(h.requestCount.Load()) * time.Second)).String(),
	}
}

// RequestLogger 是一个 HTTP 中间件，用于记录每个请求的详细信息并更新运行指标。
//
// 处理流程：
//  1. 记录请求开始时间（start）
//  2. 使用 responseWriter 包装原始 ResponseWriter，以便捕获响应状态码
//  3. 调用 IncrementRequestCount 递增请求计数
//  4. 调用 next 处理器（被包装的原始 HandlerFunc）
//  5. 计算请求耗时（duration）
//  6. 输出访问日志到标准输出：[HH:MM:SS] METHOD /path STATUS DURATION
//  7. 若 metrics 不为 nil，调用 metrics.RecordRequest 记录请求指标
//  8. 若响应状态码 >= 400，调用 IncrementErrorCount 并记录警告日志
//
// @author chensong
// @date   2026-04-26
// @brief HTTP 请求日志记录中间件
// @param next http.HandlerFunc 被包装的下游处理器
// @return http.HandlerFunc 包装后的处理器函数
// @note 日志格式示例：[14:30:25] POST /v1/chat/completions 200 1.234s
func (h *Handler) RequestLogger(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		h.IncrementRequestCount()
		next(wrapped, r)

		duration := time.Since(start)
		fmt.Printf("[%s] %s %s %d %v\n",
			start.Format("15:04:05"),
			r.Method,
			r.URL.Path,
			wrapped.statusCode,
			duration,
		)

		// Update metrics
		if h.metrics != nil {
			h.metrics.RecordRequest(r.URL.Path, wrapped.statusCode, duration)
		}

		if wrapped.statusCode >= 400 {
			h.IncrementErrorCount()
			if h.appLogger != nil {
				h.appLogger.Warn("Client error %d for %s %s", wrapped.statusCode, r.Method, r.URL.Path)
			}
		}
	}
}

// HandleUsageSummary 返回 Token 用量的聚合统计数据（GET /api/usage/summary）。
//
// 处理流程：
//  1. 从查询参数解析统计天数（days 参数），默认 30 天
//  2. 调用 buildUsageSummary 构建用量聚合数据
//  3. 若构建失败，记录错误日志并返回 HTTP 500
//  4. 设置 Content-Type 并编码返回 JSON 响应
//
// @author chensong
// @date   2026-04-26
// @brief Token 用量聚合统计接口处理器
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
// @note 查询参数 days（可选）：统计最近多少天的数据，范围 1~365，默认 30
func (h *Handler) HandleUsageSummary(w http.ResponseWriter, r *http.Request) {
	days := parseUsageDays(r)
	resp, err := h.buildUsageSummary(days)
	if err != nil {
		h.appLogger.Error("Failed to build usage summary: %v", err)
		http.Error(w, "Failed to read usage logs", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleUsageStream 通过 SSE（Server-Sent Events）流式推送用量统计数据。
//
// 处理流程：
//  1. 从查询参数解析统计天数
//  2. 检查 ResponseWriter 是否支持 http.Flusher 接口，不支持则返回 HTTP 500
//  3. 设置 SSE 响应头（Content-Type: text/event-stream、Cache-Control: no-cache、Connection: keep-alive）
//  4. 定义 send 闭包函数：构建用量摘要 → JSON 序列化 → 按 SSE 格式写入 → Flush
//  5. 立即发送第一条数据（send）
//  6. 启动 1 秒定时器，循环推送数据
//  7. 当客户端断开连接（r.Context().Done()）或发送失败时退出循环
//
// @author chensong
// @date   2026-04-26
// @brief SSE 流式推送用量统计接口处理器
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
// @note SSE 事件名称为 "usage"，数据格式为 JSON；每 1 秒推送一次
func (h *Handler) HandleUsageStream(w http.ResponseWriter, r *http.Request) {
	days := parseUsageDays(r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	send := func() error {
		resp, err := h.buildUsageSummary(days)
		if err != nil {
			return err
		}
		data, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "event: usage\ndata: %s\n\n", string(data))
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := send(); err != nil {
		if h.appLogger != nil {
			h.appLogger.Warn("usage SSE initial send failed: %v", err)
		}
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				if h.appLogger != nil {
					h.appLogger.Warn("usage SSE send failed: %v", err)
				}
				return
			}
		}
	}
}

// parseUsageDays 从 HTTP 请求查询参数中解析统计天数。
//
// 处理流程：
//  1. 从 URL 查询参数中读取 "days" 参数
//  2. 去除首尾空白字符
//  3. 若参数为空，返回默认值 30
//  4. 尝试转换为整数，验证范围在 1~365 之间
//  5. 验证通过则返回解析值，否则返回默认值 30
//
// @author chensong
// @date   2026-04-26
// @brief 解析统计天数查询参数
// @param r *http.Request HTTP 请求对象
// @return int 统计天数，范围 1~365，默认 30
// @note 参数名称为 "days"，取值范围 [1, 365]，超出范围或格式错误均返回默认值
func parseUsageDays(r *http.Request) int {
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	return days
}

// buildUsageSummary 构建用量聚合统计的完整响应数据。
//
// 处理流程：
//  1. 调用存储层 ListLogs 获取所有日志文件列表
//  2. 计算统计起始日期（当前日期 - days + 1，起始日 00:00:00）
//  3. 定义内部聚合结构体（modelAgg 模型聚合、dayAgg 日期聚合）
//  4. 遍历所有日志文件，跳过 streams/ 子目录下的原始 chunk 文件
//  5. 对每个日志文件的每条记录：
//     a. JSON 反序列化为 RequestLog
//     b. 过滤：跳过时间戳为空、超出统计时间范围、Token 数为 0 的记录
//     c. 提取 Token 数（tokensFromRecord）和延迟（parseDurationMS）
//     d. 估算费用（estimateCostUSD）
//     e. 累加全局统计量
//     f. 更新日期维度聚合（dayMap[dateKey]）
//     g. 更新模型维度聚合（modelMap[modelKey]，空模型名记为 "unknown"）
//     h. 追加到最近请求列表（recent）
//  6. 将 dayMap 转换为 daily 切片并按日期升序排列
//  7. 将 modelMap 转换为 byModel 切片并按 TotalTokens 降序排列
//  8. 将 recent 按时间戳降序排列并截取最多 20 条
//  9. 计算成功率和平均延迟
//  10. 组装并返回 usageSummaryResponse
//
// @author chensong
// @date   2026-04-26
// @brief 构建用量聚合统计响应
// @param days int 统计天数范围
// @return usageSummaryResponse 用量聚合统计响应
// @return error 读取或解析日志文件时的错误
// @note _stream_ 请求日志（包含最终 Token 用量）会被保留，仅 streams/ 目录下的原始 chunk 文件被排除
func (h *Handler) buildUsageSummary(days int) (usageSummaryResponse, error) {
	files, err := h.storageLogger.ListLogs("", "")
	if err != nil {
		return usageSummaryResponse{}, err
	}

	now := time.Now()
	start := now.AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())

	type modelAgg struct {
		requests   int
		prompt     int64
		completion int64
		total      int64
		cost       float64
	}
	type dayAgg struct {
		requests   int
		prompt     int64
		completion int64
		total      int64
		cost       float64
	}

	dayMap := make(map[string]*dayAgg)
	modelMap := make(map[string]*modelAgg)
	recent := make([]usageRecentItem, 0, 64)

	var totalRequests, successRequests int
	var latencyTotalMS int64
	var promptTokens, completionTokens, totalTokens int64
	var estimatedCost float64

	for _, file := range files {
		path := filepath.ToSlash(file)
		// Exclude raw chunk files under streams/ only.
		// Keep *_stream_ request logs because they contain final token usage.
		if strings.Contains(path, "/streams/") {
			continue
		}

		entries, readErr := h.storageLogger.ReadLog(file)
		if readErr != nil {
			if h.appLogger != nil {
				h.appLogger.Warn("Skip unreadable log file %s: %v", file, readErr)
			}
			continue
		}

		for _, raw := range entries {
			var rec storage.RequestLog
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}

			ts := rec.Timestamp
			if ts.IsZero() || ts.Before(start) || ts.After(now) {
				continue
			}

			prompt, completion, total := tokensFromRecord(rec)
			if total == 0 {
				total = prompt + completion
			}

			latency := parseDurationMS(rec.Duration)
			cost := estimateCostUSD(rec.Model, prompt, completion)
			dateKey := ts.Format("2006-01-02")

			totalRequests++
			if rec.StatusCode > 0 && rec.StatusCode < 400 {
				successRequests++
			}
			latencyTotalMS += latency
			promptTokens += prompt
			completionTokens += completion
			totalTokens += total
			estimatedCost += cost

			if _, ok := dayMap[dateKey]; !ok {
				dayMap[dateKey] = &dayAgg{}
			}
			dayMap[dateKey].requests++
			dayMap[dateKey].prompt += prompt
			dayMap[dateKey].completion += completion
			dayMap[dateKey].total += total
			dayMap[dateKey].cost += cost

			modelKey := rec.Model
			if strings.TrimSpace(modelKey) == "" {
				modelKey = "unknown"
			}
			if _, ok := modelMap[modelKey]; !ok {
				modelMap[modelKey] = &modelAgg{}
			}
			modelMap[modelKey].requests++
			modelMap[modelKey].prompt += prompt
			modelMap[modelKey].completion += completion
			modelMap[modelKey].total += total
			modelMap[modelKey].cost += cost

			recent = append(recent, usageRecentItem{
				Timestamp:        ts.Format(time.RFC3339),
				Model:            modelKey,
				Endpoint:         rec.Endpoint,
				StatusCode:       rec.StatusCode,
				DurationMS:       latency,
				PromptTokens:     prompt,
				CompletionTokens: completion,
				TotalTokens:      total,
				EstimatedCostUSD: round2(cost),
			})
		}
	}

	daily := make([]usageDailyPoint, 0, len(dayMap))
	for d, v := range dayMap {
		daily = append(daily, usageDailyPoint{
			Date:             d,
			Requests:         v.requests,
			PromptTokens:     v.prompt,
			CompletionTokens: v.completion,
			TotalTokens:      v.total,
			EstimatedCostUSD: round2(v.cost),
		})
	}
	sort.Slice(daily, func(i, j int) bool { return daily[i].Date < daily[j].Date })

	byModel := make([]usageModelPoint, 0, len(modelMap))
	for m, v := range modelMap {
		byModel = append(byModel, usageModelPoint{
			Model:            m,
			Requests:         v.requests,
			PromptTokens:     v.prompt,
			CompletionTokens: v.completion,
			TotalTokens:      v.total,
			EstimatedCostUSD: round2(v.cost),
		})
	}
	sort.Slice(byModel, func(i, j int) bool { return byModel[i].TotalTokens > byModel[j].TotalTokens })

	sort.Slice(recent, func(i, j int) bool { return recent[i].Timestamp > recent[j].Timestamp })
	if len(recent) > 20 {
		recent = recent[:20]
	}

	successRate := 0.0
	avgLatency := int64(0)
	if totalRequests > 0 {
		successRate = float64(successRequests) / float64(totalRequests)
		avgLatency = latencyTotalMS / int64(totalRequests)
	}

	resp := usageSummaryResponse{
		Days:             days,
		GeneratedAt:      time.Now().Format(time.RFC3339),
		TotalRequests:    totalRequests,
		SuccessRequests:  successRequests,
		SuccessRate:      round4(successRate),
		AvgLatencyMS:     avgLatency,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		EstimatedCostUSD: round2(estimatedCost),
		Daily:            daily,
		ByModel:          byModel,
		Recent:           recent,
	}

	return resp, nil
}

// HandlePromptDates 返回所有有提示词数据的日期，按月份分组（GET /api/prompts/dates）。
//
// 处理流程：
//  1. 仅接受 GET 方法，其他方法返回 HTTP 405
//  2. 调用 buildPromptDates 构建日期分组数据
//  3. 若构建失败，记录错误日志并返回 HTTP 500
//  4. 设置 Content-Type 并编码返回 JSON 响应
//
// @author chensong
// @date   2026-04-26
// @brief 提示词日期浏览接口处理器
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
// @note 响应按月份降序排列，每个月份内的日期也按降序排列
func (h *Handler) HandlePromptDates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	resp, err := h.buildPromptDates()
	if err != nil {
		if h.appLogger != nil {
			h.appLogger.Error("buildPromptDates: %v", err)
		}
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// buildPromptDates 构建提示词日期分组数据。
//
// 处理流程：
//  1. 调用存储层 ListLogs 获取所有日志文件列表
//  2. 遍历日志文件，跳过 streams/ 子目录
//  3. 从文件路径中提取 8 位纯数字日期段（YYYYMMDD 格式）
//  4. 对每个匹配日期的日志文件：
//     a. 读取日志条目
//     b. JSON 反序列化
//     c. 提取最后一条用户消息（extractLastUserMessage）
//     d. 去除系统提示内容（exporter.SystemReminderRE）
//     e. 过滤掉空白或过短（< 3 字符）的提示词
//     f. 对该日期计数加 1
//  5. 将日期计数按 YYYYMM 月份分组
//  6. 月份按降序排列，月份内的日期按降序排列
//  7. 组装并返回 promptDatesResponse
//
// @author chensong
// @date   2026-04-26
// @brief 构建提示词日期分组数据
// @return promptDatesResponse 日期分组响应
// @return error 读取日志文件时的错误
// @note 仅统计有有效用户提示词的日期，空日期不会出现在结果中
func (h *Handler) buildPromptDates() (promptDatesResponse, error) {
	files, err := h.storageLogger.ListLogs("", "")
	if err != nil {
		return promptDatesResponse{}, err
	}

	dayCount := make(map[string]int)

	for _, file := range files {
		path := filepath.ToSlash(file)
		if strings.Contains(path, "/streams/") {
			continue
		}

		// Extract YYYYMMDD from path like ".../YYYYMMDD/session_..."
		for _, seg := range strings.Split(path, "/") {
			if len(seg) == 8 && isAllDigits(seg) {
				entries, readErr := h.storageLogger.ReadLog(file)
				if readErr != nil {
					continue
				}
				for _, raw := range entries {
					var rec storage.RequestLog
					if err := json.Unmarshal(raw, &rec); err != nil {
						continue
					}
					if rec.RequestBody == nil {
						continue
					}
					promptText := extractLastUserMessage(rec.RequestBody)
					if promptText == "" {
						continue
					}
					promptClean := exporter.SystemReminderRE.ReplaceAllString(promptText, "")
					if len(strings.TrimSpace(promptClean)) < 3 {
						continue
					}
					dayCount[seg]++
				}
				break
			}
		}
	}

	// Group by month
	monthMap := make(map[string][]promptDateDay)
	for dateStr, count := range dayCount {
		month := dateStr[:6] // YYYYMM
		monthMap[month] = append(monthMap[month], promptDateDay{Date: dateStr, Count: count})
	}

	// Sort months descending
	var months []string
	for m := range monthMap {
		months = append(months, m)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(months)))

	result := make([]promptDateMonth, 0, len(months))
	for _, m := range months {
		days := monthMap[m]
		sort.Slice(days, func(i, j int) bool { return days[i].Date > days[j].Date })
		result = append(result, promptDateMonth{Month: m, Days: days})
	}

	return promptDatesResponse{Months: result}, nil
}

// isAllDigits 判断字符串是否全部由数字字符组成。
//
// 处理流程：
//  1. 遍历字符串中的每个 rune
//  2. 若发现任何非数字字符（c < '0' || c > '9'），返回 false
//  3. 全部通过则返回 true
//
// @author chensong
// @date   2026-04-26
// @brief 判断字符串是否为纯数字
// @param s string 待判断的字符串
// @return bool true 表示全部为数字字符，false 表示包含非数字字符
// @note 空字符串返回 true（vacuously true）
func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// HandlePromptList 返回分页的提示词列表，支持按日期过滤（GET /api/prompts/list）。
//
// 处理流程：
//  1. 仅接受 GET 方法，其他方法返回 HTTP 405
//  2. 从查询参数提取 date 过滤条件（可选，格式 YYYYMMDD）
//  3. 从查询参数解析 page 页码（默认 1，必须为正整数）
//  4. 从查询参数解析 per_page 每页条数（默认 20，最大值 100，必须为正整数）
//  5. 调用 buildPromptList 构建分页数据
//  6. 若构建失败，记录错误日志并返回 HTTP 500
//  7. 设置 Content-Type 并编码返回 JSON 响应
//
// @author chensong
// @date   2026-04-26
// @brief 提示词分页列表接口处理器
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
// @note 查询参数：date（可选）按 YYYYMMDD 过滤；page（可选，默认 1）；per_page（可选，默认 20，最大 100）
func (h *Handler) HandlePromptList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	dateFilter := strings.TrimSpace(r.URL.Query().Get("date"))

	page := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			page = n
		}
	}

	perPage := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("per_page")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			perPage = n
			if perPage > 100 {
				perPage = 100
			}
		}
	}

	resp, err := h.buildPromptList(dateFilter, page, perPage)
	if err != nil {
		if h.appLogger != nil {
			h.appLogger.Error("buildPromptList: %v", err)
		}
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// buildPromptList 构建提示词分页列表数据。
//
// 处理流程：
//  1. 调用存储层 ListLogs 获取匹配日期过滤的日志文件列表
//  2. 遍历日志文件，跳过 streams/ 子目录
//  3. 对每个日志文件的每条记录：
//     a. JSON 反序列化
//     b. 若指定了 dateFilter，检查时间戳是否匹配
//     c. 提取最后一条用户消息（extractLastUserMessage），过滤空消息
//     d. 去除系统提示内容，过滤过短（< 3 字符）的提示词
//     e. 检测提示词语种（exporter.DetectLanguage）
//     f. 提取 Token 数量（tokensFromRecord）
//     g. 提取回复内容（extractResponse），区分思考过程和最终回复
//     h. 生成预览文本（截取前 200 个 rune）
//     i. 组装 promptListItem 并追加到 allItems
//  4. 按时间戳降序排序
//  5. 按 PromptFull 去重（多轮对话可能产生相同最后消息的多次 API 调用）
//  6. 计算分页信息（total、totalPages）
//  7. 截取当前页数据（startIdx 到 endIdx）
//  8. 返回 promptListResponse
//
// @author chensong
// @date   2026-04-26
// @brief 构建提示词分页列表数据
// @param dateFilter string 日期过滤条件，格式 YYYYMMDD，空字符串表示不过滤
// @param page int 页码，从 1 开始
// @param perPage int 每页条数
// @return promptListResponse 分页提示词列表响应
// @return error 读取日志文件时的错误
// @note 去重逻辑：相同 PromptFull 的记录只保留最新的一条（按时间戳），避免多轮对话产生重复条目
func (h *Handler) buildPromptList(dateFilter string, page, perPage int) (promptListResponse, error) {
	files, err := h.storageLogger.ListLogs("", dateFilter)
	if err != nil {
		return promptListResponse{}, err
	}

	allItems := make([]promptListItem, 0, 256)

	for _, file := range files {
		path := filepath.ToSlash(file)
		if strings.Contains(path, "/streams/") {
			continue
		}

		entries, readErr := h.storageLogger.ReadLog(file)
		if readErr != nil {
			continue
		}

		for _, raw := range entries {
			var rec storage.RequestLog
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}

			if dateFilter != "" {
				ts := rec.Timestamp
				if ts.Format("20060102") != dateFilter {
					continue
				}
			}

			if rec.RequestBody == nil {
				continue
			}

			promptText := extractLastUserMessage(rec.RequestBody)
			if promptText == "" {
				continue
			}

			promptClean := exporter.SystemReminderRE.ReplaceAllString(promptText, "")
			promptClean = strings.TrimSpace(promptClean)
			if len(promptClean) < 3 {
				continue
			}

			lang := exporter.DetectLanguage(promptClean)
			promptTokens, _, totalTokens := tokensFromRecord(rec)

			// Extract response (thinking + solution)
			thinking, solution := extractResponse(&rec)
			var respPreview, respFull string
			if thinking != "" && solution != "" {
				respFull = "【思考过程】\n" + thinking + "\n\n【回复】\n" + solution
			} else if thinking != "" {
				respFull = "【思考过程】\n" + thinking
			} else if solution != "" {
				respFull = solution
			}
			runes := []rune(respFull)
			if len(runes) > 200 {
				respPreview = string(runes[:200]) + "..."
			} else {
				respPreview = respFull
			}

			promptPreview := promptClean
			prunes := []rune(promptPreview)
			if len(prunes) > 200 {
				promptPreview = string(prunes[:200]) + "..."
			}

			allItems = append(allItems, promptListItem{
				ID:              rec.ID,
				Timestamp:       rec.Timestamp.Format(time.RFC3339),
				Model:           rec.Model,
				Language:        lang,
				PromptTokens:    int(promptTokens),
				TotalTokens:     int(totalTokens),
				PromptPreview:   promptPreview,
				PromptFull:      promptClean,
				ResponsePreview: respPreview,
				ResponseFull:    respFull,
			})
		}
	}

	// Sort by timestamp descending (most recent first)
	sort.Slice(allItems, func(i, j int) bool {
		return allItems[i].Timestamp > allItems[j].Timestamp
	})

	// Deduplicate: keep only the latest entry per unique prompt.
	// Multi-turn conversations produce multiple API calls with the same
	// last user message; we only need one entry per distinct question.
	seen := make(map[string]bool, len(allItems))
	deduped := make([]promptListItem, 0, len(allItems))
	for _, it := range allItems {
		if !seen[it.PromptFull] {
			seen[it.PromptFull] = true
			deduped = append(deduped, it)
		}
	}
	allItems = deduped

	total := len(allItems)
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	startIdx := (page - 1) * perPage
	if startIdx >= total {
		return promptListResponse{
			Items:      []promptListItem{},
			Total:      total,
			Page:       page,
			PerPage:    perPage,
			TotalPages: totalPages,
		}, nil
	}

	endIdx := startIdx + perPage
	if endIdx > total {
		endIdx = total
	}

	return promptListResponse{
		Items:      allItems[startIdx:endIdx],
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
	}, nil
}

// extractResponse 从请求日志记录中提取思考过程（thinking）和最终回复（solution）。
//
// 处理流程（按优先级依次尝试）：
//  1. 优先级 1 - AggregatedResponse：从 choices[0].message 提取 content 和 reasoning_content / reasoning
//  2. 优先级 2 - Messages 链：倒序遍历消息列表，找到最后一条 assistant 角色的消息，提取 Reasoning 和 Content
//  3. 优先级 3 - ResponseBody（OpenAI 风格）：从 choices[0].message 提取 content 和 reasoning_content / reasoning
//  4. 优先级 4 - ResponseBody（Anthropic 风格）：遍历 content 数组，按 type 区分 thinking 和 text 内容
//  5. 若所有途径均无数据，返回空字符串
//
// @author chensong
// @date   2026-04-26
// @brief 从请求日志中提取思考过程和回复内容
// @param rec *storage.RequestLog 请求日志记录指针
// @return thinking string 思考过程文本（推理模型的 chain-of-thought 输出）
// @return solution string 最终回复文本
// @note 支持 OpenAI Chat Completions 和 Anthropic Messages 两种响应格式
func extractResponse(rec *storage.RequestLog) (thinking, solution string) {
	// Priority 1: AggregatedResponse
	if rec.AggregatedResponse != nil {
		if choices, ok := rec.AggregatedResponse["choices"].([]interface{}); ok && len(choices) > 0 {
			if c0, ok := choices[0].(map[string]interface{}); ok {
				if msg, ok := c0["message"].(map[string]interface{}); ok {
					content, _ := msg["content"].(string)
					reasoning, _ := msg["reasoning_content"].(string)
					if reasoning == "" {
						if rc, ok := msg["reasoning"].(string); ok {
							reasoning = rc
						}
					}
					return strings.TrimSpace(reasoning), strings.TrimSpace(content)
				}
			}
		}
	}

	// Priority 2: Messages chain
	for i := len(rec.Messages) - 1; i >= 0; i-- {
		msg := rec.Messages[i]
		if msg.Role == "assistant" {
			if msg.Reasoning != "" || msg.Content != "" {
				return msg.Reasoning, msg.Content
			}
		}
	}

	// Priority 3: ResponseBody
	if rec.ResponseBody != nil {
		// OpenAI style
		if ch, ok := rec.ResponseBody["choices"].([]interface{}); ok && len(ch) > 0 {
			if c0, ok := ch[0].(map[string]interface{}); ok {
				if msg, ok := c0["message"].(map[string]interface{}); ok {
					th, _ := msg["reasoning_content"].(string)
					if th == "" {
						if rc, ok := msg["reasoning"].(string); ok {
							th = rc
						}
					}
					so, _ := msg["content"].(string)
					return strings.TrimSpace(th), strings.TrimSpace(so)
				}
			}
		}
		// Anthropic style
		if c, ok := rec.ResponseBody["content"].([]interface{}); ok {
			var think, reg strings.Builder
			for _, b := range c {
				bm, ok := b.(map[string]interface{})
				if !ok {
					continue
				}
				typ, _ := bm["type"].(string)
				if txt, ok := bm["text"].(string); ok {
					if typ == "thinking" {
						think.WriteString(txt)
					} else {
						reg.WriteString(txt)
					}
				}
			}
			return strings.TrimSpace(think.String()), strings.TrimSpace(reg.String())
		}
	}

	return "", ""
}

// extractLastUserMessage 从请求体中提取最后一条有效的用户消息。
//
// 该函数用于获取多轮对话中用户的"真正问题"，避免展示会话压缩摘要等系统生成的用户消息。
//
// 处理流程：
//  1. 从请求体 map 中提取 messages 数组
//  2. 若 messages 为空，返回空字符串
//  3. 倒序遍历 messages（从最后一条开始）：
//     a. 跳过非 map 类型的消息项
//     b. 跳过 role 不为 "user" 的消息
//     c. 处理 string 类型的 content（OpenAI 格式）：
//        - 去除首尾空白
//        - 跳过空字符串或系统消息（isSystemMessage）
//        - 返回有效内容
//     d. 处理 array 类型的 content（Anthropic 格式）：
//        - 遍历 content 数组中的每个 block
//        - 跳过 tool_result 类型的 block
//        - 跳过非 text 类型的 block
//        - 收集所有 text block 的文本
//        - 若仅包含 tool_result 而无其他类型 block，跳过此消息
//        - 拼接所有文本，跳过空内容或系统消息，返回有效内容
//
// @author chensong
// @date   2026-04-26
// @brief 提取请求体中最后一条有效用户消息
// @param req map[string]interface{} 请求体 JSON 反序列化后的 map
// @return string 最后一条有效用户消息的文本内容，无有效消息时返回空字符串
// @note 会跳过 Claude Code 多轮对话中附加的会话压缩摘要（<cb_summary> 等系统消息）
func extractLastUserMessage(req map[string]interface{}) string {
	msgs, _ := req["messages"].([]interface{})
	if len(msgs) == 0 {
		return ""
	}
	// Walk backwards to find the last user message with real content.
	// Skip tool results, system instructions, and compaction summaries
	// that Claude Code appends at the end of multi-turn conversations.
	for i := len(msgs) - 1; i >= 0; i-- {
		mm, ok := msgs[i].(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		if role != "user" {
			continue
		}
		content := mm["content"]
		// Handle string content (OpenAI style)
		if s, ok := content.(string); ok {
			s = strings.TrimSpace(s)
			if s == "" || isSystemMessage(s) {
				continue
			}
			return s
		}
		// Handle array content (Anthropic style)
		if arr, ok := content.([]interface{}); ok {
			// Check if this message contains only tool_result blocks.
			hasToolResult := false
			hasOtherBlock := false
			var texts []string
			for _, b := range arr {
				bm, ok := b.(map[string]interface{})
				if !ok {
					continue
				}
				typ, _ := bm["type"].(string)
				if typ == "tool_result" {
					hasToolResult = true
					continue
				}
				if typ != "" && typ != "text" {
					continue
				}
				hasOtherBlock = true
				if t, ok := bm["text"].(string); ok {
					texts = append(texts, t)
				}
			}
			// Skip messages that are purely tool results
			if hasToolResult && !hasOtherBlock {
				continue
			}
			result := strings.TrimSpace(strings.Join(texts, "\n"))
			if result == "" || isSystemMessage(result) {
				continue
			}
			return result
		}
	}
	return ""
}

// getLastUserMessageFromLog 从 RequestLog 中提取最后一条有效用户消息。
// 优先从 RequestBody 提取，若 RequestBody 为空则回退到 Messages 字段。
func getLastUserMessageFromLog(rec storage.RequestLog) string {
	if rec.RequestBody != nil {
		if msg := extractLastUserMessage(rec.RequestBody); msg != "" {
			return msg
		}
	}
	// Fallback: extract from parsed Messages field
	for i := len(rec.Messages) - 1; i >= 0; i-- {
		m := rec.Messages[i]
		if m.Role != "user" {
			continue
		}
		s := strings.TrimSpace(m.Content)
		if s == "" || isSystemMessage(s) {
			continue
		}
		return s
	}
	return ""
}

// isSystemMessage 判断文本是否看起来像系统生成的指令消息（而非真实的用户输入）。
//
// 处理流程：
//  1. 去除首尾空白
//  2. 若文本为空或仅为 "..."，返回 true
//  3. 若以 "This session is being continued from" 开头（会话延续提示），返回 true
//  4. 若以 "CRITICAL:" 开头（紧急系统指令），返回 true
//  5. 若以 "<task-notification>" 或 "<system-reminder>" 开头（XML 风格系统通知），返回 true
//  6. 否则返回 false
//
// @author chensong
// @date   2026-04-26
// @brief 判断文本是否为系统消息
// @param text string 待判断的文本
// @return bool true 表示系统消息，false 表示真实用户输入
// @note 主要用于过滤 Claude Code 在请求体中附加的系统级提示和会话管理消息
func isSystemMessage(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || text == "..." {
		return true
	}
	if strings.HasPrefix(text, "This session is being continued from") {
		return true
	}
	if strings.HasPrefix(text, "CRITICAL:") {
		return true
	}
	// XML-style system/task notifications from Claude Code
	if strings.HasPrefix(text, "<task-notification>") || strings.HasPrefix(text, "<system-reminder>") {
		return true
	}
	return false
}

// tokensFromRecord 从请求日志记录中提取 Token 用量数据。
//
// 处理流程：
//  1. 从 rec.TokensUsed map 中读取 prompt_tokens
//  2. 从 rec.TokensUsed map 中读取 completion_tokens
//  3. 从 rec.TokensUsed map 中读取 total_tokens
//  4. 若 total_tokens 为 0，则用 prompt_tokens + completion_tokens 计算
//  5. 返回三个 Token 值
//
// @author chensong
// @date   2026-04-26
// @brief 提取请求记录中的 Token 用量
// @param rec storage.RequestLog 请求日志记录
// @return int64 prompt_tokens 输入 Token 数量
// @return int64 completion_tokens 输出 Token 数量
// @return int64 total_tokens Token 总数
// @note 当 TokensUsed 中 total_tokens 为 0 时自动使用 prompt + completion 计算
func tokensFromRecord(rec storage.RequestLog) (int64, int64, int64) {
	prompt := int64(rec.TokensUsed["prompt_tokens"])
	completion := int64(rec.TokensUsed["completion_tokens"])
	total := int64(rec.TokensUsed["total_tokens"])
	if total == 0 {
		total = prompt + completion
	}
	return prompt, completion, total
}

// parseDurationMS 将 duration 字符串（如 "1.5s"、"500ms"）解析为毫秒数。
//
// 处理流程：
//  1. 去除 duration 字符串的首尾空白
//  2. 使用 time.ParseDuration 解析为 time.Duration
//  3. 若解析失败，返回 0
//  4. 转换为毫秒并返回
//
// @author chensong
// @date   2026-04-26
// @brief 解析 duration 字符串为毫秒值
// @param raw string Go 语言 duration 格式字符串（如 "1.5s"、"500ms"、"2m"）
// @return int64 解析后的毫秒数，解析失败返回 0
func parseDurationMS(raw string) int64 {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return d.Milliseconds()
}

// estimateCostUSD 根据模型名称和 Token 数量估算费用（美元）。
//
// 内置主流模型的参考定价表（单位：美元/百万 Token）：
//   - gpt-4o:       输入 $5.00,  输出 $15.00
//   - gpt-4.1:      输入 $2.00,  输出 $8.00
//   - claude-3.7/3.5-sonnet: 输入 $3.00,  输出 $15.00
//   - claude-haiku: 输入 $0.80,  输出 $4.00
//   - gemini-1.5-pro:  输入 $3.50,  输出 $10.50
//   - gemini-1.5-flash: 输入 $0.35,  输出 $1.05
//   - qwen:         输入 $0.60,  输出 $1.80
//   - gemma/llama:  本地/免费模型，输入 $0.00,  输出 $0.00
//
// 处理流程：
//  1. 设置默认定价（未匹配到模型时使用）：输入 $1.00/1M，输出 $3.00/1M
//  2. 将模型名称转为小写
//  3. 遍历定价表，使用子串匹配（strings.Contains）找到第一个匹配项
//  4. 根据定价和 Token 数计算费用：promptTokens/1M * inPer1M + completionTokens/1M * outPer1M
//  5. 返回计算结果
//
// @author chensong
// @date   2026-04-26
// @brief 估算 LLM API 调用费用
// @param model string 模型名称
// @param promptTokens int64 输入 Token 数量
// @param completionTokens int64 输出 Token 数量
// @return float64 估算费用（美元），未做舍入处理，由调用方按需调用 round2
// @note 定价为行业参考价，实际费用以服务商账单为准；未匹配到的模型默认按 $1.00/$3.00 估算
func estimateCostUSD(model string, promptTokens, completionTokens int64) float64 {
	// Industry-like rough pricing per 1M tokens (input/output). Keep this as estimation.
	type pricing struct {
		inPer1M  float64
		outPer1M float64
	}

	prices := []struct {
		match string
		p     pricing
	}{
		{match: "gpt-4o", p: pricing{inPer1M: 5.0, outPer1M: 15.0}},
		{match: "gpt-4.1", p: pricing{inPer1M: 2.0, outPer1M: 8.0}},
		{match: "claude-3.7-sonnet", p: pricing{inPer1M: 3.0, outPer1M: 15.0}},
		{match: "claude-3.5-sonnet", p: pricing{inPer1M: 3.0, outPer1M: 15.0}},
		{match: "claude-haiku", p: pricing{inPer1M: 0.8, outPer1M: 4.0}},
		{match: "gemini-1.5-pro", p: pricing{inPer1M: 3.5, outPer1M: 10.5}},
		{match: "gemini-1.5-flash", p: pricing{inPer1M: 0.35, outPer1M: 1.05}},
		{match: "qwen", p: pricing{inPer1M: 0.6, outPer1M: 1.8}},
		{match: "gemma", p: pricing{inPer1M: 0.0, outPer1M: 0.0}},
		{match: "llama", p: pricing{inPer1M: 0.0, outPer1M: 0.0}},
	}

	selected := pricing{inPer1M: 1.0, outPer1M: 3.0}
	lower := strings.ToLower(model)
	for _, item := range prices {
		if strings.Contains(lower, item.match) {
			selected = item.p
			break
		}
	}
	inCost := float64(promptTokens) / 1_000_000.0 * selected.inPer1M
	outCost := float64(completionTokens) / 1_000_000.0 * selected.outPer1M
	return inCost + outCost
}

// round2 将浮点数四舍五入保留两位小数。
//
// 处理流程：
//  1. 乘以 100
//  2. 调用 math.Round 四舍五入
//  3. 除以 100
//
// @author chensong
// @date   2026-04-26
// @brief 保留两位小数四舍五入
// @param v float64 原始值
// @return float64 保留两位小数的结果
// @note 用于费用估算值的展示格式化
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// round4 将浮点数四舍五入保留四位小数。
//
// 处理流程：
//  1. 乘以 10000
//  2. 调用 math.Round 四舍五入
//  3. 除以 10000
//
// @author chensong
// @date   2026-04-26
// @brief 保留四位小数四舍五入
// @param v float64 原始值
// @return float64 保留四位小数的结果
// @note 用于成功率等精度要求较高的指标展示格式化
func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// =============================================================================
// 对话浏览（DeepSeek 风格）处理器
// =============================================================================

// HandleConversations 处理对话列表查询请求（GET /api/prompts/conversations）。
//
// 支持两种模式：
//  - 指定 ?date=YYYYMMDD：返回该日期的平铺对话列表（旧模式）
//  - 不指定 date：返回全部对话，按日期标签分组（DeepSeek 风格侧栏）
//
// @author chensong
// @date   2026-04-30
// @brief 对话列表查询端点
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
func (h *Handler) HandleConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	dateFilter := r.URL.Query().Get("date")
	w.Header().Set("Content-Type", "application/json")

	if dateFilter != "" {
		// Specific date mode
		resp, err := h.buildConversationList(dateFilter)
		if err != nil {
			h.appLogger.Error("buildConversationList failed: %v", err)
			http.Error(w, `{"error":"failed to build conversation list"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(resp)
	} else {
		// All conversations mode — grouped by date labels
		resp, err := h.buildAllConversations()
		if err != nil {
			h.appLogger.Error("buildAllConversations failed: %v", err)
			http.Error(w, `{"error":"failed to build conversation list"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(resp)
	}
}

// buildConversationList 扫描指定日期的日志文件，按 ConversationID / SessionID 分组构建对话列表。
//
// 处理流程：
//  1. 调用 storageLogger.ListLogs 获取匹配日期的日志文件
//  2. 遍历文件，逐行解析 RequestLog
//  3. 排除 streams/ 子目录文件、流式请求（ConversationID 为空）、无用户消息的条目
//  4. 按 ConversationID（优先）→ SessionID（回退）分组
//  5. 为每组生成标题（首条用户消息，截断 120 字符）
//  6. 按首条时间戳降序排列
//
// @author chensong
// @date   2026-04-30
// @brief 按日期构建对话分组列表
// @param dateFilter string 日期过滤字符串（YYYYMMDD）
// @return conversationListResponse 对话列表响应
// @return error 处理过程中的错误
func (h *Handler) buildConversationList(dateFilter string) (conversationListResponse, error) {
	files, err := h.storageLogger.ListLogs("", dateFilter)
	if err != nil {
		return conversationListResponse{}, err
	}

	// convGroup holds the aggregated state for a single conversation.
	type convGroup struct {
		id           string
		title        string
		firstTS      time.Time
		model        string
		turnCount    int
		preview      string
		hasFirstUser bool
	}

	groups := make(map[string]*convGroup)
	groupOrder := make([]string, 0) // preserve insertion order by first-seen timestamp

	for _, filePath := range files {
		// Skip streams subdirectory
		if strings.Contains(filePath, "/streams/") || strings.Contains(filePath, "\\streams\\") {
			continue
		}

		records, err := h.storageLogger.ReadLog(filePath)
		if err != nil {
			continue // skip unreadable files
		}

		for _, raw := range records {
			var rec storage.RequestLog
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}


			// Determine group key
			groupKey := rec.ConversationID
			if groupKey == "" {
				groupKey = rec.SessionID
			}
			if groupKey == "" {
				continue
			}

			// Extract user message
			userMsg := getLastUserMessageFromLog(rec)
			if userMsg != "" && isSystemMessage(userMsg) {
				continue
			}
			// Strip system-reminder tags
			if userMsg != "" {
				userMsg = exporter.SystemReminderRE.ReplaceAllString(strings.TrimSpace(userMsg), "")
			}

			group, exists := groups[groupKey]
			if !exists {
				title := userMsg
				if title == "" {
					title = "对话 - " + rec.Model
				}
				if len([]rune(title)) > 120 {
					title = string([]rune(title)[:120]) + "..."
				}
				preview := userMsg
				if len([]rune(preview)) > 100 {
					preview = string([]rune(preview)[:100]) + "..."
				}
				group = &convGroup{
					id:           groupKey,
					title:        title,
					firstTS:      rec.Timestamp,
					model:        rec.Model,
					turnCount:    1,
					preview:      preview,
					hasFirstUser: true,
				}
				groups[groupKey] = group
				groupOrder = append(groupOrder, groupKey)
			} else {
				group.turnCount++
				// Keep the earliest timestamp as firstTS
				if rec.Timestamp.Before(group.firstTS) {
					group.firstTS = rec.Timestamp
					// Update title to the earliest user message
					title := userMsg
					if len([]rune(title)) > 120 {
						title = string([]rune(title)[:120]) + "..."
					}
					group.title = title
					group.preview = userMsg
					if len([]rune(group.preview)) > 100 {
						group.preview = string([]rune(group.preview)[:100]) + "..."
					}
				}
			}
		}
	}

	// Build result list
	items := make([]conversationListItem, 0, len(groups))
	for _, key := range groupOrder {
		g := groups[key]
		items = append(items, conversationListItem{
			ID:        g.id,
			Title:     g.title,
			Timestamp: g.firstTS.Format(time.RFC3339),
			Model:     g.model,
			TurnCount: g.turnCount,
			Preview:   g.preview,
		})
	}

	// Sort by timestamp descending
	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp > items[j].Timestamp
	})

	return conversationListResponse{
		Conversations: items,
		Date:          dateFilter,
	}, nil
}

// buildAllConversations 扫描全部日志文件，按日期标签分组构建对话列表。
//
// 分组策略（模仿 DeepSeek 侧栏）：
//  - 今天 / 昨天 / 本周 / 本月 / YYYY年MM月 / 更早
//  每个 date 下的对话按时间降序排列，group 整体也按时间降序排列。
//
// @author chensong
// @date   2026-04-30
// @brief 扫描全部日志构建按日期标签分组的对话列表
// @return conversationListResponse 分组后的对话列表
// @return error 处理错误
func (h *Handler) buildAllConversations() (conversationListResponse, error) {
	files, err := h.storageLogger.ListLogs("", "")
	if err != nil {
		return conversationListResponse{}, err
	}

	// conversationData holds per-date conversation items.
	type conversationData struct {
		date string // YYYY-MM-DD
		item conversationListItem
	}

	// Map: date (YYYY-MM-DD) → convID → conversationListItem
	dateGroups := make(map[string]map[string]conversationListItem)

	for _, filePath := range files {
		if strings.Contains(filePath, "/streams/") || strings.Contains(filePath, "\\streams\\") {
			continue
		}

		records, err := h.storageLogger.ReadLog(filePath)
		if err != nil {
			continue
		}

		for _, raw := range records {
			var rec storage.RequestLog
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}


			groupKey := rec.ConversationID
			if groupKey == "" {
				groupKey = rec.SessionID
			}
			if groupKey == "" {
				continue
			}

			userMsg := getLastUserMessageFromLog(rec)
			// Only skip records where the message looks like a system-generated notification.
			// Empty messages get a placeholder title and are still included.
			if userMsg != "" && isSystemMessage(userMsg) {
				continue
			}
			if userMsg != "" {
				userMsg = exporter.SystemReminderRE.ReplaceAllString(strings.TrimSpace(userMsg), "")
			}

			dateStr := rec.Timestamp.Format("2006-01-02")

			if dateGroups[dateStr] == nil {
				dateGroups[dateStr] = make(map[string]conversationListItem)
			}

			existing, exists := dateGroups[dateStr][groupKey]
			if !exists {
				title := userMsg
				if title == "" {
					title = "对话 - " + rec.Model
				}
				if len([]rune(title)) > 120 {
					title = string([]rune(title)[:120]) + "..."
				}
				dateGroups[dateStr][groupKey] = conversationListItem{
					ID:        groupKey,
					Title:     title,
					Timestamp: rec.Timestamp.Format(time.RFC3339),
					Model:     rec.Model,
					TurnCount: 1,
				}
			} else {
				existing.TurnCount++
				if rec.Timestamp.Format(time.RFC3339) < existing.Timestamp {
					title := userMsg
					if len([]rune(title)) > 120 {
						title = string([]rune(title)[:120]) + "..."
					}
					existing.Title = title
					existing.Timestamp = rec.Timestamp.Format(time.RFC3339)
				}
				dateGroups[dateStr][groupKey] = existing
			}
		}
	}

	// Compute date labels and build groups
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Monday of current week
	weekday := now.Weekday()
	if weekday == 0 {
		weekday = 7
	}
	thisMonday := now.AddDate(0, 0, -int(weekday)+1).Format("2006-01-02")

	thisMonth := now.Format("2006-01")

	// dateLabel returns the DeepSeek-style label for a date.
	dateLabel := func(dateStr string) string {
		switch {
		case dateStr == today:
			return "今天"
		case dateStr == yesterday:
			return "昨天"
		case dateStr >= thisMonday:
			return "本周"
		case strings.HasPrefix(dateStr, thisMonth):
			return "本月"
		default:
			// Format as "2026年04月"
			if len(dateStr) >= 7 {
				y := dateStr[:4]
				m := dateStr[5:7]
				return y + "年" + m + "月"
			}
			return "更早"
		}
	}

	// Aggregate into label groups
	labelOrder := []string{"今天", "昨天", "本周", "本月"}
	labelGroups := make(map[string][]conversationListItem)
	monthlyLabels := make(map[string]string) // label → dateStr for ordering
	var monthlyKeys []string

	for dateStr, convMap := range dateGroups {
		label := dateLabel(dateStr)

		items := make([]conversationListItem, 0, len(convMap))
		for _, item := range convMap {
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Timestamp > items[j].Timestamp })

		if label == "今天" || label == "昨天" || label == "本周" || label == "本月" {
			labelGroups[label] = append(labelGroups[label], items...)
		} else {
			// Monthly or older groups
			if _, exists := labelGroups[label]; !exists {
				monthlyKeys = append(monthlyKeys, label)
				monthlyLabels[label] = dateStr
			}
			labelGroups[label] = append(labelGroups[label], items...)
		}
	}

	// Build ordered result
	var groups []conversationDateGroup

	for _, label := range labelOrder {
		if items, ok := labelGroups[label]; ok {
			sort.Slice(items, func(i, j int) bool { return items[i].Timestamp > items[j].Timestamp })
			groups = append(groups, conversationDateGroup{
				Label:         label,
				Date:          "",
				Conversations: items,
			})
		}
	}

	// Sort monthly labels by date descending
	sort.Slice(monthlyKeys, func(i, j int) bool {
		return monthlyLabels[monthlyKeys[i]] > monthlyLabels[monthlyKeys[j]]
	})

	for _, label := range monthlyKeys {
		items := labelGroups[label]
		sort.Slice(items, func(i, j int) bool { return items[i].Timestamp > items[j].Timestamp })
		groups = append(groups, conversationDateGroup{
			Label:         label,
			Date:          monthlyLabels[label],
			Conversations: items,
		})
	}

	return conversationListResponse{
		Groups: groups,
	}, nil
}

// HandleConversationDetail 处理单个对话详情查询请求（GET /api/prompts/conversation）。
//
// 处理流程：
//  1. 校验请求方法为 GET
//  2. 从查询参数中提取 id（必填）
//  3. 调用 buildConversationDetail 获取完整对话内容
//  4. 序列化为 JSON 并返回
//
// @author chensong
// @date   2026-04-30
// @brief 对话详情查询端点
// @param w http.ResponseWriter HTTP 响应写入器
// @param r *http.Request HTTP 请求对象
func (h *Handler) HandleConversationDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	convID := r.URL.Query().Get("id")
	if convID == "" {
		http.Error(w, `{"error":"missing required parameter: id"}`, http.StatusBadRequest)
		return
	}

	resp, err := h.buildConversationDetail(convID)
	if err != nil {
		h.appLogger.Error("buildConversationDetail failed: %v", err)
		http.Error(w, `{"error":"failed to build conversation detail"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// buildConversationDetail 扫描全部日志文件，查找匹配指定 ID 的完整对话记录。
//
// 处理流程：
//  1. 调用 storageLogger.ListLogs 获取全部日志文件
//  2. 排除 streams/ 子目录
//  3. 筛选 ConversationID 或 SessionID 匹配 id 的条目
//  4. 按 TurnIndex 升序排列（回退到 Timestamp）
//  5. 每轮提取 user_message（extractLastUserMessage）、thinking（extractResponse）、response（extractResponse）
//  6. 标题 = 首条用户消息文本
//
// @author chensong
// @date   2026-04-30
// @brief 按对话 ID 构建完整对话详情
// @param convID string 对话 ID（ConversationID 或 SessionID）
// @return conversationDetailResponse 对话详情响应
// @return error 处理过程中的错误
func (h *Handler) buildConversationDetail(convID string) (conversationDetailResponse, error) {
	files, err := h.storageLogger.ListLogs("", "")
	if err != nil {
		return conversationDetailResponse{}, err
	}

	type turnRec struct {
		rec  storage.RequestLog
		turn int
	}

	var matches []turnRec

	for _, filePath := range files {
		if strings.Contains(filePath, "/streams/") || strings.Contains(filePath, "\\streams\\") {
			continue
		}

		records, err := h.storageLogger.ReadLog(filePath)
		if err != nil {
			continue
		}

		for _, raw := range records {
			var rec storage.RequestLog
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}

			// Match by ConversationID or SessionID
			if rec.ConversationID != convID && rec.SessionID != convID {
				continue
			}

			matches = append(matches, turnRec{
				rec:  rec,
				turn: rec.TurnIndex,
			})
		}
	}

	// Sort by TurnIndex ascending, fallback to Timestamp
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].turn != matches[j].turn {
			return matches[i].turn < matches[j].turn
		}
		return matches[i].rec.Timestamp.Before(matches[j].rec.Timestamp)
	})

	// Build turns
	turns := make([]conversationTurn, 0, len(matches))
	var title string
	var firstTS time.Time
	var model string

	for _, m := range matches {
		rec := m.rec

		userMsg := getLastUserMessageFromLog(rec)
		if userMsg == "" || isSystemMessage(userMsg) {
			userMsg = ""
		} else {
			userMsg = exporter.SystemReminderRE.ReplaceAllString(strings.TrimSpace(userMsg), "")
		}

		thinking, solution := extractResponse(&rec)

		pTokens, _, totalTokens := tokensFromRecord(rec)

		turns = append(turns, conversationTurn{
			TurnIndex:    rec.TurnIndex,
			UserMessage:  userMsg,
			Thinking:     thinking,
			Response:     solution,
			Model:        rec.Model,
			PromptTokens: int(pTokens),
			TotalTokens:  int(totalTokens),
			Timestamp:    rec.Timestamp.Format(time.RFC3339),
		})

		// Capture first turn info for header
		if title == "" && userMsg != "" {
			title = userMsg
			if len([]rune(title)) > 120 {
				title = string([]rune(title)[:120]) + "..."
			}
			firstTS = rec.Timestamp
			model = rec.Model
		}
		if title == "" && solution != "" {
			title = solution
			if len([]rune(title)) > 120 {
				title = string([]rune(title)[:120]) + "..."
			}
		}
		if firstTS.IsZero() {
			firstTS = rec.Timestamp
		}
		if model == "" {
			model = rec.Model
		}
	}

	tsStr := ""
	if !firstTS.IsZero() {
		tsStr = firstTS.Format(time.RFC3339)
	}

	return conversationDetailResponse{
		ID:        convID,
		Title:     title,
		Timestamp: tsStr,
		Model:     model,
		TurnCount: len(turns),
		Turns:     turns,
	}, nil
}

// responseWriter 是对 http.ResponseWriter 的装饰器包装。
//
// 在原始 ResponseWriter 基础上增加了状态码（statusCode）和已写入字节数（written）
// 的捕获能力，用于中间件层的请求日志记录和错误检测。
//
// @author chensong
// @date   2026-04-26
// @brief HTTP ResponseWriter 装饰器，捕获响应状态码和写入字节数
type responseWriter struct {
	http.ResponseWriter ///< 嵌入原始 ResponseWriter，复用其方法
	statusCode int       ///< 捕获的 HTTP 响应状态码，默认值为 http.StatusOK（200）
	written    int       ///< 累计写入的响应字节数
}

// WriteHeader 拦截 WriteHeader 调用，捕获状态码后转发给原始 ResponseWriter。
//
// 处理流程：
//  1. 将传入的状态码保存到 rw.statusCode
//  2. 调用原始 ResponseWriter 的 WriteHeader 方法
//
// @author chensong
// @date   2026-04-26
// @brief 拦截并记录 HTTP 响应状态码
// @param code int HTTP 状态码
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Write 拦截 Write 调用，累加已写入字节数后转发给原始 ResponseWriter。
//
// 处理流程：
//  1. 调用原始 ResponseWriter.Write(b) 写入数据
//  2. 将实际写入的字节数 n 累加到 rw.written
//  3. 返回原始 Write 的返回值（写入字节数和错误）
//
// @author chensong
// @date   2026-04-26
// @brief 拦截并记录响应的写入字节数
// @param b []byte 待写入的字节切片
// @return int 实际写入的字节数
// @return error 写入过程中发生的错误
func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += n
	return n, err
}
