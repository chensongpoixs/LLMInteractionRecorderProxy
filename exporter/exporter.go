package exporter

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"proxy-llm/storage"
)

// DatasetRow matches the Opus-style Reasoning JSONL record shape.
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

// MessagesRow is the OpenAI fine-tuning compatible format.
type MessagesRow struct {
	Messages []MessageEntry `json:"messages"`
}

// MessageEntry is a single message in the OpenAI format.
type MessageEntry struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
}

var systemReminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// ExportDay aggregates all request logs under storageDir/YYYYMMDD into one JSONL file
// in the improved reasoning format.
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

// ExportMessagesDay exports in OpenAI messages format (fine-tuning compatible).
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

// collectLogFiles returns all .jsonl log files excluding stream chunk files under streams/.
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

// buildStreamIndex returns reqID -> ordered chunks of raw SSE.
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

func buildRow(rec *storage.RequestLog, streamIndex map[string][]string) (DatasetRow, bool) {
	if rec.RequestBody == nil {
		return DatasetRow{}, false
	}
	problem := extractProblem(rec.RequestBody, rec.SystemPrompt)
	problem = strings.TrimSpace(systemReminderRE.ReplaceAllString(problem, ""))
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

	userContent := extractProblem(rec.RequestBody, rec.SystemPrompt)
	userContent = strings.TrimSpace(systemReminderRE.ReplaceAllString(userContent, ""))
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

func extractProblem(req map[string]interface{}, systemPrompt string) string {
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

func aggregateFromStreamChunks(chunks []string) string {
	if len(chunks) == 0 {
		return ""
	}
	return strings.Join(chunks, "\n")
}

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

func inferCategory(problem, solution, thinking string) string {
	low := strings.ToLower(problem + "\n" + solution + "\n" + thinking)
	if strings.Contains(low, "def ") || strings.Contains(low, "function ") ||
		strings.Contains(low, "#include") || strings.Contains(low, "import ") ||
		strings.Contains(low, "class ") || strings.Contains(low, "interface ") {
		return "code"
	}
	if strings.Count(low, "$") > 2 || strings.ContainsAny(low, "∫∑√×÷") {
		return "math"
	}
	return "general"
}

func recordHash(problem, thinking, solution string) string {
	h := sha256.Sum256([]byte(problem + "\n---\n" + thinking + "\n---\n" + solution))
	hexs := hex.EncodeToString(h[:])
	if len(hexs) > 16 {
		return hexs[:16]
	}
	return hexs
}
