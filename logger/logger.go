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

package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  日志级别类型（Log Level Type）
 *
 *  Level 表示日志严重级别，级别越高表示越严重。
 *  - DEBUG(0): 调试信息，用于开发和排查问题
 *  - INFO(1):  一般信息，记录正常运行状态
 *  - WARN(2):  警告信息，表示潜在问题
 *  - ERROR(3): 错误信息，表示操作失败
 *  - FATAL(4): 致命错误，记录后程序退出
 *
 *  @note  日志过滤规则：只输出 >= 当前设置级别的日志。
 *         例如：设置 WARN 级别时，只输出 WARN/ERROR/FATAL。
 ******************************************************************************/
type Level int

const (
	DEBUG Level = iota // 调试级别 — 最详细，包含所有调试输出
	INFO               // 信息级别 — 正常运行状态记录
	WARN               // 警告级别 — 潜在问题提示
	ERROR              // 错误级别 — 操作失败记录
	FATAL              // 致命级别 — 记录后终止程序
)

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  将日志级别转换为字符串（Level to String）
 *
 *  返回日志级别对应的可读字符串名称。
 *
 *  @return 级别字符串（DEBUG / INFO / WARN / ERROR / FATAL / UNKNOWN）
 ******************************************************************************/
func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  日志配置结构体（Logger Configuration）
 *
 *  Config 定义日志系统的全部配置参数：
 *  - Level:        最低输出级别（低于此级别的日志将被忽略）
 *  - File:         日志文件路径（空字符串表示不写入文件）
 *  - MaxSizeMB:    单个日志文件最大大小（MB）
 *  - MaxBackups:   保留的旧日志文件最大数量
 *  - MaxAgeDays:   旧日志文件最长保留天数
 *  - Compress:     是否压缩旧日志文件
 *  - Console:      是否输出到控制台
 *  - RequestLog:   是否记录请求级日志
 *  - RequestBody:  是否记录请求体内容
 *  - ResponseBody: 是否记录响应体内容
 ******************************************************************************/
type Config struct {
	Level        Level  `yaml:"level"`
	File         string `yaml:"file"`
	MaxSizeMB    int    `yaml:"max_size_mb"`
	MaxBackups   int    `yaml:"max_backups"`
	MaxAgeDays   int    `yaml:"max_age_days"`
	Compress     bool   `yaml:"compress"`
	Console      bool   `yaml:"console"`
	RequestLog   bool   `yaml:"request_log"`
	RequestBody  bool   `yaml:"request_body"`
	ResponseBody bool   `yaml:"response_body"`
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  返回默认日志配置（Default Logger Configuration）
 *
 *  创建一个具有合理默认值的日志配置：
 *  - 级别: INFO
 *  - 文件: ./logs/app.log
 *  - 最大大小: 100MB
 *  - 备份数: 10
 *  - 保留天数: 30
 *  - 压缩: 启用
 *  - 控制台输出: 启用
 *  - 请求日志: 启用
 *
 *  @return 指向默认 Config 的指针
 ******************************************************************************/
func DefaultConfig() *Config {
	return &Config{
		Level:      INFO,
		File:       "./logs/app.log",
		MaxSizeMB:  100,
		MaxBackups: 10,
		MaxAgeDays: 30,
		Compress:   true,
		Console:    true,
		RequestLog: true,
	}
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  日志记录器（Logger）
 *
 *  Logger 是日志系统的核心结构体，提供线程安全的结构化日志写入能力。
 *
 *  核心特性：
 *  - 互斥锁保护:        所有写入操作通过 sync.Mutex 串行化
 *  - 双通道输出:        同时写入控制台和文件
 *  - 请求关联:          通过 RequestID 关联同一请求的所有日志
 *  - 级别过滤:          低于配置级别的日志自动忽略
 *  - 时间戳:            每条日志自动添加微秒级时间戳
 *
 *  @note  Logger 实例通过 New() 创建，推荐使用 InitGlobal() 创建全局单例。
 *  @note  使用完毕后应调用 Close() 关闭文件句柄。
 ******************************************************************************/
type Logger struct {
	mu         sync.Mutex // 互斥锁 — 保证并发写入安全
	level      Level      // 当前日志级别 — 低于此级别的日志被忽略
	file       *os.File   // 日志文件句柄
	console    *log.Logger // 控制台输出（stdlog）
	fileLogger *log.Logger // 文件输出（stdlog）
	requestID  string      // 请求关联ID — 用于请求级日志追踪
	config     *Config     // 日志配置引用
	startTime  time.Time   // 日志系统启动时间
}

// 全局日志实例及初始化锁
var globalLogger *Logger
var globalOnce sync.Once

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  创建新的日志记录器实例（Create Logger Instance）
 *
 *  根据配置创建 Logger 实例。
 *
 *  初始化流程：
 *  1. 检查配置是否为空，为空则使用默认配置
 *  2. 初始化 Logger 结构体（级别、配置、启动时间）
 *  3. 如果启用 Console 输出，创建控制台 Logger
 *  4. 如果指定了 File 路径，创建文件 Logger（自动创建目录）
 *  5. 返回初始化的 Logger 实例
 *
 *  @param  cfg  指向 Config 的指针（可为 nil，将使用默认配置）
 *  @return 初始化后的 Logger 实例和可能的错误
 *  @note   如果 File 路径不为空，会自动创建父目录。
 *  @note   日志格式: YYYY/MM/DD HH:MM:SS.微秒
 ******************************************************************************/
func New(cfg *Config) (*Logger, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	l := &Logger{
		level:     cfg.Level,
		config:    cfg,
		startTime: time.Now(),
	}

	// Setup console logger
	if cfg.Console {
		l.console = log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
	}

	// Setup file logger
	if cfg.File != "" {
		dir := filepath.Dir(cfg.File)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		l.file = f
		l.fileLogger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	}

	return l, nil
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  初始化全局日志实例（Initialize Global Logger）
 *
 *  使用 sync.Once 确保全局 Logger 仅初始化一次（线程安全）。
 *  通常在 main() 启动阶段调用，之后通过 GetDefault() 获取实例。
 *
 *  @param  cfg  日志配置指针
 *  @return 全局 Logger 实例
 *  @note   仅在首次调用时执行初始化，后续调用直接返回已创建的实例。
 *
 *  使用示例：
 *  @code
 *  // 在 main() 中
 *  logger.InitGlobal(cfg)
 *  // 在其他地方
 *  log := logger.GetDefault()
 *  log.Info("processing request")
 *  @endcode
 ******************************************************************************/
func InitGlobal(cfg *Config) *Logger {
	globalOnce.Do(func() {
		globalLogger, _ = New(cfg)
	})
	return globalLogger
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  设置日志级别（Set Log Level）
 *
 *  动态修改 Logger 的最低输出级别。低于此级别的日志将被静默忽略。
 *
 *  处理流程：
 *  1. 获取互斥锁
 *  2. 更新 level 字段
 *  3. 释放互斥锁
 *
 *  @param  level  新的日志级别
 *  @note   运行时调用，无需重启进程。
 ******************************************************************************/
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  设置请求关联ID（Set Request ID）
 *
 *  为当前 Logger 设置请求关联 ID，使后续日志输出带有 [REQ:xxx] 前缀，
 *  便于追踪同一请求的完整生命周期。
 *
 *  @param  id  请求唯一标识符
 *  @note   通常在请求处理开始时调用 WithRequestID() 而非直接修改。
 ******************************************************************************/
func (l *Logger) SetRequestID(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requestID = id
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  内部日志写入方法（Internal Log Writer）
 *
 *  核心日志写入逻辑，所有公开的日志方法最终调用此方法。
 *
 *  处理流程：
 *  1. 获取互斥锁
 *  2. 检查日志级别是否达到阈值（低于阈值则直接返回）
 *  3. 如果有 RequestID，添加 [REQ:xxx] 前缀
 *  4. 添加级别前缀 [INFO] / [ERROR] 等
 *  5. 格式化日志消息
 *  6. 写入控制台（如果启用）
 *  7. 写入文件（如果启用）
 *  8. 释放互斥锁
 *
 *  @param  level  日志级别
 *  @param  msg    日志格式字符串
 *  @param  args   格式参数
 *  @note   调用栈深度固定为 3（对应 Debug/Info/Warn/Error/Fatal → log），
 *          确保文件日志中的源文件信息正确。
 ******************************************************************************/
func (l *Logger) log(level Level, msg string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Keep logs at or above configured threshold.
	// DEBUG(0) should include everything; ERROR(3) should include ERROR/FATAL only.
	if level < l.level {
		return
	}

	// Format message with request ID if available
	var formattedMsg string
	if l.requestID != "" {
		formattedMsg = fmt.Sprintf("[REQ:%s] ", l.requestID) + msg
	} else {
		formattedMsg = msg
	}

	// Add additional context
	additionalArgs := []interface{}{}
	if l.requestID != "" {
		additionalArgs = append(additionalArgs, l.requestID)
	}

	// Format with level prefix
	levelPrefix := fmt.Sprintf("[%s] ", level.String())
	finalMsg := levelPrefix + formattedMsg

	// Write to console
	if l.config.Console && l.console != nil {
		l.console.Output(3, fmt.Sprintf(finalMsg, args...))
	}

	// Write to file
	if l.fileLogger != nil {
		fileMsg := fmt.Sprintf("[%s] %s", level.String(), formattedMsg)
		l.fileLogger.Output(3, fmt.Sprintf(fileMsg, args...))
	}
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录 DEBUG 级别日志
 *
 *  用于开发调试和问题排查，输出最详细的信息。
 *
 *  @param  msg   格式字符串
 *  @param  args  格式参数
 *  @note   DEBUG 日志仅在 level <= DEBUG 时输出。
 ******************************************************************************/
func (l *Logger) Debug(msg string, args ...interface{}) {
	l.log(DEBUG, msg, args...)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录 INFO 级别日志
 *
 *  用于记录正常运行状态信息，如服务启动、请求处理等。
 *
 *  @param  msg   格式字符串
 *  @param  args  格式参数
 ******************************************************************************/
func (l *Logger) Info(msg string, args ...interface{}) {
	l.log(INFO, msg, args...)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录 WARN 级别日志
 *
 *  用于记录警告信息，表示可能存在但不一定影响功能的问题。
 *
 *  @param  msg   格式字符串
 *  @param  args  格式参数
 ******************************************************************************/
func (l *Logger) Warn(msg string, args ...interface{}) {
	l.log(WARN, msg, args...)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录 ERROR 级别日志
 *
 *  用于记录错误信息，表示操作失败或异常情况。
 *
 *  @param  msg   格式字符串
 *  @param  args  格式参数
 ******************************************************************************/
func (l *Logger) Error(msg string, args ...interface{}) {
	l.log(ERROR, msg, args...)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录 FATAL 级别日志并退出程序
 *
 *  记录致命错误后调用 os.Exit(1) 立即终止程序。
 *  适用于无法恢复的严重错误，如配置加载失败、关键资源不可用等。
 *
 *  @param  msg   格式字符串
 *  @param  args  格式参数
 *  @note   此方法不会返回，程序直接退出。
 ******************************************************************************/
func (l *Logger) Fatal(msg string, args ...interface{}) {
	l.log(FATAL, msg, args...)
	os.Exit(1)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  创建带请求ID的子日志记录器（Logger with Request ID）
 *
 *  基于当前 Logger 创建一个新的 Logger 实例，该实例自动在所有日志中
 *  添加 [REQ:xxx] 前缀，用于追踪同一请求的完整日志链。
 *
 *  实现细节：
 *  - 共享同一个文件句柄和控制台 Logger（不创建新连接）
 *  - 复制配置和级别设置
 *  - 新的 Logger 拥有独立的 requestID
 *
 *  @param  id  请求唯一标识符
 *  @return 带有请求ID的新 Logger 实例
 *  @note   新实例与原始 Logger 共享底层文件句柄，关闭其中一个不会影响另一个。
 *
 *  使用示例：
 *  @code
 *  reqLogger := appLogger.WithRequestID("req_12345")
 *  reqLogger.Info("processing chat completion")
 *  // 输出: [INFO] [REQ:req_12345] processing chat completion
 *  @endcode
 ******************************************************************************/
func (l *Logger) WithRequestID(id string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newLogger := &Logger{
		level:      l.level,
		file:       l.file,
		console:    l.console,
		fileLogger: l.fileLogger,
		requestID:  id,
		config:     l.config,
		startTime:  l.startTime,
	}
	return newLogger
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录入站请求详情（Log Incoming Request）
 *
 *  记录客户端发来的 HTTP 请求基本信息，包括方法、路径和请求体大小。
 *  如果启用了 RequestBody 日志，额外输出请求体大小信息（DEBUG 级别）。
 *
 *  @param  reqID     请求唯一标识符
 *  @param  method    HTTP 方法（GET / POST 等）
 *  @param  path      请求路径
 *  @param  headers   HTTP 请求头（保留参数，当前未使用）
 *  @param  bodySize  请求体大小（字节）
 *  @note   输出级别为 INFO。
 ******************************************************************************/
func (l *Logger) LogRequest(reqID, method, path string, headers map[string][]string, bodySize int) {
	l.Info("Incoming request: %s %s (size: %d bytes)", method, path, bodySize)
	if l.config.RequestBody && bodySize > 0 {
		l.Debug("Request body size: %d bytes", bodySize)
	}
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录出站响应详情（Log Outgoing Response）
 *
 *  记录代理返回给客户端的 HTTP 响应信息。
 *
 *  状态码解读：
 *  - 2xx: [OK] — 正常响应
 *  - 4xx/5xx: [ERROR] — 客户端或服务端错误
 *
 *  @param  reqID        请求唯一标识符
 *  @param  method       HTTP 方法
 *  @param  path         请求路径
 *  @param  statusCode   HTTP 状态码
 *  @param  duration     请求处理耗时
 *  @param  responseSize 响应体大小（字节）
 ******************************************************************************/
func (l *Logger) LogResponse(reqID, method, path string, statusCode int, duration time.Duration, responseSize int) {
	statusStr := fmt.Sprintf("%d", statusCode)
	if statusCode >= 200 && statusCode < 300 {
		statusStr = fmt.Sprintf("%d [OK]", statusCode)
	} else if statusCode >= 400 {
		statusStr = fmt.Sprintf("%d [ERROR]", statusCode)
	}
	l.Info("Response: %s %s -> %s (duration: %v, response: %d bytes)",
		method, path, statusStr, duration, responseSize)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录代理转发详情（Log Proxy Forward）
 *
 *  记录代理将请求转发到上游 LLM 服务商的详细信息，
 *  包括目标模型、端点、URL 和是否使用流式传输。
 *
 *  @param  reqID     请求唯一标识符
 *  @param  model     使用的模型名称
 *  @param  endpoint  代理端点（如 "chat/completions"）
 *  @param  targetURL 上游目标 URL
 *  @param  isStream  是否启用流式传输
 ******************************************************************************/
func (l *Logger) LogProxyForward(reqID, model, endpoint, targetURL string, isStream bool) {
	streamStr := "no"
	if isStream {
		streamStr = "yes"
	}
	l.Info("Proxy forward: model=%s, endpoint=%s, target=%s, stream=%s",
		model, endpoint, targetURL, streamStr)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录代理响应详情（Log Proxy Response）
 *
 *  记录上游 LLM 服务商返回的响应信息。如果发生错误，
 *  输出 ERROR 级别日志；否则输出 INFO 级别日志。
 *
 *  @param  reqID      请求唯一标识符
 *  @param  model      模型名称
 *  @param  endpoint   代理端点
 *  @param  statusCode HTTP 状态码
 *  @param  bodySize   响应体大小（字节）
 *  @param  duration   上游请求耗时
 *  @param  err        错误信息（nil 表示成功）
 ******************************************************************************/
func (l *Logger) LogProxyResponse(reqID, model, endpoint string, statusCode int, bodySize int, duration time.Duration, err error) {
	if err != nil {
		l.Error("Proxy error for %s/%s: %v", model, endpoint, err)
		return
	}
	l.Info("Proxy response: model=%s, endpoint=%s, status=%d, size=%d bytes, duration=%v",
		model, endpoint, statusCode, bodySize, duration)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录存储操作详情（Log Storage Operation）
 *
 *  记录数据存储操作的结果。如果操作失败，输出 ERROR 级别日志；
 *  否则输出 INFO 级别日志。
 *
 *  @param  operation  操作名称（如 "write"、"read"、"list"）
 *  @param  path       文件路径
 *  @param  size       数据大小（字节）
 *  @param  err        错误信息（nil 表示成功）
 ******************************************************************************/
func (l *Logger) LogStorage(operation, path string, size int, err error) {
	if err != nil {
		l.Error("Storage %s failed for %s: %v", operation, path, err)
		return
	}
	l.Info("Storage %s: %s (size: %d bytes)", operation, path, size)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录流式传输块详情（Log Streaming Chunk）
 *
 *  记录 SSE 流式传输中每个数据块的索引和大小（DEBUG 级别）。
 *
 *  @param  reqID      请求唯一标识符
 *  @param  chunkIndex 块序号（从 0 开始）
 *  @param  chunkSize  块大小（字节）
 *  @note   仅在 DEBUG 级别时输出，生产环境通常设为 INFO 或以上。
 ******************************************************************************/
func (l *Logger) LogStreaming(reqID string, chunkIndex int, chunkSize int) {
	l.Debug("Streaming chunk %d: %d bytes", chunkIndex, chunkSize)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  记录服务生命周期事件（Log Server Lifecycle Event）
 *
 *  用于记录服务器的关键生命周期事件，如启动、关闭、重启等。
 *
 *  @param  event   事件名称（如 "start"、"shutdown"）
 *  @param  detail  事件详情（如端口号、关闭原因）
 ******************************************************************************/
func (l *Logger) LogServer(event, detail string) {
	l.Info("Server %s: %s", event, detail)
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  获取全局日志实例（Get Global Logger）
 *
 *  返回通过 InitGlobal() 初始化的全局 Logger 单例。
 *  如果尚未初始化，返回 nil — 调用方应检查。
 *
 *  @return 全局 Logger 实例（可能为 nil）
 *  @note   使用前必须调用 InitGlobal() 完成初始化。
 ******************************************************************************/
func GetDefault() *Logger {
	return globalLogger
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  查询是否记录请求体（Should Log Request Body）
 *
 *  返回配置中是否启用了请求体日志记录。
 *
 *  @return true 表示需要记录请求体内容
 ******************************************************************************/
func (l *Logger) ShouldLogRequestBody() bool {
	return l.config.RequestBody
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  查询是否记录响应体（Should Log Response Body）
 *
 *  返回配置中是否启用了响应体日志记录。
 *
 *  @return true 表示需要记录响应体内容
 ******************************************************************************/
func (l *Logger) ShouldLogResponseBody() bool {
	return l.config.ResponseBody
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  关闭日志文件句柄（Close Log File）
 *
 *  安全关闭日志文件，释放系统资源。
 *
 *  处理流程：
 *  1. 获取互斥锁
 *  2. 如果文件句柄不为 nil，关闭文件
 *  3. 释放互斥锁
 *
 *  @return 关闭操作的错误（nil 表示成功关闭或无文件需要关闭）
 *  @note   应在程序退出前调用（通常通过 defer 实现）。
 ******************************************************************************/
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  实现 io.Writer 接口（Implement io.Writer）
 *
 *  使 Logger 可以作为 io.Writer 使用（例如作为 HTTP 客户端的日志输出）。
 *  所有写入内容以 INFO 级别记录。
 *
 *  @param  p   写入的字节切片
 *  @return 写入字节数和可能的错误
 ******************************************************************************/
func (l *Logger) Write(p []byte) (n int, err error) {
	l.Info("%s", string(p))
	return len(p), nil
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  截断字符串（Truncate String）
 *
 *  将超过指定最大长度的字符串截断，末尾添加 "..."。用于日志输出中
 *  防止超长内容（如请求体、响应体）撑爆日志。
 *
 *  处理流程：
 *  1. 如果字符串长度 <= maxLen，直接返回原字符串
 *  2. 否则截断至 maxLen-3 并追加 "..."
 *
 *  @param  s      原始字符串
 *  @param  maxLen 最大允许长度
 *  @return 截断后的字符串
 *
 *  使用示例：
 *  @code
 *  truncated := TruncateString(longBody, 500)
 *  // 如果 longBody 长度为 800，返回前 497 字符 + "..."
 *  @endcode
 ******************************************************************************/
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

/******************************************************************************
 *  @author chensong
 *  @date   2026-04-26
 *  @brief  掩码敏感字符串（Mask Sensitive String）
 *
 *  对敏感信息（如 API Key、Token）进行部分掩码处理，
 *  保留首尾可见字符，中间替换为 ****。
 *
 *  处理流程：
 *  1. 如果字符串长度 <= visibleChars*2，返回完整字符串（太短不掩码）
 *  2. 否则保留前 visibleChars 和后 visibleChars，中间替换为 "****"
 *
 *  @param  s            原始字符串
 *  @param  visibleChars 首尾保留的可见字符数
 *  @return 掩码后的字符串
 *
 *  使用示例：
 *  @code
 *  masked := MaskString("sk-abc123def456ghi789jkl", 4)
 *  // 返回: "sk-a****jkl"
 *  @endcode
 ******************************************************************************/
func MaskString(s string, visibleChars int) string {
	if len(s) <= visibleChars*2 {
		return s
	}
	return s[:visibleChars] + "****" + s[len(s)-visibleChars:]
}
