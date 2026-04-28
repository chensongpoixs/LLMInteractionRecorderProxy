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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"proxy-llm/config"
)

// ===========================================================================
// SSE 事件数据结构
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   ReAct 智能体 SSE 事件流中"思考"事件的数据结构。
 *          当 LLM 返回其推理过程时，服务端通过 SSE 的 "thinking" 事件
 *          将该结构发送给客户端，用于展示当前循环中 LLM 的分析和规划。
 *
 * @field Iteration 当前是第几轮 ReAct 循环（从 1 开始计数）
 * @field Thought   LLM 返回的思考内容，描述下一步计划或分析结果
 */
type agentThinkingEvent struct {
	Iteration int    `json:"iteration"`
	Thought   string `json:"thought"`
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   ReAct 智能体 SSE 事件流中"动作"事件的数据结构。
 *          当 LLM 决定调用某个工具时，服务端通过 SSE 的 "action" 事件
 *          发送该结构，告知客户端即将执行哪个工具及其参数。
 *
 * @field Iteration  当前是第几轮 ReAct 循环（从 1 开始计数）
 * @field Tool       被调用的工具名称（如 read_file, bash, grep 等）
 * @field Arguments  工具参数 JSON 字符串
 * @field Descriptor 人类可读的工具调用描述（如"读取文件: /path/to/file"）
 */
type agentActionEvent struct {
	Iteration  int    `json:"iteration"`
	Tool       string `json:"tool"`
	Arguments  string `json:"arguments"`
	Descriptor string `json:"descriptor"`
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   ReAct 智能体 SSE 事件流中"观察"事件的数据结构。
 *          当工具执行完毕后，服务端通过 SSE 的 "observation" 事件
 *          发送该结构，将工具的输出结果反馈给客户端。
 *
 * @field Iteration 当前是第几轮 ReAct 循环（从 1 开始计数）
 * @field Result    工具执行的标准输出结果字符串
 * @field Truncated 结果是否因长度限制而被截断
 */
type agentObservationEvent struct {
	Iteration int    `json:"iteration"`
	Result    string `json:"result"`
	Truncated bool   `json:"truncated"`
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   ReAct 智能体 SSE 事件流中"最终回复"事件的数据结构。
 *          当 LLM 收集到足够信息并给出最终答案时，服务端通过 SSE 的
 *          "response" 事件发送该结构。
 *
 * @field Content LLM 生成的最终回复文本（默认使用中文）
 */
type agentResponseEvent struct {
	Content string `json:"content"`
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   ReAct 智能体 SSE 事件流中"完成"事件的数据结构。
 *          当 ReAct 循环正常结束时，服务端通过 SSE 的 "done" 事件
 *          发送该结构，表示整个智能体会话已完成。
 *
 * @field Iterations 总共执行的 ReAct 循环轮数
 * @field TokensUsed 各模型消耗的 Token 统计（可选字段）
 */
type agentDoneEvent struct {
	Iterations int            `json:"iterations"`
	TokensUsed map[string]int `json:"tokens_used,omitempty"`
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   ReAct 智能体 SSE 事件流中"错误"事件的数据结构。
 *          当发生异常（如 LLM 调用失败、达到最大循环次数等）时，
 *          服务端通过 SSE 的 "error" 事件发送该结构。
 *
 * @field Message 错误描述信息
 */
type agentErrorEvent struct {
	Message string `json:"message"`
}

// ===========================================================================
// Agent 请求/响应数据结构
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   客户端发送给 ReAct 智能体的请求结构。
 *          客户端通过 POST 请求体以 JSON 格式传入模型选择、用户消息
 *          以及要启用的工具列表。
 *
 * @field Model   模型配置名称，对应 config.yaml 中 models[].name
 * @field Message 用户输入的原始消息
 * @field Tools   要启用的工具名称列表（如 ["read_file", "bash", "grep"]），
 *                为空时默认启用全部工具
 */
type agentChatRequest struct {
	Model   string   `json:"model"`
	Message string   `json:"message"`
	Tools   []string `json:"tools"`
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   LLM 返回的 JSON 中 "action" 字段的结构。
 *          描述了 LLM 决定调用的工具名称及其参数。
 *
 * @field Tool      要调用的工具名称
 * @field Arguments 工具的键值对参数（如 {"path": "/file.txt"} 或 {"command": "ls -la"}）
 */
type agentLLMAction struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   LLM 返回的完整 JSON 响应结构。
 *          这是 ReAct 循环中解析 LLM 输出的顶层结构，包含思考过程、
 *          以及下一步动作或最终答案。
 *
 * @field Thought     当前轮次 LLM 的分析与推理（必填）
 * @field Action      工具调用信息（与 FinalAnswer 互斥，二选一）
 * @field FinalAnswer 最终答案文本（与 Action 互斥，二选一），
 *                    当 LLM 认为已有足够信息时产生
 */
type agentLLMResponse struct {
	Thought     string          `json:"thought"`
	Action      *agentLLMAction `json:"action,omitempty"`
	FinalAnswer string          `json:"final_answer,omitempty"`
}

// ===========================================================================
// ReAct 系统提示词
// ===========================================================================

/*
 * reactSystemPrompt 是发送给 LLM 的系统级提示词常量。
 * 它定义了 ReAct 智能体的行为规范：
 *   - 列出所有可用工具及其用途
 *   - 规定 Think → Act → Observe 三步循环
 *   - 定义 JSON 输出格式（tool 调用 vs final_answer）
 *   - 行为约束：一次一个工具、默认中文回答、失败后尝试替代方案
 */
const reactSystemPrompt = `You are a ReAct (Reasoning + Acting) agent. You have access to tools to help answer the user's questions.

## Available Tools

- **read_file(path)**: Read the contents of a file at the given path.
- **write_file(path, content)**: Write content to a file at the given path. Creates parent directories automatically.
- **bash(command)**: Execute a shell command and return stdout+stderr.
- **grep(pattern, path)**: Search for a regex pattern in files. Path can be a file or directory.
- **glob(pattern)**: Find files matching a glob pattern (e.g., "**/*.go", "*.txt").

## ReAct Cycle

For each user request, follow this cycle:
1. **THINK**: Analyze what you know and what you need to find out. Decide which tool to use next.
2. **ACT**: Execute exactly ONE tool per response.
3. **OBSERVE**: You will see the tool's output, then think again.

## Response Format

When you want to use a tool, respond with:
{
  "thought": "your reasoning about what to do next",
  "action": {
    "tool": "tool_name",
    "arguments": {
      "arg1": "value1"
    }
  }
}

When you have enough information and are ready to answer the user, respond with:
{
  "thought": "I now have all the information needed to answer",
  "final_answer": "your complete response to the user in Chinese"
}

## Rules
- Always use Chinese for final_answer unless the user explicitly asks for another language.
- Execute only ONE tool at a time.
- Be thorough: read files before modifying them, verify results before concluding.
- If a tool fails, analyze the error and try a different approach.
- Keep your responses concise and focused on the user's request.`

// ===========================================================================
// HTTP Handler
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   处理 ReAct 智能体聊天请求的主入口 SSE Handler。
 *
 *          handleAgentChat 是代理路由中 "/v1/agent/chat" 的 HTTP 处理器，
 *          实现了完整的 ReAct（Reasoning + Acting）循环。客户端发送用户消息后，
 *          服务端通过 SSE 实时推送思考、动作、观察、回复等事件，直到 LLM 给出
 *          最终答案或达到最大循环次数。
 *
 *          处理流程：
 *          1. 校验 HTTP 方法必须为 POST，否则返回 405
 *          2. 解析请求体 JSON 为 agentChatRequest 结构
 *          3. 校验 message 字段非空
 *          4. 查找匹配的模型配置，若找不到则返回 400
 *          5. 若未指定工具列表，默认启用全部 5 个工具
 *          6. 检测 ResponseWriter 是否支持 Flusher（SSE 必需）
 *          7. 设置 SSE 响应头（Content-Type: text/event-stream 等）
 *          8. 读取 Agent 配置（最大迭代次数、工作目录、Bash 超时），使用默认值兜底
 *          9. 创建工作目录（如不存在）
 *          10. 构建 LLM 消息列表（system 提示词 + user 消息）
 *          11. 进入 ReAct 主循环（最多 maxIter 轮）：
 *              a. 检查 context 是否已取消
 *              b. 调用 LLM（非流式，便于 JSON 解析），获取响应文本
 *              c. 尝试将 LLM 输出解析为 agentLLMResponse
 *                 若失败则尝试从 Markdown 代码块中提取 JSON
 *              d. 发送 "thinking" SSE 事件
 *              e. 若解析到 FinalAnswer，发送 "response" + "done" 事件后返回
 *              f. 若无 Action 也无 FinalAnswer，将 Thought 作为最终答案返回
 *              g. 发送 "action" SSE 事件
 *              h. 检查工具是否启用，未启用则添加错误提示到消息历史并继续下一轮
 *              i. 执行工具调用，发送 "observation" SSE 事件
 *              j. 将 assistant 回复和工具结果追加到消息历史
 *          12. 若超过最大循环次数，发送 "error" 事件
 *
 * @param  w http.ResponseWriter - SSE 流式响应写入器
 * @param  r *http.Request       - 客户端 HTTP 请求
 * @return 无（通过 SSE 事件流返回结果）
 *
 * @note   该函数需要 http.ResponseWriter 实现 http.Flusher 接口，
 *         否则无法进行 SSE 流式传输，会返回 500 错误。
 *
 * @note   ReAct 循环中每次调用 LLM 都使用非流式模式（stream=false），
 *         以确保能完整获取 JSON 格式的响应内容用于解析。
 *
 * @code
 *   // 客户端示例（curl）：
 *   curl -N -X POST http://localhost:8080/v1/agent/chat \
 *     -H "Content-Type: application/json" \
 *     -d '{"model":"deepseek-chat","message":"帮我看看 /tmp 目录下有哪些文件"}'
 *
 *   // SSE 事件流示例：
 *   event: thinking
 *   data: {"iteration":1,"thought":"我需要查看 /tmp 目录下的文件列表"}
 *
 *   event: action
 *   data: {"iteration":1,"tool":"bash","arguments":"{\"command\":\"ls /tmp\"}","descriptor":"执行命令: ls /tmp"}
 *
 *   event: observation
 *   data: {"iteration":1,"result":"file1.txt\nfile2.log","truncated":false}
 *
 *   event: response
 *   data: {"content":"/tmp 目录下有以下文件：file1.txt, file2.log"}
 *
 *   event: done
 *   data: {"iterations":1}
 * @endcode
 */
func (p *Proxy) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req agentChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}

	modelCfg := p.findModelConfig(req.Model)
	if modelCfg == nil {
		http.Error(w, `{"error":"model not found: `+req.Model+`"}`, http.StatusBadRequest)
		return
	}

	// Default to all tools when not specified
	if len(req.Tools) == 0 {
		req.Tools = []string{"read_file", "write_file", "bash", "grep", "glob"}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()

	agentCfg := p.config.Agent
	maxIter := agentCfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 15
	}
	workspace := agentCfg.WorkspaceDir
	if workspace == "" {
		workspace = "./agent_workspace"
	}
	bashTimeout := agentCfg.BashTimeout
	if bashTimeout <= 0 {
		bashTimeout = 30
	}

	// Ensure workspace exists
	os.MkdirAll(workspace, 0755)

	// Build messages
	messages := []map[string]interface{}{
		{"role": "system", "content": reactSystemPrompt},
		{"role": "user", "content": req.Message},
	}

	sendSSE := func(event string, data interface{}) error {
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(payload))
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	sendError := func(msg string) {
		sendSSE("error", agentErrorEvent{Message: msg})
	}

	for iteration := 1; iteration <= maxIter; iteration++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Call LLM (non-streaming for JSON parsing)
		respBody, err := p.callLLM(ctx, modelCfg, messages)
		if err != nil {
			sendError(fmt.Sprintf("LLM 调用失败: %v", err))
			return
		}

		// Parse the LLM JSON response
		var parsed agentLLMResponse
		if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
			// Try to extract JSON from markdown code blocks
			extracted := extractJSON(respBody)
			if extracted == "" || json.Unmarshal([]byte(extracted), &parsed) != nil {
				sendError("无法解析 Agent 响应格式，请重试")
				return
			}
		}

		// Send thinking event
		if err := sendSSE("thinking", agentThinkingEvent{
			Iteration: iteration,
			Thought:   parsed.Thought,
		}); err != nil {
			return
		}

		// Check for final answer
		if parsed.FinalAnswer != "" {
			if err := sendSSE("response", agentResponseEvent{
				Content: parsed.FinalAnswer,
			}); err != nil {
				return
			}
			sendSSE("done", agentDoneEvent{Iterations: iteration})
			return
		}

		// Must have action to continue
		if parsed.Action == nil {
			// No action and no final answer — treat thought as final
			if err := sendSSE("response", agentResponseEvent{
				Content: parsed.Thought,
			}); err != nil {
				return
			}
			sendSSE("done", agentDoneEvent{Iterations: iteration})
			return
		}

		// Send action event
		argsJSON, _ := json.Marshal(parsed.Action.Arguments)
		if err := sendSSE("action", agentActionEvent{
			Iteration:  iteration,
			Tool:       parsed.Action.Tool,
			Arguments:  string(argsJSON),
			Descriptor: buildDescriptor(parsed.Action.Tool, parsed.Action.Arguments),
		}); err != nil {
			return
		}

		// Execute tool
		if !toolEnabled(req.Tools, parsed.Action.Tool) {
			messages = append(messages,
				map[string]interface{}{"role": "assistant", "content": respBody},
				map[string]interface{}{"role": "user", "content": fmt.Sprintf("工具 %s 未启用。可用工具: %s", parsed.Action.Tool, strings.Join(req.Tools, ", "))},
			)
			continue
		}

		result, toolErr := executeTool(parsed.Action.Tool, parsed.Action.Arguments, workspace, bashTimeout)
		truncated := false
		if toolErr != nil {
			result = fmt.Sprintf("错误: %v", toolErr)
		}

		// Send observation event
		if err := sendSSE("observation", agentObservationEvent{
			Iteration: iteration,
			Result:    result,
			Truncated: truncated,
		}); err != nil {
			return
		}

		// Append to message history
		messages = append(messages,
			map[string]interface{}{"role": "assistant", "content": respBody},
			map[string]interface{}{"role": "user", "content": fmt.Sprintf("工具执行结果:\n%s", result)},
		)
	}

	// Exceeded max iterations
	sendError(fmt.Sprintf("已达到最大循环次数 (%d)，请简化请求或增加 max_iterations 配置", maxIter))
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   根据模型名称查找对应的模型配置。
 *
 *          处理流程：
 *          1. 遍历代理配置中的所有模型
 *          2. 按 Name 字段进行精确匹配
 *          3. 若找到则返回其指针，否则返回 nil
 *
 * @param   name string - 要查找的模型名称（对应 config.yaml 中 models[].name）
 * @return  *config.ModelConfig - 匹配的模型配置指针，未找到时返回 nil
 *
 * @note   该方法使用指针返回，避免大结构体拷贝。
 *
 * @code
 *   cfg := proxy.findModelConfig("deepseek-chat")
 *   if cfg == nil {
 *       log.Fatal("模型配置未找到")
 *   }
 *   fmt.Println(cfg.BaseURL)
 * @endcode
 */
func (p *Proxy) findModelConfig(name string) *config.ModelConfig {
	for _, m := range p.config.Models {
		if m.Name == name {
			return &m
		}
	}
	return nil
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   调用 OpenAI 兼容的 Chat Completions API 获取 LLM 响应。
 *
 *          处理流程：
 *          1. 构建请求体：model + messages，设置 stream=false（非流式）
 *          2. 将请求体序列化为 JSON
 *          3. 拼接目标 URL：{BaseURL}/chat/completions
 *          4. 创建带 context 的 HTTP POST 请求，设置 Content-Type 和 Authorization 头
 *          5. 从代理的 clients 缓存中获取 HTTP 客户端（不存在则新建，超时 300 秒）
 *          6. 发送 HTTP 请求
 *          7. 读取响应体（上限 256KB 防止内存溢出）
 *          8. 检查 HTTP 状态码，非 200 返回错误
 *          9. 解析 OpenAI Chat Completions 格式的 JSON 响应
 *          10. 提取 choices[0].message.content 并返回
 *
 * @param   ctx      context.Context      - 请求上下文，用于超时控制和取消
 * @param   modelCfg *config.ModelConfig  - 模型配置（BaseURL、APIKey、ModelName）
 * @param   messages []map[string]interface{} - 对话消息历史
 * @return  string - LLM 返回的文本内容
 * @return  error  - 调用或解析失败时的错误信息
 *
 * @note    该方法始终使用非流式模式（stream=false），
 *          因为 ReAct 智能体需要完整解析 LLM 的 JSON 输出。
 *          响应体读取上限为 256KB，通过 io.LimitReader 防止异常大响应导致 OOM。
 *
 * @code
 *   messages := []map[string]interface{}{
 *       {"role": "system", "content": "You are a helpful assistant."},
 *       {"role": "user", "content": "Hello"},
 *   }
 *   content, err := proxy.callLLM(ctx, modelCfg, messages)
 *   if err != nil {
 *       log.Printf("LLM 调用失败: %v", err)
 *       return
 *   }
 *   fmt.Println(content)
 * @endcode
 */
func (p *Proxy) callLLM(ctx context.Context, modelCfg *config.ModelConfig, messages []map[string]interface{}) (string, error) {
	reqBody := map[string]interface{}{
		"model":    modelCfg.ModelName,
		"messages": messages,
		"stream":   false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(modelCfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+modelCfg.APIKey)

	client, ok := p.clients[modelCfg.Name]
	if !ok {
		client = &http.Client{Timeout: 300 * time.Second}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", fmt.Errorf("failed to parse LLM response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response from LLM")
	}

	return result.Choices[0].Message.Content, nil
}

// ===========================================================================
// JSON 提取工具
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   从文本中提取 JSON 字符串。
 *
 *          由于 LLM 有时会将 JSON 包裹在 Markdown 代码块中
 *          （```json ... ```）而非直接输出纯 JSON，本函数提供
 *          两种提取策略作为后备解析方案。
 *
 *          处理流程：
 *          1. 查找 ```json 标记，若找到则提取其内部的 JSON 内容
 *          2. 若未找到 Markdown 代码块，则尝试从文本中查找第一个
 *             成对的大括号 { ... } 作为 JSON
 *          3. 使用深度计数器匹配大括号以正确识别嵌套 JSON
 *          4. 若两种方式都未找到，返回空字符串
 *
 * @param   text string - 可能包含 JSON 的原始文本
 * @return  string       - 提取后的纯 JSON 字符串，未找到时返回 ""
 *
 * @note    该函数仅处理 ```json 代码块和裸 { } 两种格式，
 *          不处理其他 Markdown 代码语言标签。
 *
 * @code
 *   raw := "Here is the response:\n```json\n{\"action\":{\"tool\":\"bash\"}}\n```"
 *   jsonStr := extractJSON(raw)
 *   // jsonStr == "{\"action\":{\"tool\":\"bash\"}}"
 * @endcode
 */
func extractJSON(text string) string {
	// Try to find JSON in markdown code blocks
	start := strings.Index(text, "```json")
	if start != -1 {
		start += 7
		if end := strings.Index(text[start:], "```"); end != -1 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	// Try bare { ... } extraction
	start = strings.Index(text, "{")
	if start != -1 {
		depth := 0
		for i := start; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return text[start : i+1]
				}
			}
		}
	}
	return ""
}

// ===========================================================================
// 工具调度基础设施
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   检查指定工具是否在启用列表中。
 *
 *          处理流程：
 *          1. 遍历启用的工具列表
 *          2. 按名称进行精确匹配
 *
 * @param   enabled []string - 客户端指定要启用的工具名称列表
 * @param   tool    string   - 要检查的工具名称
 * @return  bool              - true 表示工具已启用，false 表示未启用
 *
 * @code
 *   tools := []string{"read_file", "bash", "grep"}
 *   if toolEnabled(tools, "bash") {
 *       fmt.Println("bash 工具已启用")
 *   }
 * @endcode
 */
func toolEnabled(enabled []string, tool string) bool {
	for _, t := range enabled {
		if t == tool {
			return true
		}
	}
	return false
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   根据工具名称和参数生成人类可读的工具调用描述符。
 *
 *          不同的工具生成不同格式的描述：
 *          - read_file    → "读取文件: /path/to/file"
 *          - write_file   → "写入文件: /path/to/file"
 *          - bash         → "执行命令: ls -la"（超过 60 字符截断加 ...）
 *          - grep         → "搜索 'pattern' 在 /path"
 *          - glob         → "查找文件: ** / *.go"
 *          - 未知工具     → 直接返回工具名
 *
 * @param   tool string                 - 工具名称
 * @param   args map[string]interface{} - 工具参数键值对
 * @return  string                       - 人类可读的描述字符串
 *
 * @note    bash 命令的描述会截断至 60 字符防止展示过长。
 *
 * @code
 *   args := map[string]interface{}{"pattern": "func main", "path": "./src"}
 *   desc := buildDescriptor("grep", args)
 *   // desc == "搜索 'func main' 在 ./src"
 * @endcode
 */
func buildDescriptor(tool string, args map[string]interface{}) string {
	switch tool {
	case "read_file":
		return fmt.Sprintf("读取文件: %v", args["path"])
	case "write_file":
		return fmt.Sprintf("写入文件: %v", args["path"])
	case "bash":
		cmd, _ := args["command"].(string)
		if len(cmd) > 60 {
			cmd = cmd[:60] + "..."
		}
		return fmt.Sprintf("执行命令: %s", cmd)
	case "grep":
		return fmt.Sprintf("搜索 '%v' 在 %v", args["pattern"], args["path"])
	case "glob":
		return fmt.Sprintf("查找文件: %v", args["pattern"])
	default:
		return tool
	}
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   工具调用总调度器。根据工具名称路由到对应的具体执行函数。
 *
 *          处理流程：
 *          1. 根据工具名称 switch 分发到对应实现
 *          2. 支持的工具有：read_file, write_file, bash, grep, glob
 *          3. 传入工作目录和 bash 超时参数
 *          4. 未知工具返回错误
 *
 * @param   tool        string                 - 要执行的工具名称
 * @param   args        map[string]interface{} - 工具参数键值对
 * @param   workspace   string                 - 沙箱工作目录路径
 * @param   bashTimeout int                    - bash 命令超时秒数
 * @return  string  - 工具执行的输出结果
 * @return  error   - 执行失败（如未知工具、参数缺失）时的错误
 *
 * @note    该函数是工具调用的唯一入口，所有工具执行前都会经过沙箱路径校验。
 *
 * @code
 *   result, err := executeTool("read_file", map[string]interface{}{"path": "/test.txt"}, "./ws", 30)
 *   if err != nil {
 *       log.Printf("工具执行失败: %v", err)
 *   }
 *   fmt.Println(result)
 * @endcode
 */
func executeTool(tool string, args map[string]interface{}, workspace string, bashTimeout int) (string, error) {
	switch tool {
	case "read_file":
		return toolReadFile(args, workspace)
	case "write_file":
		return toolWriteFile(args, workspace)
	case "bash":
		return toolBash(args, workspace, bashTimeout)
	case "grep":
		return toolGrep(args, workspace)
	case "glob":
		return toolGlob(args, workspace)
	default:
		return "", fmt.Errorf("未知工具: %s", tool)
	}
}

// ===========================================================================
// 安全机制
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   沙箱路径校验。将用户提供的路径限制在工作目录范围内。
 *
 *          处理流程：
 *          1. 清理路径（去除 ../、./、多余斜杠等）
 *          2. 去除路径开头的 "/" 和 "\"，将绝对路径转为相对路径
 *          3. 拼接到工作目录下得到完整解析路径
 *          4. 获取工作目录和解析路径的绝对路径
 *          5. 校验解析后的绝对路径必须以前缀包含工作目录的绝对路径
 *          6. 路径越界（如 ../../etc/passwd）返回错误
 *
 * @param   workspace string - 沙箱工作目录的绝对或相对路径
 * @param   path      string - 用户要访问的文件路径
 * @return  string    - 安全校验后的完整路径
 * @return  error     - 路径越界或路径解析失败时的错误
 *
 * @note    该函数是防止目录遍历攻击的核心安全机制。
 *          所有文件类工具（read_file, write_file, grep）都通过此函数校验路径。
 *
 * @code
 *   safePath, err := sandboxPath("./agent_workspace", "/etc/passwd")
 *   // 结果解析为 ./agent_workspace/etc/passwd，但如果尝试 ../../../etc/passwd 会被拒绝
 *
 *   safePath, err := sandboxPath("./agent_workspace", "subdir/file.txt")
 *   // safePath == "agent_workspace/subdir/file.txt"
 * @endcode
 */
func sandboxPath(workspace, path string) (string, error) {
	cleaned := filepath.Clean(path)
	cleaned = strings.TrimPrefix(cleaned, "/")
	cleaned = strings.TrimPrefix(cleaned, "\\")
	resolved := filepath.Join(workspace, cleaned)

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absResolved, absWorkspace+string(filepath.Separator)) && absResolved != absWorkspace {
		return "", fmt.Errorf("路径越界: %s 不在工作目录内", path)
	}
	return resolved, nil
}

// ===========================================================================
// 工具实现：read_file
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   读取指定文件的内容。
 *
 *          处理流程：
 *          1. 从参数中提取 path 字段
 *          2. 参数校验：path 为空返回错误
 *          3. 通过 sandboxPath 进行安全路径校验
 *          4. 调用 os.ReadFile 读取文件内容
 *          5. 若内容超过 64KB，截断并追加截断提示
 *
 * @param   args      map[string]interface{} - 工具参数，需包含 "path" 键
 * @param   workspace string                 - 沙箱工作目录
 * @return  string    - 文件内容字符串
 * @return  error     - 参数缺失、路径越界或文件读取失败时的错误
 *
 * @note    文件内容读取上限为 64KB（65536 字节），
 *          超过此限制的内容会被截断并追加提示信息。
 *
 * @code
 *   content, err := toolReadFile(map[string]interface{}{"path": "config.yaml"}, "./agent_workspace")
 *   if err != nil {
 *       log.Printf("读取失败: %v", err)
 *   }
 *   fmt.Println(content)
 * @endcode
 */
func toolReadFile(args map[string]interface{}, workspace string) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", fmt.Errorf("缺少 path 参数")
	}
	safePath, err := sandboxPath(workspace, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(safePath)
	if err != nil {
		return "", err
	}
	if len(data) > 64*1024 {
		data = data[:64*1024]
		return string(data) + "\n... (内容已截断至 64KB)", nil
	}
	return string(data), nil
}

// ===========================================================================
// 工具实现：write_file
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   向指定文件写入内容。如父目录不存在则自动创建。
 *
 *          处理流程：
 *          1. 从参数中提取 path 和 content 字段
 *          2. 参数校验：path 为空返回错误
 *          3. 通过 sandboxPath 进行安全路径校验
 *          4. 创建目标文件的父目录（权限 0755）
 *          5. 写入文件内容（权限 0644）
 *          6. 返回成功信息（含文件路径和字节数）
 *
 * @param   args      map[string]interface{} - 工具参数，需包含 "path" 和 "content" 键
 * @param   workspace string                 - 沙箱工作目录
 * @return  string    - 写入成功的确认信息
 * @return  error     - 参数缺失、路径越界、目录创建或文件写入失败时的错误
 *
 * @code
 *   msg, err := toolWriteFile(
 *       map[string]interface{}{"path": "output/result.txt", "content": "Hello, World!"},
 *       "./agent_workspace",
 *   )
 *   // msg == "成功写入文件: output/result.txt (13 字节)"
 * @endcode
 */
func toolWriteFile(args map[string]interface{}, workspace string) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return "", fmt.Errorf("缺少 path 参数")
	}
	safePath, err := sandboxPath(workspace, path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(safePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	return fmt.Sprintf("成功写入文件: %s (%d 字节)", path, len(content)), nil
}

// ===========================================================================
// 工具实现：bash（Shell 命令执行）
// ===========================================================================

/*
 * bannedCommands 定义了被禁止执行的 Shell 命令关键字列表。
 *
 * 包含的危险操作：
 *   - rm -rf / rm -r   : 递归删除（防止误删工作目录外文件）
 *   - sudo              : 提权操作
 *   - chmod 777         : 权限开放
 *   - > /dev            : 设备文件写入（如 dd 填零）
 *   - mkfs              : 格式化文件系统
 *   - dd if=            : 磁盘直接读写
 *   - :(){              : Fork 炸弹
 *   - curl / wget       : 外网访问（防止数据外泄）
 *   - shutdown / reboot : 系统关机
 *   - kill / pkill      : 进程终止
 *   - chown             : 文件所有者变更
 *   - passwd            : 密码修改
 */
var bannedCommands = []string{
	"rm -rf", "rm -r", "sudo", "chmod 777",
	"> /dev", "mkfs", "dd if=", ":(){",
	"curl", "wget", "shutdown", "reboot",
	"kill", "pkill", "chown", "passwd",
}

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   在沙箱环境中执行 Shell 命令。
 *
 *          处理流程：
 *          1. 从参数中提取 command 字段
 *          2. 参数校验：command 为空返回错误
 *          3. 安全审查：检查命令是否包含 bannedCommands 中列出的危险关键字
 *          4. 路径遍历检测：检查 cd .. 和 ls .. 中是否包含多层 ../ 遍历
 *          5. 创建带超时的 context（默认 30 秒）
 *          6. 通过 exec.CommandContext 执行 sh -c <command>
 *          7. 设置工作目录为沙箱目录
 *          8. 捕获 stdout 和 stderr
 *          9. 结果拼接：stdout 在前，stderr 以 [stderr] 前缀追加
 *          10. 特殊状态处理：超时、非零退出码、无输出
 *          11. 输出截断：超过 8KB 截断并提示
 *
 * @param   args        map[string]interface{} - 工具参数，需包含 "command" 键
 * @param   workspace   string                 - 沙箱工作目录（命令将在此目录执行）
 * @param   timeoutSec  int                    - 命令超时秒数
 * @return  string    - 命令执行的输出（stdout + stderr + 状态信息）
 * @return  error     - 命令被安全策略拒绝或参数缺失时的错误
 *
 * @note    即使命令执行失败（非零退出码）也不会返回 error，
 *          而是在输出中追加退出码信息，以保证 ReAct 循环能继续。
 *          只有在安全策略拒绝、参数缺失等前置校验阶段才返回 error。
 *
 * @note    该函数使用 exec.CommandContext 而非 exec.Command，
 *          确保超时后子进程会被强制终止。
 *
 * @code
 *   result, err := toolBash(
 *       map[string]interface{}{"command": "ls -la"},
 *       "./agent_workspace",
 *       30,
 *   )
 *   if err != nil {
 *       log.Printf("命令被拒绝: %v", err)
 *   }
 *   fmt.Println(result)
 * @endcode
 */
func toolBash(args map[string]interface{}, workspace string, timeoutSec int) (string, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", fmt.Errorf("缺少 command 参数")
	}

	lowerCmd := strings.ToLower(command)
	for _, banned := range bannedCommands {
		if strings.Contains(lowerCmd, strings.ToLower(banned)) {
			return "", fmt.Errorf("命令被安全策略拒绝: 包含 '%s'", banned)
		}
	}
	if strings.Contains(command, "..") && (strings.Contains(command, "cd ") || strings.Contains(command, "ls ")) {
		// Allow cd .. and ls .. but check for path traversal patterns
		if strings.Contains(command, "../../") || strings.Contains(command, "..\\..") {
			return "", fmt.Errorf("命令被安全策略拒绝: 路径遍历")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workspace

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := stdout.String()
	if stderr.Len() > 0 {
		if result != "" {
			result += "\n"
		}
		result += "[stderr]\n" + stderr.String()
	}
	if ctx.Err() == context.DeadlineExceeded {
		return result + fmt.Sprintf("\n[命令超时 (%ds)]", timeoutSec), nil
	}
	if err != nil {
		return result + fmt.Sprintf("\n[退出码: %v]", err), nil
	}
	if result == "" {
		return "(无输出)", nil
	}
	if len(result) > 8*1024 {
		return result[:8*1024] + "\n... (输出已截断至 8KB)", nil
	}
	return result, nil
}

// ===========================================================================
// 工具实现：grep（正则搜索）
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   在指定路径下执行 grep 正则搜索。
 *
 *          处理流程：
 *          1. 从参数中提取 pattern 和 path 字段
 *          2. 参数校验：pattern 为空返回错误
 *          3. path 未指定时默认为当前目录 "."
 *          4. 通过 sandboxPath 进行安全路径校验
 *          5. 创建 10 秒超时的 context
 *          6. 执行 grep -rn -I --exclude-dir=.git <pattern> <safePath>
 *          7. 无匹配结果时返回 "(无匹配结果)"
 *          8. 输出超过 4KB 时截断并提示
 *
 * @param   args      map[string]interface{} - 工具参数，需包含 "pattern" 键，可选 "path" 键
 * @param   workspace string                 - 沙箱工作目录
 * @return  string    - grep 搜索结果
 * @return  error     - 参数缺失或路径越界时的错误
 *
 * @note    grep 命令默认参数：-r（递归）、-n（行号）、-I（忽略二进制文件）、
 *          --exclude-dir=.git（排除 .git 目录）。结果上限 4KB。
 *
 * @code
 *   result, err := toolGrep(
 *       map[string]interface{}{"pattern": "func main", "path": "./src"},
 *       "./agent_workspace",
 *   )
 *   // result 类似: "./src/main.go:42:func main() {"
 * @endcode
 */
func toolGrep(args map[string]interface{}, workspace string) (string, error) {
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)
	if pattern == "" {
		return "", fmt.Errorf("缺少 pattern 参数")
	}
	if path == "" {
		path = "."
	}

	safePath, err := sandboxPath(workspace, path)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "grep", "-rn", "-I", "--exclude-dir=.git", pattern, safePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	result := stdout.String()
	if stderr.Len() > 0 && result == "" {
		result = stderr.String()
	}
	if err != nil {
		if result == "" {
			return "(无匹配结果)", nil
		}
	}
	if result == "" {
		return "(无匹配结果)", nil
	}
	if len(result) > 4*1024 {
		return result[:4*1024] + "\n... (结果已截断至 4KB)", nil
	}
	return result, nil
}

// ===========================================================================
// 工具实现：glob（文件名模式匹配）
// ===========================================================================

/*
 * @author  chensong
 * @date    2026-04-26
 * @brief   根据 glob 模式查找匹配的文件。
 *
 *          处理流程：
 *          1. 从参数中提取 pattern 字段
 *          2. 参数校验：pattern 为空返回错误
 *          3. 将 workspace 与 pattern 拼接得到安全搜索路径
 *          4. 调用 filepath.Glob 执行文件匹配
 *          5. 无匹配时返回 "(未找到匹配文件)"
 *          6. 将匹配结果转为相对于 workspace 的路径
 *          7. 最多返回前 100 个匹配项，超出则追加提示
 *          8. 输出超过 2KB 时截断并提示
 *
 * @param   args      map[string]interface{} - 工具参数，需包含 "pattern" 键
 * @param   workspace string                 - 沙箱工作目录
 * @return  string    - 匹配的文件路径列表（每行一个）
 * @return  error     - 参数缺失时的错误
 *
 * @note    pattern 参数支持 Go 的 filepath.Glob 语法：
 *          * 匹配任意非分隔符字符，** 不递归（使用单层匹配）。
 *          结果上限：100 个文件 + 2KB 输出。
 *
 * @code
 *   result, err := toolGlob(
 *       map[string]interface{}{"pattern": "*.go"},
 *       "./agent_workspace",
 *   )
 *   // result 类似: "main.go\nutils.go\nagent.go"
 * @endcode
 */
func toolGlob(args map[string]interface{}, workspace string) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("缺少 pattern 参数")
	}

	safePattern := filepath.Join(workspace, pattern)
	matches, err := filepath.Glob(safePattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "(未找到匹配文件)", nil
	}

	// Show relative paths
	var lines []string
	for i, m := range matches {
		rel, _ := filepath.Rel(workspace, m)
		if rel == "" {
			rel = m
		}
		lines = append(lines, rel)
		if i >= 100 {
			lines = append(lines, fmt.Sprintf("... 共 %d 个文件，仅显示前 100 个", len(matches)))
			break
		}
	}
	result := strings.Join(lines, "\n")
	if len(result) > 2*1024 {
		return result[:2*1024] + "\n... (结果已截断至 2KB)", nil
	}
	return result, nil
}
