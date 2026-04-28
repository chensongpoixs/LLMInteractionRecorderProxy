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

package exporter

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"proxy-llm/storage"
)

// ──────────────────────────────────────────────────────────────────────────────
// 1. Opus-style 推理数据集格式 (ExportDay)
// ──────────────────────────────────────────────────────────────────────────────

/*
 * @struct  DatasetRow
 * @author  chensong
 * @date    2026-04-26
 * @brief   Opus-style 推理数据集记录，每行描述一个问题及其推理链
 *
 * 此结构与 Claude Opus 的训练数据格式对齐，包含问题、系统提示、思考过程
 * 和最终解答，附带自动推断的难度、分类和质量标签。
 *
 * @field ID         记录唯一标识，格式为 "proxy_<原始请求ID>"
 * @field Problem    用户问题文本，已清理 system-reminder 标签并去首尾空白
 * @field System     系统提示词，可选字段
 * @field Thinking   模型的推理/思考过程文本
 * @field Solution   模型的最终解答文本
 * @field Difficulty 难度标签：easy / medium / hard
 * @field Category   分类标签：code / math / general
 * @field Model      使用的模型名称
 * @field Provider   LLM 服务商名称 (openai/anthropic/deepseek 等)
 * @field Timestamp  时间戳，RFC3339Nano 格式
 * @field Hash       记录内容 SHA256 前 16 位十六进制，用于去重
 * @field Source     数据来源标识，格式为 "proxy-llm/<provider>"
 */
type DatasetRow struct {
	ID         string `json:"id"`
	Problem    string `json:"problem"`
	System     string `json:"system,omitempty"`
	Thinking   string `json:"thinking"`
	Solution   string `json:"solution"`
	Difficulty string `json:"difficulty"`
	Category   string `json:"category"`
	Model      string `json:"model,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Timestamp  string `json:"timestamp"`
	Hash       string `json:"hash"`
	Source     string `json:"source"`
}

// ──────────────────────────────────────────────────────────────────────────────
// 2. OpenAI Messages 微调格式 (ExportMessagesDay)
// ──────────────────────────────────────────────────────────────────────────────

/*
 * @struct  MessagesRow
 * @author  chensong
 * @date    2026-04-26
 * @brief   OpenAI fine-tuning 兼容格式的记录
 *
 * 每行对应一次对话的完整 messages 数组，可直接用于 OpenAI fine-tuning API。
 * 格式遵循 {"messages": [{"role": "...", "content": "..."}, ...]} 结构。
 *
 * @field Messages 会话消息数组，按对话顺序排列
 */
type MessagesRow struct {
	Messages []MessageEntry `json:"messages"`
}

/*
 * @struct  MessageEntry
 * @author  chensong
 * @date    2026-04-26
 * @brief   OpenAI 格式的单条消息
 *
 * @field Role      消息角色：system / user / assistant
 * @field Content   消息文本内容
 * @field Reasoning 推理内容（仅 assistant 角色），可选字段
 */
type MessageEntry struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
}

// SystemReminderRE 匹配并移除 LLM API 响应中注入的 <system-reminder>...</system-reminder> 标签块
var SystemReminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

/*
 * @func   ExportDay
 * @author chensong
 * @date   2026-04-26
 * @brief  将指定日期的所有请求日志聚合导出为 Opus-style 推理格式 JSONL
 *
 * 处理流程：
 *   1. 格式化日期字符串 (YYYYMMDD) 并验证日目录是否存在
 *   2. 收集日目录下所有非 streams/ 子目录的 .jsonl 日志文件
 *   3. 构建流式 chunk 索引 (reqID → ordered chunks)
 *   4. 创建输出目录和输出文件
 *   5. 逐行扫描每个日志文件：
 *      a. 解析 RequestLog JSON
 *      b. 过滤错误请求（无响应体且无流式数据）
 *      c. 调用 buildRow() 构建 DatasetRow（含问题提取、思考/解答抽取）
 *      d. 基于 Hash 去重
 *      e. 编码写入输出文件
 *   6. 若有效记录数为 0，删除空输出文件；否则返回导出条数
 *
 * @param  storageDir  原始 JSONL 日志的根目录 (如 ./data)
 * @param  day         要导出的日期
 * @param  outputPath  输出 JSONL 文件的完整路径
 * @return n           成功导出的记录数
 * @return err         错误信息（日目录不存在/IO 错误等）
 * @note   使用 16MB 行缓冲区处理超长响应行
 */
func ExportDay(storageDir string, day time.Time, outputPath string) (n int, err error) {
	dateStr := day.Format("20060102")
	dayDir := filepath.Join(storageDir, dateStr)
	if st, e := os.Stat(dayDir); e != nil || !st.IsDir() {
		return 0, fmt.Errorf("day directory not found: %s", dayDir)
	}

	logFiles := collectLogFiles(dayDir)
	streamIndex, _ := buildStreamIndex(filepath.Join(dayDir, "streams"))

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return 0, err
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	seen := make(map[string]struct{})

	for _, lf := range logFiles {
		f, e := os.Open(lf)
		if e != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 16*1024*1024)
		for sc.Scan() {
			b := sc.Bytes()
			if len(b) == 0 {
				continue
			}
			var rec storage.RequestLog
			if json.Unmarshal(b, &rec) != nil {
				continue
			}
			if rec.Error != "" && rec.ResponseBody == nil && rec.AggregatedResponse == nil && !rec.Stream {
				continue
			}

			row, ok := buildRow(&rec, streamIndex)
			if !ok {
				continue
			}
			if _, exists := seen[row.Hash]; exists {
				continue
			}
			seen[row.Hash] = struct{}{}
			if err := enc.Encode(row); err != nil {
				f.Close()
				return n, err
			}
			n++
		}
		_ = f.Close()
	}

	if n == 0 {
		_ = out.Close()
		_ = os.Remove(outputPath)
		return 0, nil
	}
	return n, nil
}

/*
 * @func   ExportMessagesDay
 * @author chensong
 * @date   2026-04-26
 * @brief  将指定日期的所有请求日志聚合导出为 OpenAI Messages 微调格式 JSONL
 *
 * 处理流程：
 *   1. 格式化日期字符串 (YYYYMMDD) 并验证日目录是否存在
 *   2. 收集日目录下所有非 streams/ 子目录的 .jsonl 日志文件
 *   3. 创建输出目录和输出文件
 *   4. 逐行扫描每个日志文件：
 *      a. 解析 RequestLog JSON
 *      b. 过滤错误请求
 *      c. 调用 buildMessagesRow() 构建 MessagesRow
 *      d. 序列化后计算 SHA256 前 16 位 hex 作为去重 key
 *      e. 编码写入输出文件
 *   5. 若有效记录数为 0，删除空输出文件
 *
 * @param  storageDir  原始 JSONL 日志的根目录
 * @param  day         要导出的日期
 * @param  outputPath  输出 JSONL 文件的完整路径
 * @return n           成功导出的记录数
 * @return err         错误信息
 * @note   与 ExportDay 不同，此函数不构建流式索引，仅使用 RequestLog 中的 Messages/RawResponse
 */
func ExportMessagesDay(storageDir string, day time.Time, outputPath string) (n int, err error) {
	dateStr := day.Format("20060102")
	dayDir := filepath.Join(storageDir, dateStr)
	if st, e := os.Stat(dayDir); e != nil || !st.IsDir() {
		return 0, fmt.Errorf("day directory not found: %s", dayDir)
	}

	logFiles := collectLogFiles(dayDir)

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return 0, err
	}
	out, err := os.Create(outputPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	seen := make(map[string]struct{})

	for _, lf := range logFiles {
		f, e := os.Open(lf)
		if e != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 16*1024*1024)
		for sc.Scan() {
			b := sc.Bytes()
			if len(b) == 0 {
				continue
			}
			var rec storage.RequestLog
			if json.Unmarshal(b, &rec) != nil {
				continue
			}
			if rec.Error != "" && rec.ResponseBody == nil && rec.AggregatedResponse == nil && !rec.Stream {
				continue
			}

			row, ok := buildMessagesRow(&rec)
			if !ok {
				continue
			}
			rowBytes, _ := json.Marshal(row)
			h := sha256.Sum256(rowBytes)
			hashKey := hex.EncodeToString(h[:])
			if len(hashKey) > 16 {
				hashKey = hashKey[:16]
			}
			if _, exists := seen[hashKey]; exists {
				continue
			}
			seen[hashKey] = struct{}{}
			if err := enc.Encode(row); err != nil {
				f.Close()
				return n, err
			}
			n++
		}
		_ = f.Close()
	}

	if n == 0 {
		_ = out.Close()
		_ = os.Remove(outputPath)
		return 0, nil
	}
	return n, nil
}

/*
 * @func   collectLogFiles
 * @author chensong
 * @date   2026-04-26
 * @brief  收集日目录下所有非 streams/ 子目录的 .jsonl 日志文件路径，按文件名排序
 *
 * 处理流程：
 *   1. 使用 filepath.WalkDir 遍历日目录
 *   2. 跳过 streams/ 子目录（流式 chunk 数据由 buildStreamIndex 单独处理）
 *   3. 跳过 streams/ 下的 .jsonl 文件
 *   4. 收集其余 .jsonl 文件路径
 *   5. 按字典序排序后返回
 *
 * @param  dayDir  日目录路径 (如 ./data/20260428)
 * @return         排序后的 .jsonl 文件路径切片
 */
func collectLogFiles(dayDir string) []string {
	var logFiles []string
	filepath.WalkDir(dayDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if path != dayDir && strings.Contains(path, "streams") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"streams"+string(filepath.Separator)) {
			return nil
		}
		logFiles = append(logFiles, path)
		return nil
	})
	sort.Strings(logFiles)
	return logFiles
}

/*
 * @func   buildStreamIndex
 * @author chensong
 * @date   2026-04-26
 * @brief  读取 streams/ 目录下的所有 chunk JSONL 文件，构建 reqID → 有序 chunk 列表的索引
 *
 * 处理流程：
 *   1. 检查 streams/ 目录是否存在且为目录
 *   2. 遍历目录下所有 .jsonl 文件
 *   3. 逐行解析 ResponseStream JSON
 *   4. 按 reqID 分组追加 chunk 文本（保持读取顺序即为时间顺序）
 *   5. 返回索引 map
 *
 * @param  streamsDir  streams/ 目录路径 (如 ./data/20260428/streams)
 * @return map[string][]string  reqID 到 chunk 字符串切片的映射
 * @return error                目录不存在或读取错误
 * @note   chunks 按文件读取顺序（即为写入顺序）排列，无需额外排序
 */
func buildStreamIndex(streamsDir string) (map[string][]string, error) {
	out := make(map[string][]string)
	st, err := os.Stat(streamsDir)
	if err != nil || !st.IsDir() {
		return out, err
	}
	entries, err := os.ReadDir(streamsDir)
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(streamsDir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		for sc := bufio.NewScanner(f); sc.Scan(); {
			var ch storage.ResponseStream
			if json.Unmarshal(sc.Bytes(), &ch) != nil {
				continue
			}
			if ch.ID == "" || ch.Chunk == "" {
				continue
			}
			out[ch.ID] = append(out[ch.ID], ch.Chunk)
		}
		_ = f.Close()
	}
	return out, nil
}

/*
 * @func   buildRow
 * @author chensong
 * @date   2026-04-26
 * @brief  从单条 RequestLog 构建 DatasetRow
 *
 * 处理流程：
 *   1. 检查 RequestBody 是否为 nil，若为空则返回 false
 *   2. 调用 ExtractProblem() 提取用户问题文本
 *   3. 使用正则表达式移除 <system-reminder>...</system-reminder> 标签块
 *   4. 去除首尾空白，若问题为空则返回 false
 *   5. 调用 extractThinkingSolution() 提取思考/解答
 *   6. 组装 DatasetRow：ID、Problem、System、Thinking、Solution
 *   7. 调用 inferDifficulty() 和 inferCategory() 自动标注难度和分类
 *   8. 计算 content hash 用于去重
 *   9. 生成时间戳（若原始时间戳为零值则使用当前时间）
 *
 * @param  rec          *storage.RequestLog  原始请求日志记录
 * @param  streamIndex  map[string][]string  流式 chunk 索引
 * @return DatasetRow  构建完成的推理格式记录
 * @return bool        是否成功构建（问题为空时为 false）
 */
func buildRow(rec *storage.RequestLog, streamIndex map[string][]string) (DatasetRow, bool) {
	if rec.RequestBody == nil {
		return DatasetRow{}, false
	}
	problem := ExtractProblem(rec.RequestBody, rec.SystemPrompt)
	problem = strings.TrimSpace(SystemReminderRE.ReplaceAllString(problem, ""))
	problem = strings.TrimSpace(problem)
	if problem == "" {
		return DatasetRow{}, false
	}

	thinking, solution := extractThinkingSolution(rec, streamIndex)

	row := DatasetRow{
		ID:         fmt.Sprintf("proxy_%s", rec.ID),
		Problem:    problem,
		System:     rec.SystemPrompt,
		Thinking:   thinking,
		Solution:   solution,
		Difficulty: inferDifficulty(rec.Model, thinking, solution),
		Category:   inferCategory(problem, solution, thinking),
		Model:      rec.Model,
		Provider:   rec.Provider,
		Timestamp:  rec.Timestamp.UTC().Format(time.RFC3339Nano),
		Source:     fmt.Sprintf("proxy-llm/%s", rec.Provider),
	}
	if strings.HasPrefix(row.Timestamp, "0001-") {
		row.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	row.Hash = recordHash(row.Problem, row.Thinking, row.Solution)
	return row, true
}

/*
 * @func   buildMessagesRow
 * @author chensong
 * @date   2026-04-26
 * @brief  从单条 RequestLog 构建 OpenAI Messages 微调格式的 MessagesRow
 *
 * 处理流程（按优先级）：
 *   1. 提取系统提示词：
 *      a. 优先使用 rec.SystemPrompt
 *      b. 若无，尝试从 RequestBody[]messages 中提取 system 角色消息
 *      c. 若有系统提示词，作为首条 system 消息加入 messages
 *   2. 优先使用预追踪会话消息链 (rec.Messages)，逐一转换角色对应字段
 *   3. 若无会话消息链，回退到从 RequestBody 提取用户内容：
 *      a. 调用 ExtractProblem() 提取问题文本
 *      b. 清理 system-reminder 标签
 *      c. 构建 user 消息
 *   4. 提取 assistant 响应：
 *      a. 依次尝试 AggregatedResponse → OpenAI style → Anthropic style 响应体解析
 *      b. 构建 assistant 消息（含 reasoning 字段如果存在思考过程）
 *
 * @param  rec  *storage.RequestLog  原始请求日志记录
 * @return MessagesRow  OpenAI 兼容的会话记录
 * @return bool         是否成功构建
 */
func buildMessagesRow(rec *storage.RequestLog) (MessagesRow, bool) {
	messages := make([]MessageEntry, 0, 8)

	// Add system prompt
	systemContent := strings.TrimSpace(rec.SystemPrompt)
	if systemContent == "" {
		if rec.RequestBody != nil {
			if msgs, system, _ := storage.ExtractMessagesFromRequest(rec.RequestBody); len(msgs) > 0 || system != "" {
				systemContent = system
			}
		}
	}
	if systemContent != "" {
		messages = append(messages, MessageEntry{Role: "system", Content: systemContent})
	}

	// Add conversation messages if available
	if len(rec.Messages) > 0 {
		for _, msg := range rec.Messages {
			entry := MessageEntry{Role: msg.Role, Content: msg.Content}
			if msg.Reasoning != "" && msg.Role == "assistant" {
				entry.Reasoning = msg.Reasoning
			}
			messages = append(messages, entry)
		}
		return MessagesRow{Messages: messages}, true
	}

	// Fallback: extract from request/response
	if rec.RequestBody == nil {
		return MessagesRow{}, false
	}

	userContent := ExtractProblem(rec.RequestBody, rec.SystemPrompt)
	userContent = strings.TrimSpace(SystemReminderRE.ReplaceAllString(userContent, ""))
	userContent = strings.TrimSpace(userContent)
	if userContent == "" {
		return MessagesRow{}, false
	}
	messages = append(messages, MessageEntry{Role: "user", Content: userContent})

	thinking, solution := "", ""
	if rec.AggregatedResponse != nil {
		thinking, solution = fromAggregatedResponse(rec.AggregatedResponse)
	}
	if thinking == "" && solution == "" && rec.ResponseBody != nil {
		thinking, solution = fromOpenAIStyleResponse(rec.ResponseBody)
	}
	if thinking == "" && solution == "" && rec.ResponseBody != nil {
		thinking, solution = fromAnthropicStyleResponse(rec.ResponseBody)
	}
	if solution != "" || thinking != "" {
		entry := MessageEntry{Role: "assistant", Content: solution}
		if thinking != "" {
			entry.Reasoning = thinking
		}
		messages = append(messages, entry)
	}

	return MessagesRow{Messages: messages}, true
}

/*
 * @func   ExtractProblem
 * @author chensong
 * @date   2026-04-26
 * @brief  从请求体中提取用户问题文本
 *
 * 处理流程（按优先级尝试多种请求格式）：
 *   1. 若存在 "messages" 数组 → 调用 joinUserMessages() 拼接用户消息
 *   2. 若存在 "system" 字符串字段 → 直接返回
 *   3. 若存在 "prompt" 字符串字段（文本补全格式） → 直接返回
 *   4. 若存在 "input" 字符串字段（嵌入格式） → 直接返回
 *   5. 以上均无 → 返回空字符串
 *
 * @param  req          map[string]interface{}  请求体 JSON 解析后的 map
 * @param  systemPrompt string                  系统提示词（用于过滤 messages 中的 system 消息）
 * @return              提取到的问题文本
 */
func ExtractProblem(req map[string]interface{}, systemPrompt string) string {
	if msgs, ok := req["messages"].([]interface{}); ok {
		return joinUserMessages(msgs, systemPrompt)
	}
	if s, ok := req["system"].(string); ok && s != "" {
		return s
	}
	if s, ok := req["prompt"].(string); ok {
		return s
	}
	if s, ok := req["input"].(string); ok {
		return s
	}
	return ""
}

/*
 * @func   joinUserMessages
 * @author chensong
 * @date   2026-04-26
 * @brief  将 messages 数组中 user 和 system 角色的消息内容拼接为单个问题字符串
 *
 * 处理流程：
 *   1. 遍历 messages 数组，收集 role=user 或 role=system 的消息内容
 *   2. 若 system 消息与传入的 systemPrompt 相同则跳过（避免重复）
 *   3. 调用 messageText() 解析 content 字段（支持纯文本和内容块数组两种格式）
 *   4. 使用 "\n\n" 连接所有消息片段
 *   5. 若未收集到任何 user/system 消息，则回退收集所有消息（不分角色）
 *
 * @param  msgs         []interface{}           messages 数组
 * @param  systemPrompt string                  系统提示词，用于跳过重复的 system 消息
 * @return              拼接后的问题文本
 */
func joinUserMessages(msgs []interface{}, systemPrompt string) string {
	var parts []string
	for _, m := range msgs {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		if role != "user" && role != "system" {
			continue
		}
		if role == "system" && systemPrompt != "" {
			continue
		}
		parts = append(parts, messageText(mm["content"]))
	}
	if len(parts) == 0 {
		for _, m := range msgs {
			mm, ok := m.(map[string]interface{})
			if !ok {
				continue
			}
			parts = append(parts, messageText(mm["content"]))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

/*
 * @func   messageText
 * @author chensong
 * @date   2026-04-26
 * @brief  从消息 content 字段提取纯文本
 *
 * 处理流程：
 *   1. 若 content 为字符串 → 直接返回
 *   2. 若 content 为数组（多模态内容块） → 遍历数组，提取 type="" 或 type="text" 的块的 text 字段
 *      （跳过 type="image_url" 等其他类型）
 *   3. 其他类型 → 返回空字符串
 *
 * @param  c       interface{}  消息的 content 字段值
 * @return         提取到的纯文本内容
 */
func messageText(c interface{}) string {
	switch t := c.(type) {
	case string:
		return t
	case []interface{}:
		var sb strings.Builder
		for _, b := range t {
			bm, ok := b.(map[string]interface{})
			if !ok {
				continue
			}
			if typ, _ := bm["type"].(string); typ != "text" && typ != "" {
				continue
			}
			if x, ok := bm["text"].(string); ok {
				sb.WriteString(x)
			}
		}
		return sb.String()
	default:
		return ""
	}
}

/*
 * @func   extractThinkingSolution
 * @author chensong
 * @date   2026-04-26
 * @brief  从 RequestLog 中提取模型的思考过程 (thinking) 和最终解答 (solution)
 *
 * 按以下优先级依次尝试多种数据源：
 *   1. AggregatedResponse — 流式聚合后的完整响应（最高优先级）
 *      调用 fromAggregatedResponse() 解析
 *   2. Messages 链 — 预追踪的完整对话历史
 *      反向遍历查找最后一条 assistant 消息
 *   3. ResponseBody — 非流式请求的原始响应体
 *      依次尝试 OpenAI 格式和 Anthropic 格式解析
 *   4. Stream chunks — 流式请求的 SSE chunk 聚合
 *      调用 aggregateFromStreamChunks() 合并后解析 OpenAI 流式格式
 *
 * @param  rec          *storage.RequestLog   原始请求日志
 * @param  streamIndex  map[string][]string   reqID → chunk 列表索引
 * @return thinking     模型的推理/思考过程
 * @return solution     模型的最终解答
 */
func extractThinkingSolution(rec *storage.RequestLog, streamIndex map[string][]string) (thinking, solution string) {
	// Priority 1: AggregatedResponse (new streaming aggregation)
	if rec.AggregatedResponse != nil {
		th, sol := fromAggregatedResponse(rec.AggregatedResponse)
		if sol != "" || th != "" {
			return th, sol
		}
	}

	// Priority 2: Messages chain (conversation tracking)
	if len(rec.Messages) > 0 {
		for i := len(rec.Messages) - 1; i >= 0; i-- {
			msg := rec.Messages[i]
			if msg.Role == "assistant" {
				if msg.Reasoning != "" || msg.Content != "" {
					return msg.Reasoning, msg.Content
				}
			}
		}
	}

	// Priority 3: ResponseBody (non-streaming)
	if rec.ResponseBody != nil {
		th, sol := fromOpenAIStyleResponse(rec.ResponseBody)
		if sol != "" || th != "" {
			return th, sol
		}
		th, sol = fromAnthropicStyleResponse(rec.ResponseBody)
		if sol != "" || th != "" {
			return th, sol
		}
	}

	// Priority 4: Stream chunks
	if rec.Stream {
		agg := aggregateFromStreamChunks(streamIndex[rec.ID])
		th, sol := parseOpenAIStreamAggregate(agg)
		return th, sol
	}

	return "", ""
}

/*
 * @func   fromAggregatedResponse
 * @author chensong
 * @date   2026-04-26
 * @brief  从聚合响应 map 中提取思考内容和最终回答
 *
 * 处理流程：
 *   1. 查找 choices[0].message.content（最终回答）和 choices[0].message.reasoning_content（思考内容）
 *   2. 若 reasoning_content 为空，尝试 choices[0].message.reasoning 字段
 *   3. 若上述均无内容，检查 raw_sse 原始 SSE 文本字段（流式回退）
 *   4. 移除首尾空白后返回
 *
 * @param  agg       map[string]interface{}  聚合响应 JSON map
 * @return thinking  思考过程文本
 * @return solution  最终回答文本
 */
func fromAggregatedResponse(agg map[string]interface{}) (string, string) {
	if choices, ok := agg["choices"].([]interface{}); ok && len(choices) > 0 {
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
	if rawSSE, ok := agg["raw_sse"].(string); ok && rawSSE != "" {
		return parseOpenAIStreamAggregate(rawSSE)
	}
	return "", ""
}

/*
 * @func   fromOpenAIStyleResponse
 * @author chensong
 * @date   2026-04-26
 * @brief  从 OpenAI Chat Completions 格式的响应体提取思考和回答
 *
 * 处理流程：
 *   1. 访问 choices[0].message 对象
 *   2. 提取 reasoning_content 和 content 字段
 *   3. 若 reasoning_content 为空，尝试 reasoning 字段
 *   4. 移除首尾空白后返回
 *
 * @param  m         map[string]interface{}  OpenAI 格式的响应体 JSON map
 * @return thinking  思考内容
 * @return solution  回答内容
 */
func fromOpenAIStyleResponse(m map[string]interface{}) (string, string) {
	if ch, ok := m["choices"].([]interface{}); ok && len(ch) > 0 {
		if c0, ok := ch[0].(map[string]interface{}); ok {
			if msg, ok := c0["message"].(map[string]interface{}); ok {
				th, _ := msg["reasoning_content"].(string)
				so, _ := msg["content"].(string)
				if th == "" {
					if rc, ok := msg["reasoning"].(string); ok {
						th = rc
					}
				}
				return strings.TrimSpace(th), strings.TrimSpace(so)
			}
		}
	}
	return "", ""
}

/*
 * @func   fromAnthropicStyleResponse
 * @author chensong
 * @date   2026-04-26
 * @brief  从 Anthropic Messages API 格式的响应体提取思考和回答
 *
 * 处理流程：
 *   1. 遍历 content 数组中的每个内容块
 *   2. 根据 type 字段区分：
 *      a. type="thinking" → 拼接到思考文本
 *      b. 其他 type → 拼接到回答文本
 *   3. 若 content 数组不存在，尝试顶层的 text 字段
 *
 * @param  m         map[string]interface{}  Anthropic 格式的响应体 JSON map
 * @return thinking  思考过程（thinking 类型块的聚合文本）
 * @return solution  回答文本（其他类型的聚合文本）
 * @note   Anthropic 的 extended thinking 功能将思考过程作为独立的 content block 返回
 */
func fromAnthropicStyleResponse(m map[string]interface{}) (string, string) {
	if c, ok := m["content"].([]interface{}); ok {
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
	if t, ok := m["text"].(string); ok {
		return "", strings.TrimSpace(t)
	}
	return "", ""
}

/*
 * @func   parseOpenAIStreamAggregate
 * @author chensong
 * @date   2026-04-26
 * @brief  解析聚合后的 OpenAI SSE 流式文本，提取思考和解答内容
 *
 * 处理流程：
 *   1. 按换行符拆分原始 SSE 文本
 *   2. 逐行过滤 "data:" 前缀的行
 *   3. 跳过空 payload 和 "[DONE]" 终止符
 *   4. 将 payload 解析为 JSON chunk
 *   5. 从 choices[0].delta 提取：
 *      a. delta.content → 拼接到解答
 *      b. delta.reasoning_content → 拼接到思考
 *   6. 返回修剪后的思考/解答文本
 *
 * @param  raw       string  原始 SSE 文本（多个 "data:{...}" 行拼接）
 * @return thinking  聚合后的思考过程文本
 * @return solution  聚合后的解答文本
 */
func parseOpenAIStreamAggregate(raw string) (string, string) {
	var th, out strings.Builder
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk map[string]interface{}
		if json.NewDecoder(strings.NewReader(payload)).Decode(&chunk) != nil {
			continue
		}
		ch, ok := chunk["choices"].([]interface{})
		if !ok || len(ch) == 0 {
			continue
		}
		c0, ok := ch[0].(map[string]interface{})
		if !ok {
			continue
		}
		if d, ok := c0["delta"].(map[string]interface{}); ok {
			if x, ok := d["content"].(string); ok {
				out.WriteString(x)
			}
			if x, ok := d["reasoning_content"].(string); ok {
				th.WriteString(x)
			}
		}
	}
	return strings.TrimSpace(th.String()), strings.TrimSpace(out.String())
}

/*
 * @func   aggregateFromStreamChunks
 * @author chensong
 * @date   2026-04-26
 * @brief  将流式 chunk 字符串切片用换行符连接为单个聚合文本
 *
 * @param  chunks  []string  有序的 SSE chunk 字符串（每个已是 "data:{...}" 格式）
 * @return         聚合后的原始 SSE 文本
 */
func aggregateFromStreamChunks(chunks []string) string {
	if len(chunks) == 0 {
		return ""
	}
	return strings.Join(chunks, "\n")
}

/*
 * @func   inferDifficulty
 * @author chensong
 * @date   2026-04-26
 * @brief  根据模型名称和响应长度推断问题的难度分类
 *
 * 处理流程：
 *   1. 基于模型名称关键词推断（优先级从高到低）：
 *      a. 含 haiku/flash/mini/tiny/small → "easy"
 *      b. 含 sonnet/4o/qwen/gemma/deepseek → "medium"
 *      c. 含 opus/reasoning/claude-4/gpt-4 → "hard"
 *   2. 若模型名称无法确定，根据 (thinking + solution) 总长度推断：
 *      a. > 5000 chars → "hard"
 *      b. > 1000 chars → "medium"
 *      c. 其他 → "medium"（默认）
 *
 * @param  model     string  模型名称
 * @param  thinking  string  思考过程文本
 * @param  solution  string  解答文本
 * @return           "easy" / "medium" / "hard"
 * @note   这是一个启发式推断，不保证精确匹配实际难度
 */
func inferDifficulty(model, thinking, solution string) string {
	lower := strings.ToLower(model)
	if strings.Contains(lower, "haiku") || strings.Contains(lower, "flash") || strings.Contains(lower, "mini") || strings.Contains(lower, "tiny") || strings.Contains(lower, "small") {
		return "easy"
	}
	if strings.Contains(lower, "sonnet") || strings.Contains(lower, "4o") || strings.Contains(lower, "qwen") || strings.Contains(lower, "gemma") || strings.Contains(lower, "deepseek") {
		return "medium"
	}
	if strings.Contains(lower, "opus") || strings.Contains(lower, "reasoning") || strings.Contains(lower, "claude-4") || strings.Contains(lower, "gpt-4") {
		return "hard"
	}

	totalLen := len(thinking) + len(solution)
	if totalLen > 5000 {
		return "hard"
	} else if totalLen > 1000 {
		return "medium"
	}
	return "medium"
}

/*
 * @func   inferCategory
 * @author chensong
 * @date   2026-04-26
 * @brief  根据问题、解答和思考内容推断任务分类
 *
 * 处理流程：
 *   1. 将所有文本合并转为小写
 *   2. 若包含编程关键字（def/function/#include/import/class/interface） → "code"
 *   3. 若包含数学符号（$ 出现超过 2 次 或 ∫∑√×÷） → "math"
 *   4. 其他 → "general"
 *
 * @param  problem   string  问题文本
 * @param  solution  string  解答文本
 * @param  thinking  string  思考过程文本
 * @return           "code" / "math" / "general"
 * @note   strings.ContainsAny 检查数学符号 (∫∑√×÷)
 */
func inferCategory(problem, solution, thinking string) string {
	low := strings.ToLower(problem + "\n" + solution + "\n" + thinking)
	if strings.Contains(low, "def ") || strings.Contains(low, "function ") ||
		strings.Contains(low, "#include") || strings.Contains(low, "import ") ||
		strings.Contains(low, "class ") || strings.Contains(low, "interface ") {
		return "code"
	}
	if strings.Count(low, "$") > 2 || strings.ContainsAny(low, "\u222b\u2211\u221a\u00d7\u00f7") {
		return "math"
	}
	return "general"
}

/*
 * @func   recordHash
 * @author chensong
 * @date   2026-04-26
 * @brief  计算问题 + 思考 + 解答内容的 SHA256 前 16 位十六进制哈希（用于去重）
 *
 * 处理流程：
 *   1. 使用 "\n---\n" 作为分隔符拼接 problem、thinking、solution
 *   2. 计算 SHA256 哈希
 *   3. 取前 16 位十六进制字符串
 *
 * @param  problem   string  问题文本
 * @param  thinking  string  思考过程文本
 * @param  solution  string  解答文本
 * @return           16 位十六进制 hash 字符串
 */
func recordHash(problem, thinking, solution string) string {
	h := sha256.Sum256([]byte(problem + "\n---\n" + thinking + "\n---\n" + solution))
	hexs := hex.EncodeToString(h[:])
	if len(hexs) > 16 {
		return hexs[:16]
	}
	return hexs
}

// ──────────────────────────────────────────────────────────────────────────────
// 3. 行业标准四目录数据集导出 (ExportDatasetDay)
// ──────────────────────────────────────────────────────────────────────────────

/*
 * @struct  PromptRecord
 * @author  chensong
 * @date    2026-04-26
 * @brief   用户提示词记录，输出到 prompts/ 子目录
 *
 * @field ID           唯一标识，格式为 "prompt_<原始请求ID>"
 * @field Prompt       清理后的用户提示词文本
 * @field SystemPrompt  系统提示词
 * @field Language     语言检测结果：zh / en / mixed / unknown
 * @field TaskCategory 任务分类：code / math / general
 * @field TokenCount   提示词的 token 数量（优先使用记录值，否则用字符数估算）
 * @field Timestamp    时间戳，RFC3339Nano 格式
 */
type PromptRecord struct {
	ID           string `json:"id"`
	Prompt       string `json:"prompt"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	Language     string `json:"language"`
	TaskCategory string `json:"task_category"`
	TokenCount   int    `json:"token_count"`
	Timestamp    string `json:"timestamp"`
}

/*
 * @struct  ResponseRecord
 * @author  chensong
 * @date    2026-04-26
 * @brief   模型响应记录，输出到 responses/ 子目录
 *
 * @field ID               唯一标识，格式为 "resp_<原始请求ID>"
 * @field Model            使用的模型名称
 * @field Provider         LLM 服务商
 * @field Thinking         模型的推理/思考过程
 * @field Response         模型的最终回答文本
 * @field PromptTokens     提示词 token 数
 * @field CompletionTokens 补全 token 数
 * @field TotalTokens      总 token 数
 * @field LatencyMS        请求延迟（毫秒）
 * @field Stream           是否为流式请求
 * @field StatusCode       HTTP 状态码
 * @field Timestamp        时间戳，RFC3339Nano 格式
 */
type ResponseRecord struct {
	ID               string `json:"id"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Thinking         string `json:"thinking,omitempty"`
	Response         string `json:"response"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	LatencyMS        int64  `json:"latency_ms"`
	Stream           bool   `json:"stream"`
	StatusCode       int    `json:"status_code"`
	Timestamp        string `json:"timestamp"`
}

/*
 * @struct  RevisionRecord
 * @author  chensong
 * @date    2026-04-26
 * @brief   修订记录，记录同一提示词的多轮/重复交互，输出到 revisions/ 子目录
 *
 * @field ID               唯一标识，格式为 "rev_<promptHash>_<turnIndex>"
 * @field SessionID        会话 ID，用于关联多轮对话
 * @field PromptHash       提示词的 SHA256 前 16 位 hex
 * @field OriginalRespHash 前一轮响应的 hash（首次没有）
 * @field RevisedRespHash  当前轮响应的 hash
 * @field TurnIndex        当前轮次的序号（从 1 开始）
 * @field TotalTurns       该提示词的总交互轮数
 * @field RevisionType     修订类型：multi_turn（多轮对话）/ repeated_prompt（重复提示词）
 */
type RevisionRecord struct {
	ID               string `json:"id"`
	SessionID        string `json:"session_id"`
	PromptHash       string `json:"prompt_hash"`
	OriginalRespHash string `json:"original_response_hash,omitempty"`
	RevisedRespHash  string `json:"revised_response_hash"`
	TurnIndex        int    `json:"turn_index"`
	TotalTurns       int    `json:"total_turns"`
	RevisionType     string `json:"revision_type"`
}

/*
 * @struct  FeedbackRecord
 * @author  chensong
 * @date    2026-04-26
 * @brief   自动推断的质量反馈记录，输出到 feedback/ 子目录
 *
 * @field ID                   唯一标识，格式为 "fb_<原始请求ID>"
 * @field QualityScore         综合质量评分，范围 [0.0, 1.0]，精度 0.01
 * @field ResponseCompleteness 响应完整度评分，范围 [0.0, 1.0]
 * @field HasThinking          是否包含思考过程
 * @field HasError             是否包含错误标记
 * @field ThinkingRatio        思考文本占总输出长度的比例，精度 0.001
 * @field LatencyMS            请求延迟（毫秒）
 * @field TotalTokens          总 token 数
 * @field Timestamp            时间戳，RFC3339Nano 格式
 */
type FeedbackRecord struct {
	ID                   string  `json:"id"`
	QualityScore         float64 `json:"quality_score"`
	ResponseCompleteness float64 `json:"response_completeness"`
	HasThinking          bool    `json:"has_thinking"`
	HasError             bool    `json:"has_error"`
	ThinkingRatio        float64 `json:"thinking_ratio"`
	LatencyMS            int64   `json:"latency_ms"`
	TotalTokens          int     `json:"total_tokens"`
	Timestamp            string  `json:"timestamp"`
}

/*
 * @struct  DatasetStats
 * @author  chensong
 * @date    2026-04-26
 * @brief   数据集导出统计信息
 *
 * @field TotalRecords    扫描的原始记录总数
 * @field FilteredOut     被过滤掉的记录数
 * @field Prompts         成功导出的提示词数
 * @field Responses       成功导出的响应数
 * @field Revisions       生成的修订记录数
 * @field Feedback        生成的质量反馈数
 * @field ByLanguage      按语言分组的计数：en / zh / mixed / unknown
 * @field ByTaskCategory  按任务分类分组的计数：code / math / general
 * @field ByModel         按模型名称分组的计数
 * @field TotalTokens     所有记录的总 token 数
 * @field AvgQualityScore 平均质量评分
 */
type DatasetStats struct {
	TotalRecords    int            `json:"total_records"`
	FilteredOut     int            `json:"filtered_out"`
	Prompts         int            `json:"prompts"`
	Responses       int            `json:"responses"`
	Revisions       int            `json:"revisions"`
	Feedback        int            `json:"feedback"`
	ByLanguage      map[string]int `json:"by_language"`
	ByTaskCategory  map[string]int `json:"by_task_category"`
	ByModel         map[string]int `json:"by_model"`
	TotalTokens     int            `json:"total_tokens"`
	AvgQualityScore float64        `json:"avg_quality_score"`
}

const (
	minPromptLength   = 5   // 清理后用户提示词的最小长度（低于此值将被过滤）
	minResponseLength = 5   // 非流式请求中解答/思考的最小长度（低于此值将被过滤）
)

/*
 * @func   ExportDatasetDay
 * @author chensong
 * @date   2026-04-26
 * @brief  将指定日期的请求日志导出为行业标准四目录格式数据集
 *
 * 输出目录结构：
 *   outputDir/
 *   ├── prompts/prompts.jsonl      用户提示词
 *   ├── responses/responses.jsonl  模型响应
 *   ├── revisions/revisions.jsonl  多轮/重复交互修订
 *   ├── feedback/feedback.jsonl    自动质量评分
 *   └── metadata.csv               元数据统计
 *
 * 处理流程（两遍扫描）：
 *   第一遍：收集和过滤
 *     1. 格式化日期字符串，验证日目录存在
 *     2. 收集日志文件并构建流式 chunk 索引
 *     3. 创建四个输出子目录
 *     4. 打开四个 JSONL 输出文件
 *     5. 逐条扫描日志记录：
 *        a. 过滤错误请求（无响应体且非流式）
 *        b. 过滤 HTTP 错误状态码 (>= 400)
 *        c. 提取问题文本并清理 system-reminder 标签
 *        d. 过滤短于 minPromptLength 的问题
 *        e. 提取思考/解答
 *        f. 非流式请求过滤短于 minResponseLength 的响应
 *        g. 计算 prompt hash 和 response hash 用于去重
 *        h. 去重检查
 *        i. 添加到有效记录列表，记录到 promptHashSeen 用于后续修订检测
 *   第二遍：写入输出
 *     1. 遍历有效记录列表：
 *        a. 写入 prompts/ → PromptRecord（含语言检测和分类）
 *        b. 写入 responses/ → ResponseRecord（含 token 统计和延迟）
 *        c. 写入 feedback/ → FeedbackRecord（含质量评分和完整度）
 *        d. 累加统计信息
 *     2. 生成 revisions/ → RevisionRecord
 *        a. 按 promptHash 分组
 *        b. 按 sessionID 再分组
 *        c. 每组内按 turnIndex 排序
 *        d. 为第 2 轮及以后的记录生成修订对
 *     3. 计算平均质量评分
 *     4. 写入 metadata.csv
 *
 * @param  storageDir  string       原始 JSONL 日志根目录
 * @param  day         time.Time    要导出的日期
 * @param  outputDir   string       输出目录路径
 * @return *DatasetStats            导出统计信息
 * @return error                    错误信息
 * @note   流式请求在没有保存 chunk 时解答可能为空，因此不强制要求响应长度
 */
func ExportDatasetDay(storageDir string, day time.Time, outputDir string) (*DatasetStats, error) {
	dateStr := day.Format("20060102")
	dayDir := filepath.Join(storageDir, dateStr)
	if st, e := os.Stat(dayDir); e != nil || !st.IsDir() {
		return nil, fmt.Errorf("day directory not found: %s", dayDir)
	}

	logFiles := collectLogFiles(dayDir)
	streamIndex, _ := buildStreamIndex(filepath.Join(dayDir, "streams"))

	// Output directories
	promptsDir := filepath.Join(outputDir, "prompts")
	responsesDir := filepath.Join(outputDir, "responses")
	revisionsDir := filepath.Join(outputDir, "revisions")
	feedbackDir := filepath.Join(outputDir, "feedback")
	for _, d := range []string{promptsDir, responsesDir, revisionsDir, feedbackDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, err
		}
	}

	// Open output files
	promptsFile, err := os.Create(filepath.Join(promptsDir, "prompts.jsonl"))
	if err != nil {
		return nil, err
	}
	defer promptsFile.Close()
	respsFile, err := os.Create(filepath.Join(responsesDir, "responses.jsonl"))
	if err != nil {
		return nil, err
	}
	defer respsFile.Close()
	revsFile, err := os.Create(filepath.Join(revisionsDir, "revisions.jsonl"))
	if err != nil {
		return nil, err
	}
	defer revsFile.Close()
	fbFile, err := os.Create(filepath.Join(feedbackDir, "feedback.jsonl"))
	if err != nil {
		return nil, err
	}
	defer fbFile.Close()

	promptEnc := json.NewEncoder(promptsFile)
	respEnc := json.NewEncoder(respsFile)
	revEnc := json.NewEncoder(revsFile)
	fbEnc := json.NewEncoder(fbFile)

	stats := &DatasetStats{
		ByLanguage:     make(map[string]int),
		ByTaskCategory: make(map[string]int),
		ByModel:        make(map[string]int),
	}

	// For revision detection: track prompt-hash → list of (id, responseHash, turnIndex)
	type promptOccurrence struct {
		id         string
		respHash  string
		turnIndex int
		sessionID string
	}
	promptHashSeen := make(map[string][]promptOccurrence)
	// For dedup
	seen := make(map[string]struct{})

	// First pass: collect all valid records and build revision index
	type validRecord struct {
		rec        *storage.RequestLog
		prompt     string
		thinking   string
		solution   string
		promptHash string
		respHash   string
	}
	var validRecords []validRecord

	for _, lf := range logFiles {
		f, e := os.Open(lf)
		if e != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 16*1024*1024)
		for sc.Scan() {
			b := sc.Bytes()
			if len(b) == 0 {
				continue
			}
			var rec storage.RequestLog
			if json.Unmarshal(b, &rec) != nil {
				continue
			}

			stats.TotalRecords++

			// ── Filtering ──
			if rec.Error != "" && rec.ResponseBody == nil && rec.AggregatedResponse == nil && !rec.Stream {
				stats.FilteredOut++
				continue
			}
			if rec.StatusCode >= 400 {
				stats.FilteredOut++
				continue
			}

			problem := ExtractProblem(rec.RequestBody, rec.SystemPrompt)
			problem = strings.TrimSpace(SystemReminderRE.ReplaceAllString(problem, ""))
			problem = strings.TrimSpace(problem)
			if len(problem) < minPromptLength {
				stats.FilteredOut++
				continue
			}

			thinking, solution := extractThinkingSolution(&rec, streamIndex)
			// For stream records, response content comes from stream chunks (may be empty if
			// chunks aren't saved). Only require response content for non-stream records.
			if !rec.Stream {
				if len(strings.TrimSpace(solution)) < minResponseLength && len(strings.TrimSpace(thinking)) < minResponseLength {
					stats.FilteredOut++
					continue
				}
			}

			pHash := shortHash(problem)
			rHash := shortHash(thinking + "\n---\n" + solution)

			// Dedup
			if _, exists := seen[pHash+rHash]; exists {
				stats.FilteredOut++
				continue
			}
			seen[pHash+rHash] = struct{}{}

			validRecords = append(validRecords, validRecord{
				rec:        &rec,
				prompt:     problem,
				thinking:   thinking,
				solution:   solution,
				promptHash: pHash,
				respHash:   rHash,
			})

			// Track for revisions
			promptHashSeen[pHash] = append(promptHashSeen[pHash], promptOccurrence{
				id:         rec.ID,
				respHash:   rHash,
				turnIndex:  rec.TurnIndex,
				sessionID:  rec.SessionID,
			})
		}
		_ = f.Close()
	}

	// Second pass: write output files
	totalQuality := 0.0
	for _, vr := range validRecords {
		rec := vr.rec
		ts := rec.Timestamp.UTC().Format(time.RFC3339Nano)
		if strings.HasPrefix(ts, "0001-") {
			ts = time.Now().UTC().Format(time.RFC3339Nano)
		}

		// ── prompts/ ──
		lang := DetectLanguage(vr.prompt)
		category := inferCategory(vr.prompt, vr.solution, vr.thinking)
		promptTokens := 0
		if rec.TokensUsed != nil {
			promptTokens = rec.TokensUsed["prompt_tokens"]
		}
		tokenCount := promptTokens
		if tokenCount == 0 {
			tokenCount = len([]rune(vr.prompt)) // rough estimate
		}

		promptEnc.Encode(PromptRecord{
			ID:           fmt.Sprintf("prompt_%s", rec.ID),
			Prompt:       vr.prompt,
			SystemPrompt: rec.SystemPrompt,
			Language:     lang,
			TaskCategory: category,
			TokenCount:   tokenCount,
			Timestamp:    ts,
		})
		stats.Prompts++
		stats.ByLanguage[lang]++
		stats.ByTaskCategory[category]++

		// ── responses/ ──
		pt := 0
		ct := 0
		tt := 0
		if rec.TokensUsed != nil {
			pt = rec.TokensUsed["prompt_tokens"]
			ct = rec.TokensUsed["completion_tokens"]
			tt = rec.TokensUsed["total_tokens"]
		}
		if tt == 0 {
			tt = pt + ct
		}

		respEnc.Encode(ResponseRecord{
			ID:               fmt.Sprintf("resp_%s", rec.ID),
			Model:            rec.Model,
			Provider:         rec.Provider,
			Thinking:         vr.thinking,
			Response:         vr.solution,
			PromptTokens:     pt,
			CompletionTokens: ct,
			TotalTokens:      tt,
			LatencyMS:        parseLatency(rec.Duration),
			Stream:           rec.Stream,
			StatusCode:       rec.StatusCode,
			Timestamp:        ts,
		})
		stats.Responses++
		stats.ByModel[rec.Model]++
		stats.TotalTokens += tt

		// ── feedback/ ──
		qs, completeness := computeQuality(rec, vr.thinking, vr.solution)
		thinkingRatio := 0.0
		fullLen := len(vr.thinking) + len(vr.solution)
		if fullLen > 0 {
			thinkingRatio = float64(len(vr.thinking)) / float64(fullLen)
		}

		fbEnc.Encode(FeedbackRecord{
			ID:                   fmt.Sprintf("fb_%s", rec.ID),
			QualityScore:         math.Round(qs*100) / 100,
			ResponseCompleteness: math.Round(completeness*100) / 100,
			HasThinking:          len(vr.thinking) > 0,
			HasError:             rec.Error != "",
			ThinkingRatio:        math.Round(thinkingRatio*1000) / 1000,
			LatencyMS:            parseLatency(rec.Duration),
			TotalTokens:          tt,
			Timestamp:            ts,
		})
		stats.Feedback++
		totalQuality += qs
	}

	// ── revisions/ ──
	// Emit revision records for sessions with multiple turns or repeated prompts.
	for pHash, occurrences := range promptHashSeen {
		if len(occurrences) < 2 {
			continue
		}
		// Group by session
		sessionGroups := make(map[string][]promptOccurrence)
		for _, o := range occurrences {
			sid := o.sessionID
			if sid == "" {
				sid = "_global"
			}
			sessionGroups[sid] = append(sessionGroups[sid], o)
		}
		for _, group := range sessionGroups {
			if len(group) < 2 {
				continue
			}
			sort.Slice(group, func(i, j int) bool { return group[i].turnIndex < group[j].turnIndex })
			for i := 1; i < len(group); i++ {
				revType := "multi_turn"
				if group[i].sessionID == "" || group[i].sessionID == "_global" {
					revType = "repeated_prompt"
				}
				rec := RevisionRecord{
					ID:              fmt.Sprintf("rev_%s_%d", pHash, i),
					SessionID:       group[i].sessionID,
					PromptHash:      pHash,
					RevisedRespHash: group[i].respHash,
					TurnIndex:       group[i].turnIndex,
					TotalTurns:      len(group),
					RevisionType:    revType,
				}
				if i > 0 {
					rec.OriginalRespHash = group[i-1].respHash
				}
				revEnc.Encode(rec)
				stats.Revisions++
			}
		}
	}

	if stats.Feedback > 0 {
		stats.AvgQualityScore = math.Round(totalQuality/float64(stats.Feedback)*100) / 100
	}

	// ── metadata.csv ──
	writeDatasetMetadataCSV(outputDir, stats, dateStr, len(logFiles))

	return stats, nil
}

/*
 * @func   DetectLanguage
 * @author chensong
 * @date   2026-04-26
 * @brief  检测文本的主要语言
 *
 * 处理流程：
 *   1. 遍历文本中的每个 rune
 *   2. 统计 CJK 字符数（汉字、平假名、片假名）
 *   3. 统计 ASCII 可打印字符数（32-127）
 *   4. 计算 CJK 比例：
 *      a. CJK 比例 > 30% → "zh"
 *      b. ASCII 比例 > 70% → "en"
 *      c. 其他 → "mixed"
 *   5. 若总字符数为 0 → "unknown"
 *
 * @param  text  string  待检测的文本
 * @return       "zh" / "en" / "mixed" / "unknown"
 */
func DetectLanguage(text string) string {
	cjk := 0
	ascii := 0
	total := 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			cjk++
		} else if r < 128 && r >= 32 {
			ascii++
		}
		total++
	}
	if total == 0 {
		return "unknown"
	}
	cjkRatio := float64(cjk) / float64(total)
	if cjkRatio > 0.3 {
		return "zh"
	}
	asciiRatio := float64(ascii) / float64(total)
	if asciiRatio > 0.7 {
		return "en"
	}
	return "mixed"
}

/*
 * @func   computeQuality
 * @author chensong
 * @date   2026-04-26
 * @brief  根据请求记录和响应内容自动计算质量评分和完整度评分
 *
 * 处理流程（从基线 0.5 开始加减）：
 *   1. 基线分：score = 0.5, completeness = 0.5
 *   2. 惩罚项：
 *      a. 有错误 (rec.Error != "")：score -= 0.4, completeness -= 0.3
 *      b. HTTP 状态码 >= 400：score -= 0.3, completeness -= 0.2
 *   3. 奖励项（按解答和思考长度）：
 *      a. 解答长度 > 100 chars：score += 0.2, completeness += 0.2
 *      b. 思考长度 > 50 chars：score += 0.15, completeness += 0.15
 *      c. 解答长度 > 1000 chars：score += 0.1, completeness += 0.1
 *   4. 夹紧到 [0.0, 1.0] 区间
 *
 * @param  rec          *storage.RequestLog  请求日志记录
 * @param  thinking     string               思考过程文本
 * @param  solution     string               解答文本
 * @return score        综合质量评分 [0.0, 1.0]
 * @return completeness 响应完整度评分 [0.0, 1.0]
 * @note   此评分基于启发式规则，用于自动标注数据质量，不替代人工评估
 */
func computeQuality(rec *storage.RequestLog, thinking, solution string) (score, completeness float64) {
	score = 0.5 // baseline
	completeness = 0.5

	if rec.Error != "" {
		score -= 0.4
		completeness -= 0.3
	}
	if rec.StatusCode >= 400 {
		score -= 0.3
		completeness -= 0.2
	}

	solLen := len(strings.TrimSpace(solution))
	thinkLen := len(strings.TrimSpace(thinking))

	if solLen > 100 {
		score += 0.2
		completeness += 0.2
	}
	if thinkLen > 50 {
		score += 0.15
		completeness += 0.15
	}
	if solLen > 1000 {
		score += 0.1
		completeness += 0.1
	}

	// Clamp
	score = math.Max(0.0, math.Min(1.0, score))
	completeness = math.Max(0.0, math.Min(1.0, completeness))
	return
}

/*
 * @func   shortHash
 * @author chensong
 * @date   2026-04-26
 * @brief  计算字符串的 SHA256 前 16 位十六进制哈希（短哈希）
 *
 * @param  s       string  输入字符串
 * @return         16 位十六进制 hash 字符串
 */
func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

/*
 * @func   parseLatency
 * @author chensong
 * @date   2026-04-26
 * @brief  将 duration 字符串（如 "1.5s", "350ms"）解析为毫秒数
 *
 * 处理流程：
 *   1. 去除首尾空白
 *   2. 调用 time.ParseDuration() 解析
 *   3. 调用 Milliseconds() 转为毫秒
 *   4. 解析失败返回 0
 *
 * @param  raw  string  duration 字符串（Go time.Duration 格式）
 * @return      毫秒数（int64）
 */
func parseLatency(raw string) int64 {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return d.Milliseconds()
}

/*
 * @func   writeDatasetMetadataCSV
 * @author chensong
 * @date   2026-04-26
 * @brief  将数据集统计信息和分布写入 metadata.csv 文件
 *
 * 输出内容（field,value 格式）：
 *   1. 导出日期和日志文件数
 *   2. 扫描/过滤/有效记录计数
 *   3. 各子目录记录数（prompts/responses/revisions/feedback）
 *   4. 总 token 数和平均质量评分
 *   5. 过滤阈值（min prompt length, min response length）
 *   6. 语言分布（language_zh / language_en / language_mixed / language_unknown）
 *   7. 任务分类分布（category_code / category_math / category_general）
 *   8. 模型分布（model_<modelName>）
 *
 * @param  outputDir  string         输出目录路径
 * @param  stats      *DatasetStats  导出统计数据
 * @param  dateStr    string         日期字符串 (YYYYMMDD)
 * @param  fileCount  int            源日志文件数
 */
func writeDatasetMetadataCSV(outputDir string, stats *DatasetStats, dateStr string, fileCount int) {
	csvPath := filepath.Join(outputDir, "metadata.csv")
	f, err := os.Create(csvPath)
	if err != nil {
		return
	}
	defer f.Close()

	f.WriteString("field,value\n")
	fmt.Fprintf(f, "export_date,%s\n", dateStr)
	fmt.Fprintf(f, "source_log_files,%d\n", fileCount)
	fmt.Fprintf(f, "total_records_scanned,%d\n", stats.TotalRecords)
	fmt.Fprintf(f, "records_filtered_out,%d\n", stats.FilteredOut)
	fmt.Fprintf(f, "valid_prompts,%d\n", stats.Prompts)
	fmt.Fprintf(f, "valid_responses,%d\n", stats.Responses)
	fmt.Fprintf(f, "revision_pairs,%d\n", stats.Revisions)
	fmt.Fprintf(f, "feedback_records,%d\n", stats.Feedback)
	fmt.Fprintf(f, "total_tokens,%d\n", stats.TotalTokens)
	fmt.Fprintf(f, "avg_quality_score,%.2f\n", stats.AvgQualityScore)
	fmt.Fprintf(f, "filter_min_prompt_length,%d\n", minPromptLength)
	fmt.Fprintf(f, "filter_min_response_length,%d\n", minResponseLength)

	// Language distribution
	for lang, count := range stats.ByLanguage {
		fmt.Fprintf(f, "language_%s,%d\n", lang, count)
	}
	// Task category distribution
	for cat, count := range stats.ByTaskCategory {
		fmt.Fprintf(f, "category_%s,%d\n", cat, count)
	}
	// Model distribution
	for model, count := range stats.ByModel {
		fmt.Fprintf(f, "model_%s,%d\n", model, count)
	}
}
