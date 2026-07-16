package proxy

import "bytes"

// extractJSONFromLine extracts a JSON object from a single SSE line.
//
// Handles the following input formats:
// - "data: {"full JSON}""  →  {"full JSON"}
// - "{"full JSON"}""       →  {"full JSON"}
// Returns nil if no valid JSON object boundary ({…}) can be found.
//
// @author chensong @date 2026-07-13
func extractJSONFromLine(line []byte) []byte {
	// Strip "data:" prefix if present
	data := line
	if bytes.HasPrefix(line, []byte("data:")) {
		data = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	}

	// Find first { and last } to extract JSON.
	// Note: In standard JSON, literal "}" inside string values is escaped as \},
	// so using LastIndex on the raw bytes is safe for well-formed JSON.
	if idxStart := bytes.Index(data, []byte("{")); idxStart >= 0 {
		if idxEnd := bytes.LastIndex(data, []byte("}")); idxEnd > idxStart {
			return data[idxStart : idxEnd+1]
		}
	}
	return nil
}

// extractTokenFromChunk updates the provided tokens map from a single SSE chunk.
//
// Behavior depends on chunk format:
//   - OpenAI usage:  each chunk's usage contains the **cumulative** token count
//     for the entire stream so far. When usage is present, we overwrite (take max)
//     rather than accumulate to avoid double-counting.
//   - llama.cpp timings: timings appear on every chunk with cumulative counts.
//     We take the max of the existing accumulated value and the new value.
//   - Anthropic usage: same as OpenAI — overwrite with max.
//
// @param tokens   map to update (created on first call if nil).
// @param chunk    parsed JSON from a single SSE chunk.
//
// @note  For standard usage objects, OpenAI/Anthropic/llama.cpp all return
//       cumulative counts per chunk. Taking the max across chunks yields the
//       final total without double-counting.
//
// @note  llama.cpp timings are read ONLY when usage is absent.
func extractTokenFromChunk(tokens map[string]int, chunk map[string]interface{}) {
	if tokens == nil {
		tokens = make(map[string]int)
	}

	// --- Path 1: Standard usage object (OpenAI / normalized llama.cpp / Anthropic) ---
	if usageRaw, exists := chunk["usage"]; exists {
		if u, ok := usageRaw.(map[string]interface{}); ok {
			updateTokenMax(tokens, "prompt_tokens", u, "prompt_tokens", "input_tokens")
			updateTokenMax(tokens, "completion_tokens", u, "completion_tokens", "output_tokens")
			updateTokenMax(tokens, "total_tokens", u, "total_tokens")
			return // usage found — do not also read timings
		}
	}

	// --- Path 2: llama.cpp raw timings (fallback when no usage object) ---
	if timingsRaw, exists := chunk["timings"]; exists {
		if t, ok := timingsRaw.(map[string]interface{}); ok {
			updateTokenMax(tokens, "prompt_tokens", t, "prompt_n")
			updateTokenMax(tokens, "completion_tokens", t, "predicted_n")
			updateTokenMax(tokens, "total_tokens", t, "total")
		}
	}
}

// updateTokenMax reads numeric values from src using the given key names (in priority order)
// and updates tokens[key] to the maximum of its current value and the new value.
// This is the core helper for both accumulate-mode (+) and max-mode (=) token extraction.
func updateTokenMax(tokens map[string]int, key string, src map[string]interface{}, names ...string) {
	for _, name := range names {
		if val, exists := src[name]; exists {
			if v, ok := asInt(val); ok {
				if v > tokens[key] {
					tokens[key] = v
				}
			}
			return // use first available field name only
		}
	}
}

// normalizeTokenKeys 将不同上游的 token 字段名统一为 OpenAI 标准格式。
//
// 处理流程：
//  1. 将 input_tokens 映射到 prompt_tokens（仅当 prompt_tokens 不存在时）
//  2. 将 output_tokens 映射到 completion_tokens（仅当 completion_tokens 不存在时）
//  3. 如果 total_tokens 为 0 且 prompt_tokens/completion_tokens 已知，则自动计算
//
// @param usage map[string]interface{} 原始 usage map，可能包含 input_tokens/output_tokens
// @return map[string]int 规范化后的 token 统计 {prompt_tokens, completion_tokens, total_tokens}
// @note 此函数不会修改输入的 usage map，而是返回一个新的 map
func normalizeTokenKeys(usage map[string]interface{}) map[string]int {
	tokens := make(map[string]int)
	if usage == nil {
		return tokens
	}

	// Extract prompt tokens: prefer prompt_tokens, fallback to input_tokens
	if val, exists := usage["prompt_tokens"]; exists {
		if v, ok := asInt(val); ok {
			tokens["prompt_tokens"] = v
		}
	} else if val, exists := usage["input_tokens"]; exists {
		if v, ok := asInt(val); ok {
			tokens["prompt_tokens"] = v
		}
	}

	// Extract completion tokens: prefer completion_tokens, fallback to output_tokens
	if val, exists := usage["completion_tokens"]; exists {
		if v, ok := asInt(val); ok {
			tokens["completion_tokens"] = v
		}
	} else if val, exists := usage["output_tokens"]; exists {
		if v, ok := asInt(val); ok {
			tokens["completion_tokens"] = v
		}
	}

	// Extract total_tokens if present
	if val, exists := usage["total_tokens"]; exists {
		if v, ok := asInt(val); ok {
			tokens["total_tokens"] = v
		}
	}

	// Auto-calculate total_tokens if missing
	if tokens["total_tokens"] == 0 {
		tokens["total_tokens"] = tokens["prompt_tokens"] + tokens["completion_tokens"]
	}

	return tokens
}
