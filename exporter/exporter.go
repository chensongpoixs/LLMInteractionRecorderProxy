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

var SystemReminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

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

// ──────────────────────────────────────────────────────────────────────────────
// Industry-standard dataset export: prompts / responses / revisions / feedback
// ──────────────────────────────────────────────────────────────────────────────

// PromptRecord represents a single user prompt in the prompts/ directory.
type PromptRecord struct {
	ID           string `json:"id"`
	Prompt       string `json:"prompt"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	Language     string `json:"language"`
	TaskCategory string `json:"task_category"`
	TokenCount   int    `json:"token_count"`
	Timestamp    string `json:"timestamp"`
}

// ResponseRecord represents model raw output in the responses/ directory.
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

// RevisionRecord tracks multi-turn or repeated-prompt revisions for revisions/.
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

// FeedbackRecord provides auto-inferred quality signals for feedback/.
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

// DatasetStats holds aggregate statistics after export.
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
	minPromptLength   = 5   // minimum user prompt length after cleaning
	minResponseLength = 5   // minimum response/thinking length
)

// ExportDatasetDay filters and exports one day of logs into the industry-standard
// 4-directory layout: prompts/, responses/, revisions/, feedback/ + metadata.csv.
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

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

func parseLatency(raw string) int64 {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return d.Milliseconds()
}

// writeDatasetMetadataCSV generates metadata.csv for the dataset directory.
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
