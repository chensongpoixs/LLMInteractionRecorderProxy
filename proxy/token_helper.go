/******************************************************************************
 *  Copyright (c) 2025 The LLM Interaction Recorder & Proxy — 大模型交互日志与数据沉淀代理 project authors. All Rights Reserved.
 *
 *  Please visit https://chensongpoixs.github.io for detail
 *
 *  Use of this source code is governed by a BSD-style license
 *  that can be found in the LICENSE file in the root of the source
 *  tree. An additional intellectual property rights grant can be found in the
 *  PATENTS file in the root of the source tree.
 ******************************************************************************/

package proxy

import "bytes"

// extractJSONFromLine 从一行文本中提取 JSON 部分（不含 data: 前缀）。
// 如果行中包含 JSON 数据，返回字节切片；否则返回 nil。
// @author chensong @date 2026-07-13
func extractJSONFromLine(line []byte) []byte {
	// Strip "data:" prefix if present
	data := line
	if bytes.HasPrefix(line, []byte("data:")) {
		data = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	}

	// Find first { and last } to extract JSON
	if idxStart := bytes.Index(data, []byte("{")); idxStart >= 0 {
		if idxEnd := bytes.LastIndex(data, []byte("}")); idxEnd > idxStart {
			return data[idxStart : idxEnd+1]
		}
	}
	return nil
}

// extractTokenFromChunk extracts token counts from a single SSE chunk (parsed JSON).
// It accumulates into the provided tokens map (pass a new map on first call).
// Handles both OpenAI format (usage.prompt_tokens) and llama.cpp (usage.input_tokens).
func extractTokenFromChunk(tokens map[string]int, chunk map[string]interface{}) {
	if tokens == nil {
		tokens = make(map[string]int)
	}
	// OpenAI format: {"usage": {"prompt_tokens": N, "completion_tokens": M, "total_tokens": T}}
	if usageRaw, exists := chunk["usage"]; exists {
		if u, ok := usageRaw.(map[string]interface{}); ok {
			if val, exists := u["prompt_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["prompt_tokens"] += v
				}
			}
			if val, exists := u["input_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["prompt_tokens"] += v
				}
			}
			if val, exists := u["completion_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["completion_tokens"] += v
				}
			}
			if val, exists := u["output_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["completion_tokens"] += v
				}
			}
			if val, exists := u["total_tokens"]; exists {
				if v, ok := asInt(val); ok {
					tokens["total_tokens"] += v
				}
			}
		}
	}
	return
}
