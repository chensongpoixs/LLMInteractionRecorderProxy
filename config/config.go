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

package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// envVarPattern 用于匹配配置值中的 ${VAR_NAME} 格式环境变量占位符。
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// ModelConfig 定义单个 LLM 模型服务商的完整连接与认证配置。
//
// @author  chensong
// @date   2026-04-26
// @brief   LLM 模型服务商配置结构体
//
// 每个 ModelConfig 实例代表一个上游 LLM 服务商（如 OpenAI、Anthropic、DeepSeek、llama.cpp 等）。
// 代理服务会根据请求路由到对应的服务商，并自动处理协议转换（如 Anthropic <-> OpenAI）。
//
// 字段说明:
//   - Name:     服务商唯一标识名称，用于请求路由匹配
//   - BaseURL:  OpenAI 兼容 API 的基础 URL（例如 https://api.deepseek.com）
//   - BaseURLAnthropic: Anthropic 兼容 API 的基础 URL（例如 https://api.deepseek.com/anthropic）
//   - APIKey:   访问该服务商所需的 API 密钥
//   - ModelName: 请求时使用的具体模型名称
//   - Timeout:  HTTP 请求超时时间
//   - MaxRetries: 请求失败时的最大重试次数
//   - LlamaAPI: llama.cpp 专用 API 端点路径（例如 "/v1/api/chat"）
//   - LlamaAPIKey: llama.cpp API 的鉴权密钥（如果服务端要求）
//   - LlamaModel: 发送给 llama.cpp 的模型名称
//
// @note  当同时配置了 BaseURLAnthropic 时，代理会同时支持 OpenAI 和 Anthropic 两套协议路径。
// @note  Llama* 相关字段仅在使用 llama.cpp 作为后端时需要填写，其他情况下可忽略。
type ModelConfig struct {
	Name             string        `yaml:"name"`               // 服务商唯一名称
	BaseURL          string        `yaml:"base_url"`           // OpenAI 兼容的基础 URL（例如 https://api.deepseek.com）
	BaseURLAnthropic string        `yaml:"base_url_anthropic"` // Anthropic 兼容的基础 URL（例如 https://api.deepseek.com/anthropic）
	APIKey           string        `yaml:"api_key"`            // API 密钥
	ModelName        string        `yaml:"model"`              // 请求时使用的模型名称
	Timeout          time.Duration `yaml:"timeout"`            // HTTP 请求超时时间
	MaxRetries       int           `yaml:"max_retries"`        // 最大重试次数
	// llama.cpp 专用配置
	LlamaAPI    string `yaml:"llama_api"`     // llama.cpp API 端点路径（例如 "/v1/api/chat"）
	LlamaAPIKey string `yaml:"llama_api_key"` // llama.cpp API 鉴权密钥
	LlamaModel  string `yaml:"llama_model"`   // 发送给 llama.cpp 的模型名称
}

// StorageConfig 定义请求/响应数据的本地持久化存储策略。
//
// @author  chensong
// @date   2026-04-26
// @brief   数据存储配置结构体
//
// 代理服务会将所有转发的请求体和响应体以数据集格式持久化到本地磁盘。
// 存储目录按日期（YYYYMMDD）自动组织，文件格式为 JSONL。
//
// 字段说明:
//   - Directory: 数据存储根目录（默认 ./data）
//   - Format:    存储文件格式，支持 "jsonl" 或 "json"
//   - Compress:  是否对写入的文件进行 gzip 压缩
//   - Rotate:    文件轮转策略，支持 "daily"（按天）、"size"（按大小）、"weekly"（按周）
//   - MaxSize:   当 Rotate 为 "size" 时，单个文件的最大尺寸
//
// @note  目前实际存储逻辑中主要使用 "daily" 轮转策略，其他策略属于保留扩展项。
type StorageConfig struct {
	Directory string `yaml:"directory"` // 数据存储根目录
	Format    string `yaml:"format"`    // 存储格式："jsonl" 或 "json"
	Compress  bool   `yaml:"compress"`  // 是否 gzip 压缩
	Rotate    string `yaml:"rotate"`    // 轮转策略："daily"、"size"、"weekly"
	MaxSize   string `yaml:"max_size"`  // 按大小轮转时的最大文件尺寸
}

// ServerConfig 定义 HTTP 服务器的网络绑定与 TLS 配置。
//
// @author  chensong
// @date   2026-04-26
// @brief   HTTP 服务器配置结构体
//
// 字段说明:
//   - Port:    监听端口号（默认 8080）
//   - Host:    绑定的主机地址（默认 0.0.0.0，监听所有网络接口）
//   - TLSCert: TLS 证书文件路径（为空则不启用 HTTPS）
//   - TLSKey:  TLS 私钥文件路径（为空则不启用 HTTPS）
//
// @note  同时配置 TLSCert 和 TLSKey 后服务将自动启用 HTTPS 监听。
type ServerConfig struct {
	Port    int    `yaml:"port"`     // 监听端口号
	Host    string `yaml:"host"`     // 绑定主机地址
	TLSCert string `yaml:"tls_cert"` // TLS 证书文件路径
	TLSKey  string `yaml:"tls_key"`  // TLS 私钥文件路径
}

// LoggingConfig 定义日志系统的所有行为参数。
//
// @author  chensong
// @date   2026-04-26
// @brief   日志系统配置结构体
//
// 日志系统支持同时输出到控制台和文件，具备自动滚动和压缩能力。
// 可通过配置控制请求体/响应体的日志记录粒度，以及上游 HTTP 调用的详细日志。
//
// 字段说明:
//   - Level:           日志级别："debug"、"info"、"warn"、"error"
//   - File:            日志文件路径（例如 ./logs/app.log）
//   - MaxSizeMB:       单个日志文件最大尺寸（MB），超过后自动滚动
//   - MaxBackups:      保留的最大历史文件数量
//   - MaxAgeDays:      历史文件最大保留天数
//   - Compress:        对滚动的历史文件进行 gzip 压缩
//   - Console:         是否同时输出到控制台
//   - RequestLog:      是否记录请求级别日志
//   - RequestBodyLog:  是否在日志中记录请求体内容（生产环境建议关闭以保护隐私）
//   - ResponseBodyLog: 是否在日志中记录响应体内容（生产环境建议关闭以保护隐私）
//   - UpstreamHTTPlog: 是否启用上游 HTTP 链路日志（客户端→代理→LLM）
//   - UpstreamLogMaxBytes: 上游日志中单个消息体的最大截断字节数（0 表示 256 KiB）
//
// @note  RequestBodyLog 和 ResponseBodyLog 设置为 true 时，日志中会包含完整的对话内容，
//        可能涉及用户隐私数据，生产环境建议设置为 false。
type LoggingConfig struct {
	Level           string `yaml:"level"`                // 日志级别："debug"、"info"、"warn"、"error"
	File            string `yaml:"file"`                 // 日志文件路径
	MaxSizeMB       int    `yaml:"max_size_mb"`          // 单个文件最大尺寸（MB）
	MaxBackups      int    `yaml:"max_backups"`          // 最大历史文件数
	MaxAgeDays      int    `yaml:"max_age_days"`         // 历史文件最大保留天数
	Compress        bool   `yaml:"compress"`             // 是否 gzip 压缩历史文件
	Console         bool   `yaml:"console"`              // 是否输出到控制台
	RequestLog      bool   `yaml:"request_log"`          // 是否记录请求日志
	RequestBodyLog  bool   `yaml:"request_body_log"`     // 是否记录请求体内容
	ResponseBodyLog bool   `yaml:"response_body_log"`    // 是否记录响应体内容
	// UpstreamHTTPlog: 设置为 true 时，INFO 级别将记录客户端→代理、代理→LLM、LLM→代理的 HTTP 链路日志
	// （消息体通过 upstream_log_max_bytes 截断）
	UpstreamHTTPlog     bool `yaml:"upstream_http_log"`      // 是否启上游 HTTP 链路日志
	UpstreamLogMaxBytes int  `yaml:"upstream_log_max_bytes"` // 上游日志截断字节数（0 = 256 KiB）
}

// DailyExportConfig 定义每日定时合并导出任务的所有参数。
//
// @author  chensong
// @date   2026-04-26
// @brief   每日数据导出配置结构体
//
// 该功能在每天指定的时间（由 RunHour 和 RunMinute 决定）自动运行，
// 将前一天的所有 JSONL 日志合并为一个符合训练参考数据集格式的导出文件。
// 支持多种导出格式以适应不同的下游训练需求。
//
// 字段说明:
//   - Enable:       是否启用每日自动导出
//   - OutputDir:    导出文件输出目录
//   - FilePrefix:   导出文件名前缀
//   - ExportFormat: 导出格式：
//     "reasoning" — 推理格式（id/problem/thinking/solution），默认值
//     "messages"  — OpenAI 微调消息格式
//     "dataset"   — 数据集格式（prompts/responses/revisions/feedback）
//   - RunHour:   每日执行的小时（0-23），基于 Timezone 时区
//   - RunMinute: 每日执行的分钟（0-59），基于 Timezone 时区
//   - Timezone:  时区设置（例如 "Local"、"Asia/Shanghai"，空值等同于 "Local"）
//
// @note  导出任务运行后会自动清理已合并的原始 JSONL 文件以避免重复导出。
// @note  如果同时启用了 DatasetRepoConfig（ModelScope/HuggingFace），导出完成后会自动触发数据集上传。
type DailyExportConfig struct {
	Enable       bool   `yaml:"enable"`        // 是否启用每日导出
	OutputDir    string `yaml:"output_dir"`    // 导出文件输出目录
	FilePrefix   string `yaml:"file_prefix"`   // 导出文件名前缀
	ExportFormat string `yaml:"export_format"` // 导出格式："reasoning"、"messages"、"dataset"
	RunHour      int    `yaml:"run_hour"`      // 每日执行小时（0-23）
	RunMinute    int    `yaml:"run_minute"`    // 每日执行分钟（0-59）
	Timezone     string `yaml:"timezone"`      // 时区（例如 "Local"、"Asia/Shanghai"）
}

// DatasetRepoConfig 控制通过 Git 将导出数据集自动推送到 ModelScope / HuggingFace 仓库的行为。
//
// @author  chensong
// @date   2026-04-26
// @brief   数据集仓库自动上传配置结构体
//
// 每日导出完成后，若此配置启用，系统会自动将导出的文件复制到本地仓库副本中，
// 执行 Git 提交并推送到远程仓库。ModelScope 和 HuggingFace 分别拥有独立的配置实例。
//
// 字段说明:
//   - Enable:      是否启用自动上传
//   - RepoURL:     Git 远程仓库 URL（应包含用于推送的 token）
//   - RepoDir:     本地仓库克隆目录
//   - GitUser:     Git 提交作者名
//   - GitEmail:    Git 提交作者邮箱
//   - DataSubdir:  仓库内存放数据文件的子目录路径（默认 "data"）
//   - UploadDelay: 导出完成后等待秒数再执行上传（默认 10 秒）
//   - GitBranch:   推送的目标远程分支（ModelScope 默认 "master"，HuggingFace 默认 "main"）
//
// @note  RepoURL 中应包含具有推送权限的访问凭证，建议使用只读部署密钥或细粒度 token。
// @note  首次运行前需要先通过 git clone 或手动创建 RepoDir 目录并初始化 Git 仓库。
type DatasetRepoConfig struct {
	Enable      bool   `yaml:"enable"`       // 是否启用自动上传
	RepoURL     string `yaml:"repo_url"`     // Git 远程仓库 URL（应包含推送 token）
	RepoDir     string `yaml:"repo_dir"`     // 本地仓库克隆目录
	GitUser     string `yaml:"git_user"`     // Git 提交作者名
	GitEmail    string `yaml:"git_email"`    // Git 提交作者邮箱
	DataSubdir  string `yaml:"data_subdir"`  // 仓库内数据子目录路径（默认 "data"）
	UploadDelay int    `yaml:"upload_delay"` // 导出后等待秒数再上传（默认 10）
	GitBranch   string `yaml:"git_branch"`   // 推送目标远程分支（默认 "master" 或 "main"）
}

// AgentConfig 定义 ReAct 智能体的运行时行为参数。
//
// @author  chensong
// @date   2026-04-26
// @brief   ReAct 智能体配置结构体
//
// ReAct（Reasoning + Acting）智能体在专用沙箱环境中执行任务，
// 通过"思考-行动-观察"循环自主完成用户目标。
// 该配置控制了智能体的资源边界、迭代上限及工具操作的安全性。
//
// 字段说明:
//   - WorkspaceDir:  工具操作的沙箱目录（默认 "./agent_workspace"）
//                     所有文件操作（创建、读取、修改）都将限制在此目录内
//   - MaxIterations: ReAct 循环的最大迭代次数（默认 15）
//                     达到上限后将强制终止当前任务以防止无限循环
//   - BashTimeout:   单次 bash 命令的最大执行超时秒数（默认 30）
//                     超出后将强制终止命令进程并返回超时错误
//
// @note  沙箱目录 WorkspaceDir 建议使用绝对路径以避免工作目录切换导致的安全问题。
// @note  BashTimeout 设置过大会影响响应延迟，设置过小可能导致长时间任务无法完成。
type AgentConfig struct {
	WorkspaceDir  string `yaml:"workspace_dir"`  // 工具操作沙箱目录（默认 "./agent_workspace"）
	MaxIterations int    `yaml:"max_iterations"` // ReAct 最大循环次数（默认 15）
	BashTimeout   int    `yaml:"bash_timeout"`   // bash 命令超时秒数（默认 30）
}

// ProxyConfig 定义 HTTP 代理转发层的所有行为参数。
//
// @author  chensong
// @date   2026-04-26
// @brief   HTTP 代理行为配置结构体
//
// 代理层负责接收客户端请求、执行鉴权、设置 CORS 头、应用速率限制，
// 然后将请求透明转发到上游 LLM API 并返回响应。
//
// 字段说明:
//   - EnableStream:   是否启用 SSE（Server-Sent Events）流式响应转发
//   - MaxBodySize:    允许的最大请求体尺寸（例如 "10mb"）
//   - RateLimit:      每小时每客户端 IP 的最大请求数限制（0 表示不限制）
//   - EnableCORS:     是否启用 CORS（跨域资源共享）
//   - AllowedOrigins: 允许跨域访问的源地址列表
//   - EnableAuth:     是否启用 API Key 鉴权
//   - AuthHeader:     鉴权时读取的 HTTP Header 名称（例如 "Authorization"）
//   - AuthToken:      期望的鉴权凭证值（例如 "Bearer sk-xxx"）
//
// @note  当 EnableAuth 为 true 时，所有非健康检查的请求都必须在 AuthHeader 指定的头中
//        携带与 AuthToken 完全匹配的凭证，否则返回 401 Unauthorized。
type ProxyConfig struct {
	EnableStream   bool     `yaml:"enable_stream"`    // 是否启用 SSE 流式响应
	MaxBodySize    string   `yaml:"max_body_size"`    // 最大请求体尺寸
	RateLimit      int      `yaml:"rate_limit"`       // 每客户端每小时请求数上限
	EnableCORS     bool     `yaml:"enable_cors"`      // 是否启用 CORS
	AllowedOrigins []string `yaml:"allowed_origins"`  // 允许的跨域源列表
	EnableAuth     bool     `yaml:"enable_auth"`      // 是否启用 API Key 鉴权
	AuthHeader     string   `yaml:"auth_header"`      // 鉴权使用的 HTTP Header 名称
	AuthToken      string   `yaml:"auth_token"`       // 期望的鉴权凭证值
}

// MonitoringConfig 定义服务监控与可观测性端点的配置。
//
// @author  chensong
// @date   2026-04-26
// @brief   监控端点配置结构体
//
// 提供两类监控能力：
//   - 健康检查端点：供负载均衡器或 Kubernetes 探针使用，验证服务存活状态
//   - Prometheus 指标端点：暴露请求计数、延迟分布、错误率等可观测性指标
//
// 字段说明:
//   - EnableHealth:  是否启用 /health 健康检查端点
//   - EnableMetrics: 是否启用 Prometheus 指标采集端点
//   - MetricsPath:   Prometheus 指标暴露路径（例如 "/metrics"）
//
// @note  指标端点默认路径为 "/metrics"，与 Prometheus 抓取配置中的 metrics_path 保持一致即可。
type MonitoringConfig struct {
	EnableHealth  bool   `yaml:"enable_health"`  // 是否启用健康检查端点
	EnableMetrics bool   `yaml:"enable_metrics"` // 是否启用 Prometheus 指标端点
	MetricsPath   string `yaml:"metrics_path"`   // 指标暴露路径
}

// Config 是 proxy-llm 服务的顶层配置聚合结构体。
//
// @author  chensong
// @date   2026-04-26
// @brief   根配置结构体
//
// 本结构体将所有子配置模块聚合为一个完整的服务配置实例。
// 对应 YAML 配置文件的顶级结构，每个字段映射到文件中的一个顶级 section。
//
// 字段说明:
//   - Server:      HTTP 服务器配置
//   - Models:      已注册的 LLM 模型服务商列表
//   - Storage:     请求/响应数据持久化配置
//   - Proxy:       代理行为与安全策略配置
//   - Monitoring:  监控与可观测性端点配置
//   - Logging:     日志系统配置
//   - DailyExport: 每日数据导出任务配置
//   - ModelScope:  ModelScope 数据集仓库自动上传配置
//   - HuggingFace: HuggingFace 数据集仓库自动上传配置
//   - Agent:       ReAct 智能体运行时配置
//
// @note  加载配置请使用 Load() 函数，它会自动处理环境变量占位符替换和运行时覆盖。
// @note  使用默认配置请使用 DefaultConfig() 函数，它会返回带有合理默认值的配置实例。
type Config struct {
	Server      ServerConfig      `yaml:"server"`      // HTTP 服务器配置
	Models      []ModelConfig     `yaml:"models"`       // LLM 模型服务商列表
	Storage     StorageConfig     `yaml:"storage"`      // 数据存储配置
	Proxy       ProxyConfig       `yaml:"proxy"`        // 代理行为配置
	Monitoring  MonitoringConfig  `yaml:"monitoring"`   // 监控端点配置
	Logging     LoggingConfig     `yaml:"logging"`      // 日志系统配置
	DailyExport DailyExportConfig `yaml:"daily_export"` // 每日数据导出配置
	ModelScope  DatasetRepoConfig `yaml:"modelscope"`   // ModelScope 数据集仓库配置
	HuggingFace DatasetRepoConfig `yaml:"huggingface"`  // HuggingFace 数据集仓库配置
	Agent       AgentConfig       `yaml:"agent"`        // ReAct 智能体配置
}

// expandEnvVars 递归遍历任意 Go 值，将所有字符串类型中的 ${VAR_NAME} 占位符
// 替换为对应环境变量的值。
//
// @author  chensong
// @date   2026-04-26
// @brief   环境变量占位符递归展开
//
// 处理流程:
//  1. 判断当前值的类型
//  2. 如果是 string：使用正则表达式匹配所有 ${VAR_NAME} 格式的占位符，
//     提取变量名并通过 os.Getenv() 获取运行时环境变量的值进行替换
//  3. 如果是 map[string]interface{}：递归遍历所有键值对
//  4. 如果是 []interface{}：递归遍历所有数组/切片元素
//  5. 其他类型：原样返回不做处理
//
// @param  v  需要进行环境变量展开的任意 Go 值，通常为 YAML 解析后的通用 map/slice/string
//
// @return 展开后的值，类型与输入保持一致。如果输入为 nil 或非复合类型则原样返回
//
// @note  如果环境变量不存在，占位符会被替换为空字符串。调用方需确保所需环境变量已正确设置。
// @note  本函数仅对 string、map、slice 三种类型进行展开，int、bool 等基础类型不做处理。
//
// @code
//
//	// 假设环境变量 export API_KEY=sk-abc123
//	rawConfig := map[string]interface{}{
//	    "api_key": "${API_KEY}",
//	    "timeout": 30,
//	    "nested": map[string]interface{}{
//	        "url": "https://api.example.com?key=${API_KEY}",
//	    },
//	}
//	expanded := expandEnvVars(rawConfig)
//	// expanded["api_key"] == "sk-abc123"
//	// expanded["nested"]["url"] == "https://api.example.com?key=sk-abc123"
//
// @endcode
func expandEnvVars(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return envVarPattern.ReplaceAllStringFunc(val, func(match string) string {
			// match 的格式为 ${VAR_NAME}，提取花括号内的变量名称
			varName := match[2 : len(match)-1]
			return os.Getenv(varName)
		})
	case map[string]interface{}:
		for k, vv := range val {
			val[k] = expandEnvVars(vv)
		}
		return val
	case []interface{}:
		for i, item := range val {
			val[i] = expandEnvVars(item)
		}
		return val
	default:
		return v
	}
}

// Load 从指定的 YAML 文件路径加载完整服务配置，自动处理环境变量占位符和运行时覆盖。
//
// @author  chensong
// @date   2026-04-26
// @brief   加载并解析配置文件
//
// 处理流程:
//  1. 使用 os.ReadFile 读取指定路径的 YAML 文件内容
//  2. 将原始 YAML 反序列化为通用的 map[string]interface{} 结构
//  3. 调用 expandEnvVars 递归展开所有 ${VAR_NAME} 占位符为实际环境变量值
//  4. 将展开后的 map 重新序列化为 YAML 字节
//  5. 将重新序列化的 YAML 反序列化为强类型的 Config 结构体
//  6. 应用运行时环境变量覆盖：
//     - PROXY_PORT 覆盖 server.port
//     - PROXY_STORAGE_DIR 覆盖 storage.directory
//
// @param  path  YAML 配置文件的文件系统路径
//
// @return 解析完成的强类型配置指针，如果任何步骤失败则返回 nil 和 error
//
// @note  环境变量覆盖的优先级高于 YAML 文件中的配置值。
// @note  本函数采用 "Unmarshal -> Expand -> Marshal -> Unmarshal" 的两阶段解析策略，
//        以确保环境变量替换发生在类型约束施加之前，避免类型不匹配问题。
// @note  文件不存在、YAML 语法错误、环境变量转换失败等情况均会导致错误返回。
//
// @code
//
//	cfg, err := config.Load("config.yaml")
//	if err != nil {
//	    log.Fatalf("加载配置失败: %v", err)
//	}
//	fmt.Printf("服务将监听端口: %d\n", cfg.Server.Port)
//	for _, model := range cfg.Models {
//	    fmt.Printf("已注册模型: %s (%s)\n", model.Name, model.BaseURL)
//	}
//
// @endcode
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// 先反序列化为通用 map 以便在所有字符串值中展开环境变量
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	expanded := expandEnvVars(raw).(map[string]interface{})

	// 将展开后的 map 重新序列化为 YAML，再反序列化为 Config 结构体
	expandedBytes, err := yaml.Marshal(expanded)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal expanded config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(expandedBytes, cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 运行时环境变量覆盖（优先级高于 YAML 文件配置）
	if v := os.Getenv("PROXY_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("PROXY_STORAGE_DIR"); v != "" {
		cfg.Storage.Directory = v
	}

	return cfg, nil
}

// DefaultConfig 返回一个包含所有模块合理默认值的配置实例，可用于快速启动或作为配置模板。
//
// @author  chensong
// @date   2026-04-26
// @brief   获取默认配置
//
// 默认配置的要点:
//   - 服务器监听 0.0.0.0:8080
//   - 注册一个名为 "default" 的模型，指向 OpenAI GPT-4，超时 120 秒
//   - 数据存储于 ./data 目录，JSONL 格式，按天轮转
//   - 启用 SSE 流式响应，最大请求体 10MB，启用 CORS，关闭鉴权
//   - 启用健康检查和 Prometheus 指标（/metrics 路径）
//   - 日志级别 info，同时输出到 ./logs/app.log 和控制台
//   - 每日导出默认关闭，导出目录 ./exports，推理格式，凌晨 00:05 执行
//   - ModelScope/HuggingFace 自动上传默认关闭
//   - ReAct 智能体沙箱目录 ./agent_workspace，最多 15 轮迭代，bash 超时 30 秒
//
// @return 填充了默认值的 Config 指针
//
// @note  本函数返回的配置不包含任何敏感信息（如 API Key），调用方需要根据实际情况
//        覆盖模型服务商配置中的 APIKey 等字段。
//
// @code
//
//	cfg := config.DefaultConfig()
//	// 覆盖模型配置
//	cfg.Models[0].APIKey = "sk-your-api-key"
//	cfg.Models[0].BaseURL = "https://api.openai.com/v1"
//	cfg.Models[0].ModelName = "gpt-4"
//	// 自定义端口
//	cfg.Server.Port = 9090
//
// @endcode
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 8080,
			Host: "0.0.0.0",
		},
		Models: []ModelConfig{
			{
				Name:      "default",
				BaseURL:   "https://api.openai.com/v1",
				ModelName: "gpt-4",
				Timeout:   120 * time.Second,
			},
		},
		Storage: StorageConfig{
			Directory: "./data",
			Format:    "jsonl",
			Rotate:    "daily",
		},
		Proxy: ProxyConfig{
			EnableStream: true,
			MaxBodySize:  "10mb",
			EnableCORS:   true,
			EnableAuth:   false,
			AuthHeader:   "Authorization",
		},
		Monitoring: MonitoringConfig{
			EnableHealth:  true,
			EnableMetrics: true,
			MetricsPath:   "/metrics",
		},
		Logging: LoggingConfig{
			Level:               "info",
			File:                "./logs/app.log",
			MaxSizeMB:           100,
			MaxBackups:          10,
			MaxAgeDays:          30,
			Compress:            true,
			Console:             true,
			RequestLog:          true,
			UpstreamHTTPlog:     false,
			UpstreamLogMaxBytes: 0,
		},
		DailyExport: DailyExportConfig{
			Enable:       false,
			OutputDir:    "./exports",
			FilePrefix:   "Opus-4.6-Reasoning-3000x-filtered-",
			ExportFormat: "reasoning",
			RunHour:      0,
			RunMinute:    5,
			Timezone:     "Local",
		},
		ModelScope: DatasetRepoConfig{
			Enable:      false,
			RepoDir:     "./modelscope_repo",
			DataSubdir:  "data",
			UploadDelay: 10,
			GitBranch:   "master",
		},
		HuggingFace: DatasetRepoConfig{
			Enable:      false,
			RepoDir:     "./huggingface_repo",
			DataSubdir:  "data",
			UploadDelay: 10,
			GitBranch:   "main",
		},
		Agent: AgentConfig{
			WorkspaceDir:  "./agent_workspace",
			MaxIterations: 15,
			BashTimeout:   30,
		},
	}
}
