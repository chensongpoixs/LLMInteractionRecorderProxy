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
	"embed"
	"net/http"
)

/*
 * @brief 内嵌的 Usage Dashboard 前端文件系统
 *
 * 通过 go:embed 指令，在编译时将 web/usage.html 打包进二进制文件。
 * 该文件包含完整的 Vue 3 单页应用，使用 Chart.js 绘制用量趋势图，
 * 通过 EventSource API 连接到 /api/usage/stream 获取实时 SSE 数据流。
 *
 * @note 编译时要求 web/usage.html 文件必须存在，否则编译失败
 */
//go:embed web/usage.html
var dashboardFS embed.FS

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   处理 Usage Dashboard 页面的 HTTP 请求
 *
 * 处理流程:
 *   1. 从内嵌文件系统 dashboardFS 中读取 web/usage.html
 *   2. 如果读取失败（如编译时未嵌入），返回 500 Internal Server Error
 *   3. 设置 Content-Type 为 text/html; charset=utf-8
 *   4. 将 HTML 内容写入 HTTP 响应体
 *
 * @param   p *Proxy           代理实例（方法接收者）
 * @param   w http.ResponseWriter  HTTP 响应写入器
 * @param   r *http.Request    客户端 HTTP 请求
 *
 * @note   该处理器注册在 /usage 路径上，返回的是单页应用前端界面，
 *         前端随后通过 /api/usage/stream 建立 SSE 连接获取实时数据
 */
func (p *Proxy) handleUsageDashboard(w http.ResponseWriter, r *http.Request) {
	data, err := dashboardFS.ReadFile("web/usage.html")
	if err != nil {
		http.Error(w, "dashboard not available", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
