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

// --- SSE event structures ---

type agentThinkingEvent struct {
	Iteration int    `json:"iteration"`
	Thought   string `json:"thought"`
}

type agentActionEvent struct {
	Iteration  int    `json:"iteration"`
	Tool       string `json:"tool"`
	Arguments  string `json:"arguments"`
	Descriptor string `json:"descriptor"`
}

type agentObservationEvent struct {
	Iteration int    `json:"iteration"`
	Result    string `json:"result"`
	Truncated bool   `json:"truncated"`
}

type agentResponseEvent struct {
	Content string `json:"content"`
}

type agentDoneEvent struct {
	Iterations int            `json:"iterations"`
	TokensUsed map[string]int `json:"tokens_used,omitempty"`
}

type agentErrorEvent struct {
	Message string `json:"message"`
}

// --- Agent request ---

type agentChatRequest struct {
	Model   string   `json:"model"`
	Message string   `json:"message"`
	Tools   []string `json:"tools"`
}

// --- LLM response parsing ---

type agentLLMAction struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
}

type agentLLMResponse struct {
	Thought     string          `json:"thought"`
	Action      *agentLLMAction `json:"action,omitempty"`
	FinalAnswer string          `json:"final_answer,omitempty"`
}

// --- ReAct system prompt ---

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

// --- Handler ---

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

func (p *Proxy) findModelConfig(name string) *config.ModelConfig {
	for _, m := range p.config.Models {
		if m.Name == name {
			return &m
		}
	}
	return nil
}

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

// --- Tool execution ---

func toolEnabled(enabled []string, tool string) bool {
	for _, t := range enabled {
		if t == tool {
			return true
		}
	}
	return false
}

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

var bannedCommands = []string{
	"rm -rf", "rm -r", "sudo", "chmod 777",
	"> /dev", "mkfs", "dd if=", ":(){",
	"curl", "wget", "shutdown", "reboot",
	"kill", "pkill", "chown", "passwd",
}

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
