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
	"sort"
	"strconv"
	"strings"

	"proxy-llm/logger"
)

/*
 * @brief 默认的上游日志请求/响应体截断上限（字节）
 *
 * 单条日志中请求体或响应体的最大记录字节数。
 * 超过此限制的内容将被截断并附加 "... [truncated, total N bytes]" 标记。
 *
 * @note 设置为 256KB 可在日志可读性和体积之间取得平衡，
 *       对超大请求/响应体（如多模态请求中的图片 base64）尤为关键
 */
const defaultUpstreamLogMaxBytes = 256 * 1024

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   判断是否应记录上游 HTTP 日志
 *
 * 处理流程:
 *   1. 检查 p.config 是否为 nil（配置未加载）
 *   2. 检查 p.config.Logging.UpstreamHTTPlog 字段的值
 *   3. 同时满足配置非空且 UpstreamHTTPlog 为 true 时返回 true
 *
 * @return  bool  true 表示应记录上游 HTTP 日志，false 表示不记录
 *
 * @note   该方法被 logHTTPClientToProxy、logHTTPOutgoingUpstream、
 *         logHTTPUpstreamResponseFull、logHTTPUpstreamResponseStreamMeta
 *         四个方法调用，作为日志输出的统一开关
 */
func (p *Proxy) shouldLogUpstreamHTTP() bool {
	return p.config != nil && p.config.Logging.UpstreamHTTPlog
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   获取上游日志的截断上限（字节数）
 *
 * 处理流程:
 *   1. 如果 p.config 为 nil，返回默认值 defaultUpstreamLogMaxBytes
 *   2. 读取 p.config.Logging.UpstreamLogMaxBytes
 *   3. 如果 UpstreamLogMaxBytes > 0，返回该值，否则返回默认值
 *
 * @return  int  截断上限字节数，最小为 defaultUpstreamLogMaxBytes（256KB）
 *
 * @note   返回值 > 0 是调用方 truncateForLog 的合法入参，
 *         UpstreamLogMaxBytes 为 0 或负数时退回默认值
 */
func (p *Proxy) upstreamLogMax() int {
	if p.config == nil {
		return defaultUpstreamLogMaxBytes
	}
	n := p.config.Logging.UpstreamLogMaxBytes
	if n > 0 {
		return n
	}
	return defaultUpstreamLogMaxBytes
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   按指定最大长度截断字符串，用于控制日志中请求/响应体的大小
 *
 * 处理流程:
 *   1. 如果 len(s) <= max，返回原字符串（无需截断）
 *   2. 如果 len(s) > max，截取前 max 个字节
 *   3. 在截断后的字符串末尾附加 "... [truncated, total N bytes]" 标记
 *      （N 为原始字符串的总字节数）
 *
 * @param   s   string  待截断的原始字符串
 * @param   max int     最大保留字节数
 *
 * @return  string  截断后的字符串。若 s 未超过 max 则返回原串，
 *                  否则返回 "前max字节\n... [truncated, total 原始字节数 bytes]"
 *
 * @note   截断标记使用 strconv.Itoa 格式化原始字节数，避免使用 fmt.Sprintf 的反射开销
 */
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... [truncated, total " + strconv.Itoa(len(s)) + " bytes]"
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   对 HTTP 头部的敏感值进行脱敏处理
 *
 * 处理流程:
 *   1. 将头部名称转为小写进行比较（大小写不敏感匹配）
 *   2. 如果头部名称为 "authorization":
 *      a. 如果值以 "bearer "（大小写不敏感）开头，返回 "Bearer <redacted len=N>"（N 为原值长度）
 *      b. 否则返回 "<redacted len=N>"
 *   3. 如果头部名称为 "x-api-key":
 *      返回 "<redacted len=N>"（N 为原值长度）
 *   4. 其他头部直接返回原始值，不做脱敏
 *
 * @param   name  string  HTTP 头部名称（如 "Authorization", "X-API-Key"）
 * @param   value string  HTTP 头部原始值
 *
 * @return  string  脱敏后的头部值。需要脱敏的头部仅暴露原值长度信息；
 *                  不需要脱敏的头部返回原始值
 *
 * @note   脱敏后仍然保留原值长度信息，便于运维排查异常长度的 API Key
 *         Bearer token 脱敏后保留前缀 "Bearer " 标识，便于识别认证类型
 */
func redactHeaderValue(name, value string) string {
	ln := strings.ToLower(name)
	if ln == "authorization" {
		low := strings.ToLower(value)
		if strings.HasPrefix(low, "bearer ") {
			return "Bearer <redacted len=" + strconv.Itoa(len(value)) + ">"
		}
		return "<redacted len=" + strconv.Itoa(len(value)) + ">"
	}
	if ln == "x-api-key" {
		return "<redacted len=" + strconv.Itoa(len(value)) + ">"
	}
	return value
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   格式化 HTTP 头部为稳定的多行字符串
 *
 * 处理流程:
 *   1. 如果 h 为 nil，直接返回空字符串
 *   2. 提取所有头部名称到 keys 切片，按字母序排序（确保输出稳定）
 *   3. 遍历排序后的头部名称:
 *      a. 对每个名称下的每个值调用 redactHeaderValue 进行脱敏
 *      b. 格式化为 "  名称: 脱敏后的值\n" 追加到 strings.Builder
 *   4. 返回构建好的多行字符串
 *
 * @param   h  http.Header  待格式化的 HTTP 头部
 *
 * @return  string  格式化的头部字符串，每行以两个空格缩进，格式为:
 *                  "  Header-Name: value\n"
 *                  若 h 为 nil 则返回 ""
 *
 * @note   使用 sort.Strings 确保输出稳定，避免因 map 遍历顺序随机
 *         导致日志内容频繁变化，影响日志比对和 diff
 */
// formatHeaderLines prints headers with stable key order, one per line
func formatHeaderLines(h http.Header) string {
	if h == nil {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "  %s: %s\n", k, redactHeaderValue(k, v))
		}
	}
	return b.String()
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   记录客户端到代理的入站 HTTP 请求日志
 *
 * 处理流程:
 *   1. 调用 shouldLogUpstreamHTTP 检查是否需要记录，不需要则直接返回
 *   2. 将请求体字节数组转为字符串，并调用 truncateForLog 按上限截断
 *   3. 通过 logger 的 Info 方法输出日志，格式为:
 *      "{intro} [client->proxy] {Method} {RequestURI} from {RemoteAddr}
 *         Query: {RawQuery}
 *         Headers:
 *           {formatHeaderLines 输出的头部，含脱敏}
 *         Body ({len(body)} bytes):
 *           {截断后的请求体}"
 *
 * @param   p     *Proxy       代理实例
 * @param   l     *logger.Logger  结构化日志记录器
 * @param   intro string       日志前缀标识（如请求ID），用于关联同一次请求的上下游日志
 * @param   r     *http.Request  客户端原始 HTTP 请求
 * @param   body  []byte        已读取的完整请求体字节数组
 *
 * @note   代理在读取完整请求体后立即调用此方法，确保在请求被转发前
 *         记录原始的客户端请求数据，便于后续追踪和审计
 */
// logHTTPClientToProxy logs the incoming request from the user agent to the proxy
func (p *Proxy) logHTTPClientToProxy(l *logger.Logger, intro string, r *http.Request, body []byte) {
	if !p.shouldLogUpstreamHTTP() {
		return
	}
	b := string(body)
	b = truncateForLog(b, p.upstreamLogMax())
	l.Info("%s [client->proxy] %s %s from %s\n  Query: %s\n  Headers:\n%s  Body (%d bytes):\n%s",
		intro, r.Method, r.URL.RequestURI(), r.RemoteAddr, r.URL.RawQuery, formatHeaderLines(r.Header), len(body), b,
	)
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   记录代理即将发往大语言模型服务商的上游 HTTP 请求日志
 *
 * 处理流程:
 *   1. 调用 shouldLogUpstreamHTTP 检查是否需要记录，不需要则直接返回
 *   2. 如果 r 为 nil（异常情况），直接返回
 *   3. 将请求体字节数组转为字符串，并调用 truncateForLog 按上限截断
 *   4. 通过 logger 输出日志，格式为:
 *      "HTTP upstream request [call_id={callID}] [proxy->LLM] {Method} {URL}
 *         Headers:
 *           {formatHeaderLines 输出的头部，含脱敏}
 *         Body ({len(body)} bytes):
 *           {截断后的请求体}"
 *
 * @param   p      *Proxy       代理实例
 * @param   l      *logger.Logger  结构化日志记录器
 * @param   callID string       本次上游调用的唯一标识符，用于关联请求和响应日志
 * @param   r      *http.Request  代理构造的、即将发往 LLM 的上游 HTTP 请求
 * @param   body   []byte        即将发送的请求体字节数组
 *
 * @note   callID 用于在全量日志中将 proxy->LLM 请求和 LLM->proxy 响应
 *         关联起来，格式通常为 "{requestID}_{upstreamIndex}"
 */
// logHTTPOutgoingUpstream logs the HTTP request the proxy is about to send to the model backend
func (p *Proxy) logHTTPOutgoingUpstream(l *logger.Logger, callID string, r *http.Request, body []byte) {
	if !p.shouldLogUpstreamHTTP() {
		return
	}
	if r == nil {
		return
	}
	b := string(body)
	b = truncateForLog(b, p.upstreamLogMax())
	l.Info("HTTP upstream request [call_id=%s] [proxy->LLM] %s %s\n  Headers:\n%s  Body (%d bytes):\n%s",
		callID, r.Method, r.URL.String(), formatHeaderLines(r.Header), len(body), b,
	)
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   记录大语言模型服务商返回的完整上游 HTTP 响应日志（非流式）
 *
 * 处理流程:
 *   1. 调用 shouldLogUpstreamHTTP 检查是否需要记录，不需要则直接返回
 *   2. 如果 resp 为 nil（异常情况），直接返回
 *   3. 将响应体字节数组转为字符串，并调用 truncateForLog 按上限截断
 *   4. 通过 logger 输出日志，格式为:
 *      "HTTP upstream response [call_id={callID}] [LLM->proxy] {Status} (status {StatusCode}, {len(body)} bytes body)
 *         Headers:
 *           {formatHeaderLines 输出的头部，含脱敏}
 *         Body:
 *           {截断后的响应体}"
 *
 * @param   p      *Proxy       代理实例
 * @param   l      *logger.Logger  结构化日志记录器
 * @param   callID string       本次上游调用的唯一标识符
 * @param   resp   *http.Response  LLM 服务商返回的 HTTP 响应对象
 * @param   body   []byte        已读取的完整响应体字节数组
 *
 * @note   此方法仅用于非流式请求（proxy.EnableStream = false），
 *         因为非流式请求中代理会先完整读取响应体，再返回给客户端，
 *         此时响应体已在内存中，可直接记录完整内容。
 *         流式请求请使用 logHTTPUpstreamResponseStreamMeta 方法
 */
// logHTTPUpstreamResponseFull logs status, headers and full body after a non-streaming read
func (p *Proxy) logHTTPUpstreamResponseFull(l *logger.Logger, callID string, resp *http.Response, body []byte) {
	if !p.shouldLogUpstreamHTTP() || resp == nil {
		return
	}
	b := string(body)
	b = truncateForLog(b, p.upstreamLogMax())
	l.Info("HTTP upstream response [call_id=%s] [LLM->proxy] %s (status %d, %d bytes body)\n  Headers:\n%s  Body:\n%s",
		callID, resp.Status, resp.StatusCode, len(body), formatHeaderLines(resp.Header), b,
	)
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   记录大语言模型服务商返回的流式上游 HTTP 响应的元数据（状态行和头部）
 *
 * 处理流程:
 *   1. 调用 shouldLogUpstreamHTTP 检查是否需要记录，不需要则直接返回
 *   2. 如果 resp 为 nil（异常情况），直接返回
 *   3. 通过 logger 输出日志，格式为:
 *      "HTTP upstream response (streaming) [call_id={callID}] [LLM->proxy] {Status}
 *       — body is SSE, read and forwarded chunkwise (not fully buffered here)
 *         Headers:
 *           {formatHeaderLines 输出的头部，含脱敏}"
 *
 * @param   p      *Proxy       代理实例
 * @param   l      *logger.Logger  结构化日志记录器
 * @param   callID string       本次上游调用的唯一标识符
 * @param   resp   *http.Response  LLM 服务商返回的 HTTP 响应对象
 *
 * @note   此方法仅记录流式响应的状态行和头部信息。
 *         由于流式响应的响应体是 SSE（Server-Sent Events）格式的
 *         增量数据流，代理会在收到每个 chunk 后立即转发给客户端，
 *         而不进行完整缓冲，因此响应体内容不会在此处记录。
 *         每个 SSE chunk 的内容在 proxy.go 的流式转发逻辑中
 *         通过 storage 层的 WriteStreamChunk 方法单独持久化
 */
// logHTTPUpstreamResponseStreamMeta logs the response line and headers; stream body is not materialized
func (p *Proxy) logHTTPUpstreamResponseStreamMeta(l *logger.Logger, callID string, resp *http.Response) {
	if !p.shouldLogUpstreamHTTP() || resp == nil {
		return
	}
	l.Info("HTTP upstream response (streaming) [call_id=%s] [LLM->proxy] %s — body is SSE, read and forwarded chunkwise (not fully buffered here)\n  Headers:\n%s",
		callID, resp.Status, formatHeaderLines(resp.Header),
	)
}
