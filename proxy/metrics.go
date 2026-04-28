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
	"fmt"
	"net/http"
	"sync"
	"time"
)

/*
 * @brief 环形缓冲区容量上限
 *
 * 控制延迟样本和响应体大小样本的最大存储数量。
 * 当样本数达到此上限后，新的记录会覆盖最旧的记录（环形覆盖）。
 *
 * @note 10000 条样本在默认采样频率下可覆盖绝大多数使用场景，
 *       如需更高精度可调整此常量，但需注意内存增长
 */
const maxMetricSamples = 10000 // ring buffer capacity for latencies and response sizes

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   代理请求指标采集器
 *
 * 使用有界环形缓冲区存储请求延迟和响应体大小样本，避免内存无限增长。
 * 按路径和状态码维度统计请求总数和错误数，支持计算平均延迟、错误率、
 * 总字节数、运行时长等聚合指标。
 *
 * 字段说明:
 *   - mu:            读写锁，保护所有字段的并发访问。读取指标时使用 RLock，写入时使用 Lock
 *   - requestCount:  按路径+状态码分组的请求计数，key 格式为 "{path}_{status_code}"
 *   - errorCount:    按路径+状态码分组的错误计数（statusCode >= 400），key 格式同上
 *   - latencies:     环形缓冲区，存储最近 maxMetricSamples 条请求的延迟时间
 *   - responseSizes: 环形缓冲区，存储最近 maxMetricSamples 条请求的响应体字节数
 *   - ringWrite:     环形缓冲区的下一个写入位置索引，写入后自增并在达到上限时归零
 *   - ringCount:     环形缓冲区中有效条目的数量（0 ~ maxMetricSamples）
 *   - totalBytes:    累计发送的总字节数，单调递增
 *   - startTime:     指标采集器的启动时间，用于计算 uptime
 *
 * @note   该采集器只维护最近 maxMetricSamples 条请求的延迟/大小样本，
 *         因此 GetMetrics 返回的 avg_latency 是基于最近样本的近似值，
 *         而非全量历史数据的精确平均值
 */
type Metrics struct {
	mu            sync.RWMutex
	requestCount  map[string]int
	errorCount    map[string]int
	latencies     []time.Duration // ring buffer
	responseSizes []int64         // ring buffer
	ringWrite     int             // next write position in ring
	ringCount     int             // number of valid entries (≤ maxMetricSamples)
	totalBytes    int64
	startTime     time.Time
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   创建新的指标采集器实例
 *
 * 处理流程:
 *   1. 初始化 requestCount 和 errorCount 为空的 map[string]int
 *   2. 分配容量为 maxMetricSamples 的延迟切片和响应体大小切片
 *   3. 记录当前时间作为 startTime，用于后续计算 uptime
 *
 * @return  *Metrics  初始化完成的指标采集器指针
 *
 * @note   调用方应在代理启动时调用一次，并将实例传给各请求处理器
 */
// NewMetrics creates a new metrics collector
func NewMetrics() *Metrics {
	return &Metrics{
		requestCount:  make(map[string]int),
		errorCount:    make(map[string]int),
		latencies:     make([]time.Duration, maxMetricSamples),
		responseSizes: make([]int64, maxMetricSamples),
		startTime:     time.Now(),
	}
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   记录一条请求指标（不含响应体大小）
 *
 * 处理流程:
 *   1. 获取写锁（Lock），保护并发写入
 *   2. 构造 key = "{path}_{statusCode}"，在 requestCount 中递增计数
 *   3. 如果 statusCode >= 400，在 errorCount 中递增计数
 *   4. 将本次请求的延迟写入环形缓冲区 ringWrite 位置
 *   5. 将响应体大小记为 0（实际字节数在 RecordBytesSent 中更新）
 *   6. 环形缓冲区指针 ringWrite 前移，到达上限时归零
 *   7. 如果 ringCount 未达到上限，递增 ringCount
 *   8. 释放写锁（defer Unlock）
 *
 * @param   path       string        请求路径（如 "/v1/chat/completions"）
 * @param   statusCode int           HTTP 响应状态码
 * @param   latency    time.Duration 从接收请求到完成响应的总耗时
 *
 * @note   适用于不计响应体大小的简单计数场景（如错误响应、重定向等）。
 *         如需记录响应体大小，请使用 RecordBytesSent 方法
 */
func (m *Metrics) RecordRequest(path string, statusCode int, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s_%d", path, statusCode)
	m.requestCount[key]++

	if statusCode >= 400 {
		m.errorCount[key]++
	}

	m.latencies[m.ringWrite] = latency
	m.responseSizes[m.ringWrite] = 0
	m.ringWrite++
	if m.ringCount < maxMetricSamples {
		m.ringCount++
	}
	if m.ringWrite >= maxMetricSamples {
		m.ringWrite = 0
	}
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   记录一条请求指标（含响应体大小）
 *
 * 处理流程:
 *   1. 获取写锁（Lock），保护并发写入
 *   2. 构造 key = "{path}_{statusCode}"，在 requestCount 中递增计数
 *   3. 如果 statusCode >= 400，在 errorCount 中递增计数
 *   4. 将本次请求的延迟写入环形缓冲区 ringWrite 位置
 *   5. 将响应体字节数写入环形缓冲区 ringWrite 位置
 *   6. 累加 totalBytes（用于计算总字节数）
 *   7. 环形缓冲区指针 ringWrite 前移，到达上限时归零
 *   8. 如果 ringCount 未达到上限，递增 ringCount
 *   9. 释放写锁（defer Unlock）
 *
 * @param   path       string        请求路径（如 "/v1/chat/completions"）
 * @param   statusCode int           HTTP 响应状态码
 * @param   latency    time.Duration 从接收请求到完成响应的总耗时
 * @param   bytesSent  int64        响应体字节数
 *
 * @note   该方法和 RecordRequest 的核心区别在于：
 *         1) 记录了实际的 bytesSent（非 0）
 *         2) 累加了 totalBytes
 *         其他逻辑完全一致。调用方应在获得实际字节数后使用此方法
 */
// RecordBytesSent records the response body size for the most recent request.
// Call this after RecordRequest when the actual byte count is known.
func (m *Metrics) RecordBytesSent(path string, statusCode int, latency time.Duration, bytesSent int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s_%d", path, statusCode)
	m.requestCount[key]++

	if statusCode >= 400 {
		m.errorCount[key]++
	}

	m.latencies[m.ringWrite] = latency
	m.responseSizes[m.ringWrite] = bytesSent
	m.totalBytes += bytesSent
	m.ringWrite++
	if m.ringCount < maxMetricSamples {
		m.ringCount++
	}
	if m.ringWrite >= maxMetricSamples {
		m.ringWrite = 0
	}
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   获取当前聚合指标的快照
 *
 * 处理流程:
 *   1. 获取读锁（RLock），允许并发读取
 *   2. 遍历 requestCount 计算 totalRequests（总请求数）
 *   3. 遍历 errorCount 计算 totalErrors（总错误数）
 *   4. 遍历环形缓冲区中的有效样本计算平均延迟（avgLatency）
 *   5. 计算错误率（errorRate = totalErrors / totalRequests）
 *   6. 计算运行时长（uptime = time.Since(startTime)）
 *   7. 组装包含所有聚合指标的 map[string]interface{} 并返回
 *   8. 释放读锁（defer RUnlock）
 *
 * @return  map[string]interface{}  聚合指标数据，包含以下键:
 *         - total_requests:     总请求数（int）
 *         - total_errors:       总错误数（int）
 *         - error_rate:         错误率（float64，范围 0.0 ~ 1.0）
 *         - avg_latency:        平均延迟（string，如 "123ms"）
 *         - uptime:             运行时长（string，如 "2h30m"）
 *         - total_bytes:        累计发送字节数（int64）
 *         - by_endpoint:        按端点分组的请求计数（map[string]int）
 *         - errors_by_endpoint: 按端点分组的错误计数（map[string]int）
 *
 * @note   平均延迟仅基于环形缓冲区中的最近 maxMetricSamples 条样本，
 *         如果 ringCount 为 0（无任何请求记录），avg_latency 为 "0s"
 */
func (m *Metrics) GetMetrics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalRequests := 0
	totalErrors := 0
	for _, v := range m.requestCount {
		totalRequests += v
	}
	for _, v := range m.errorCount {
		totalErrors += v
	}

	avgLatency := time.Duration(0)
	if m.ringCount > 0 {
		sum := time.Duration(0)
		for i := 0; i < m.ringCount; i++ {
			sum += m.latencies[i]
		}
		avgLatency = sum / time.Duration(m.ringCount)
	}

	errRate := 0.0
	if totalRequests > 0 {
		errRate = float64(totalErrors) / float64(totalRequests)
	}

	return map[string]interface{}{
		"total_requests":     totalRequests,
		"total_errors":       totalErrors,
		"error_rate":         errRate,
		"avg_latency":        avgLatency.String(),
		"uptime":             time.Since(m.startTime).String(),
		"total_bytes":        m.totalBytes,
		"by_endpoint":        m.requestCount,
		"errors_by_endpoint": m.errorCount,
	}
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   通过 HTTP 暴露 Prometheus 风格的文本格式指标
 *
 * 处理流程:
 *   1. 设置 Content-Type 为 text/plain
 *   2. 调用 GetMetrics 获取当前聚合指标快照
 *   3. 按 Prometheus 文本格式输出总体指标（# 开头表示注释行）
 *      - Total Requests（总请求数）
 *      - Total Errors（总错误数）
 *      - Error Rate（错误率）
 *      - Average Latency（平均延迟）
 *      - Uptime（运行时长）
 *      - Total Bytes Sent（总发送字节数）
 *   4. 输出按端点分组的请求计数明细
 *
 * @param   w http.ResponseWriter  HTTP 响应写入器
 * @param   r *http.Request    客户端 HTTP 请求
 *
 * @note   该处理器注册在 /metrics 路径上，输出格式兼容 Prometheus 的
 *         text/plain 抓取模式，但并非标准的 Prometheus 指标格式，
 *         而是简化的键值对格式，便于阅读和调试
 */
// ServeHTTP exposes metrics via HTTP
func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	metrics := m.GetMetrics()

	fmt.Fprintln(w, "# Proxy Metrics")
	fmt.Fprintf(w, "# Total Requests: %v\n", metrics["total_requests"])
	fmt.Fprintf(w, "# Total Errors: %v\n", metrics["total_errors"])
	fmt.Fprintf(w, "# Error Rate: %v\n", metrics["error_rate"])
	fmt.Fprintf(w, "# Average Latency: %v\n", metrics["avg_latency"])
	fmt.Fprintf(w, "# Uptime: %v\n", metrics["uptime"])
	fmt.Fprintf(w, "# Total Bytes Sent: %v\n", metrics["total_bytes"])

	if byEndpoint, ok := metrics["by_endpoint"].(map[string]int); ok {
		fmt.Fprintln(w, "\n# By Endpoint:")
		for k, v := range byEndpoint {
			fmt.Fprintf(w, "%s %d\n", k, v)
		}
	}
}
