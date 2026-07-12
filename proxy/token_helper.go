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

// extractTokenFromChunk extracts token counts from a single SSE chunk (parsed JSON).
// It accumulates into the provided tokens map (pass a new map on first call).
// Handles both OpenAI format (prompt_tokens/completion_tokens) and
// llama.cpp/Anthropic format (input_tokens/output_tokens).
//
// @note  For each dimension (prompt/completion/total), only the first
//       available field name is used to avoid double-counting when an
//       upstream returns both canonical and legacy names.
//
// OpenAI:   {"usage":{"prompt_tokens":N,"completion_tokens":M,"total_tokens":T}}
// llama.cpp:{"usage":{"input_tokens":N,"output_tokens":M,"total_tokens":T}}
// Anthropic:{"usage":{"input_tokens":N,"output_tokens":M}}
func extractTokenFromChunk(tokens map[string]int, chunk map[string]interface{}) {
	if tokens == nil {
		tokens = make(map[string]int)
	}
	if usageRaw, exists := chunk["usage"]; exists {
		if u, ok := usageRaw.(map[string]interface{}); !ok {
			return
		} else {
			// Prompt tokens: prefer prompt_tokens (OpenAI standard), fall back to input_tokens
			if val, exists := u["prompt_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["prompt_tokens"] += v
				}
			} else if val, exists := u["input_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["prompt_tokens"] += v
				}
			}
			// Completion tokens: prefer completion_tokens (OpenAI standard), fall back to output_tokens
			if val, exists := u["completion_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["completion_tokens"] += v
				}
			} else if val, exists := u["output_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["completion_tokens"] += v
				}
			}
			// Total tokens: only one canonical name
			if val, exists := u["total_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["total_tokens"] += v
				}
			}
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
