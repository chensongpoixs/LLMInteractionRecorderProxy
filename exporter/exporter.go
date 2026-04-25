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
	Thinking   string `json:"thinking"`
	Solution   string `json:"solution"`
	Difficulty string `json:"difficulty"`
	Category   string `json:"category"`
	Timestamp  string `json:"timestamp"`
	Hash       string `json:"hash"`
}

var systemReminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// ExportDay aggregates all request logs under storageDir/YYYYMMDD into one JSONL file.
// outputPath is the full path of the target file (e.g. .../Opus-4.6-Reasoning-3000x-filtered-20260425.jsonl).
func ExportDay(storageDir string, day time.Time, outputPath string) (n int, err error) {
	// Use calendar day of `day` in its Location (callers should pass a wall-clock day, e.g. noon).
	dateStr := day.Format("20060102")
	dayDir := filepath.Join(storageDir, dateStr)
	if st, e := os.Stat(dayDir); e != nil || !st.IsDir() {
		return 0, fmt.Errorf("day directory not found: %s", dayDir)
	}

	// Collect request log file paths (exclude streams/)
	var logFiles []string
	err = filepath.WalkDir(dayDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
	if err != nil {
		return 0, err
	}
	sort.Strings(logFiles)

	// Pre-load stream chunks for this day (keyed by request id from proxy log)
	streamIndex, sErr := buildStreamIndex(filepath.Join(dayDir, "streams"))
	if sErr != nil {
		// non-fatal: streaming aggregation may be empty
		_ = sErr
	}

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
		// allow long lines
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
			// keep stream or successful entries; drop hard failures with no data
			if rec.Error != "" && rec.ResponseBody == nil && !rec.Stream {
				continue
			}

			row, ok := buildRow(&rec, streamIndex)
			if !ok {
				continue
			}
			// de-dup by hash
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
	problem := extractProblem(rec.RequestBody)
	problem = strings.TrimSpace(systemReminderRE.ReplaceAllString(problem, ""))
	problem = strings.TrimSpace(problem)
	if problem == "" {
		return DatasetRow{}, false
	}

	thinking, solution := extractThinkingSolution(rec, streamIndex)

	row := DatasetRow{
		ID:         fmt.Sprintf("proxy_%s", rec.ID),
		Problem:    problem,
		Thinking:   thinking,
		Solution:   solution,
		Difficulty: "medium",
		Category:   inferCategory(problem, solution, thinking),
		Timestamp:  rec.Timestamp.UTC().Format(time.RFC3339Nano),
	}
	// If timestamp invalid / zero, use "now" replacement
	if strings.HasPrefix(row.Timestamp, "0001-") {
		row.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	row.Hash = recordHash(row.Problem, row.Thinking, row.Solution)
	return row, true
}

func extractProblem(req map[string]interface{}) string {
	if msgs, ok := req["messages"].([]interface{}); ok {
		return joinUserMessages(msgs)
	}
	if s, ok := req["system"].(string); ok && s != "" {
		return s
	}
	// Completions
	if s, ok := req["prompt"].(string); ok {
		return s
	}
	// Single user content (some proxies)
	if s, ok := req["input"].(string); ok {
		return s
	}
	return ""
}

func joinUserMessages(msgs []interface{}) string {
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
	if rec.ResponseBody != nil {
		thinking, solution = fromOpenAIStyleResponse(rec.ResponseBody)
		if solution != "" || thinking != "" {
			return thinking, solution
		}
		thinking, solution = fromAnthropicStyleResponse(rec.ResponseBody)
		if solution != "" || thinking != "" {
			return thinking, solution
		}
	}
	if rec.Stream {
		agg := aggregateFromStreamChunks(streamIndex[rec.ID])
		th, sol := parseOpenAIStreamAggregate(agg)
		return th, sol
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
	// Chunks are raw slices from upstream; concatenate in file order
	return strings.Join(chunks, "\n")
}

func inferCategory(problem, solution, thinking string) string {
	low := strings.ToLower(problem + "\n" + solution + "\n" + thinking)
	if strings.Contains(low, "```") || strings.Contains(low, "def ") || strings.Contains(low, "function ") ||
		strings.Contains(low, "#include") {
		return "code"
	}
	// heuristics from reference dataset (often "math" even for non-math; default medium + math is common)
	if strings.Count(low, "$") > 2 || strings.ContainsAny(low, "∫∑√×÷") {
		return "math"
	}
	return "math"
}

func recordHash(problem, thinking, solution string) string {
	h := sha256.Sum256([]byte(problem + "\n---\n" + thinking + "\n---\n" + solution))
	hexs := hex.EncodeToString(h[:])
	if len(hexs) > 16 {
		return hexs[:16]
	}
	return hexs
}
