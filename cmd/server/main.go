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

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"proxy-llm/config"
	"proxy-llm/exporter"
	"proxy-llm/logger"
	"proxy-llm/proxy"
	"proxy-llm/storage"
	"proxy-llm/uploader"
)

/*
logLevelFromString 将字符串形式的日志级别转换为 logger.Level 枚举值。

@author chensong
@date   2026-04-26
@brief 解析日志级别字符串并返回对应的 logger.Level 类型。

处理流程：
 1. 将输入字符串 s 与已知日志级别进行匹配（debug/info/warn/error）
 2. 若匹配成功，返回对应的 logger.Level 枚举值
 3. 若匹配失败或为空，默认返回 logger.INFO 级别

@param s string - 日志级别的字符串表示，支持 "debug"、"info"、"warn"、"error"
@return logger.Level - 对应的日志级别枚举值，未识别时默认返回 logger.INFO
@note 此函数仅处理精确匹配，大小写敏感。输入空字符串或其他未知值时返回 INFO。
*/
func logLevelFromString(s string) logger.Level {
	switch s {
	case "debug":
		return logger.DEBUG
	case "info":
		return logger.INFO
	case "warn":
		return logger.WARN
	case "error":
		return logger.ERROR
	default:
		return logger.INFO
	}
}

/*
main 是 proxy-llm 服务的入口函数，负责整个服务的生命周期管理。

@author chensong
@date   2026-04-26
@brief 完成命令行参数解析、各子系统初始化、服务启动、信号监听与优雅关闭。

处理流程：
 1. 解析命令行标志（-config 指定配置文件路径，-version 输出版本信息）
 2. 若指定 -version 标志，输出版本号后直接退出
 3. 调用 config.Load() 加载 YAML 配置文件，失败则致命退出
 4. 将配置文件中的日志设置转换为 logger.Config 结构体
 5. 调用 logger.New() 初始化结构化日志系统，defer 延迟关闭
 6. 输出启动横幅信息，包括服务地址、存储目录、日志配置及已配置的模型列表
 7. 调用 storage.NewLogger() 创建存储日志记录器，用于 JSONL 持久化
 8. 调用 proxy.NewMetrics() 创建指标采集器
 9. 调用 proxy.New() 创建代理服务器实例，传入配置、存储、指标和日志
10. 在独立 goroutine 中启动代理服务器，通过 channel 接收启动错误
11. 若配置文件启用了每日导出（daily_export.enable=true），创建可取消的 context
    并在独立 goroutine 中启动 runDailyExport() 调度函数
12. 监听 SIGINT 和 SIGTERM 操作系统信号，实现信号驱动的优雅关闭
13. 收到终止信号后，取消导出 context，创建 10 秒超时的关闭 context
14. 调用 proxyServer.Shutdown() 优雅关闭 HTTP 服务
15. 等待服务器 goroutine 返回，记录关闭完成日志

@param 无 - 通过命令行参数和配置文件获取运行参数
@return 无 - 通过日志记录运行状态，致命错误时调用 log.Fatalf 或 appLogger.Fatal
@note
  - 服务关闭超时设置为 10 秒，确保所有进行中的请求有机会完成
  - 代理服务器在独立 goroutine 中运行，主 goroutine 通过 select 等待信号或错误
  - 若服务器因 http.ErrServerClosed 退出，属于正常关闭流程，不视为错误
*/
func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Println("proxy-llm v1.0.0")
		fmt.Println("LLM API Proxy with Data Logging")
		return
	}

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize logging system
	logConfig := &logger.Config{
		Level:        logLevelFromString(cfg.Logging.Level),
		File:         cfg.Logging.File,
		MaxSizeMB:    cfg.Logging.MaxSizeMB,
		MaxBackups:   cfg.Logging.MaxBackups,
		MaxAgeDays:   cfg.Logging.MaxAgeDays,
		Compress:     cfg.Logging.Compress,
		Console:      cfg.Logging.Console,
		RequestLog:   cfg.Logging.RequestLog,
		RequestBody:  cfg.Logging.RequestBodyLog,
		ResponseBody: cfg.Logging.ResponseBodyLog,
	}

	appLogger, err := logger.New(logConfig)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer appLogger.Close()

	appLogger.Info("========================================")
	appLogger.Info("proxy-llm starting up...")
	appLogger.Info("Configuration loaded from: %s", *configPath)
	appLogger.Info("Server: %s:%d", cfg.Server.Host, cfg.Server.Port)
	appLogger.Info("Storage: %s (format: %s)", cfg.Storage.Directory, cfg.Storage.Format)
	appLogger.Info("Logging: level=%s, file=%s, console=%v", cfg.Logging.Level, cfg.Logging.File, cfg.Logging.Console)
	effMax := cfg.Logging.UpstreamLogMaxBytes
	if effMax == 0 {
		effMax = 256 * 1024
	}
	appLogger.Info(
		"Logging options: request_log=%v request_body_log=%v response_body_log=%v upstream_http_log=%v upstream_effective_log_max_bytes=%d",
		cfg.Logging.RequestLog, cfg.Logging.RequestBodyLog, cfg.Logging.ResponseBodyLog, cfg.Logging.UpstreamHTTPlog, effMax,
	)
	appLogger.Info("Models configured: %d", len(cfg.Models))

	for _, model := range cfg.Models {
		appLogger.Info("  - Model: %s, URL: %s, API: %s", model.Name, model.BaseURL, model.ModelName)
	}

	// Create storage logger
	storageLogger, err := storage.NewLogger(
		cfg.Storage.Directory,
		cfg.Storage.Format,
		cfg.Storage.Rotate,
		cfg.Storage.MaxSize,
		cfg.Storage.Compress,
	)
	if err != nil {
		appLogger.Fatal("Failed to create storage logger: %v", err)
	}

	// Create metrics collector
	metrics := proxy.NewMetrics()

	// Create proxy
	proxyServer := proxy.New(cfg, storageLogger, metrics, appLogger)

	// Start proxy server in background so we can react to OS signals.
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- proxyServer.Start()
	}()

	// Optional: daily export of the previous day into one JSONL (see daily_export in config).
	exportCtx, stopExport := context.WithCancel(context.Background())
	defer stopExport()
	if cfg.DailyExport.Enable {
		appLogger.Info("Daily export enabled: dir=%s prefix=%s at %02d:%02d (%s)",
			cfg.DailyExport.OutputDir, cfg.DailyExport.FilePrefix,
			cfg.DailyExport.RunHour, cfg.DailyExport.RunMinute, cfg.DailyExport.Timezone)
		go runDailyExport(exportCtx, cfg, appLogger)
	}

	// Listen for termination signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	select {
	case sig := <-sigChan:
		appLogger.Info("Shutdown signal received (%s), stopping gracefully...", sig.String())
		stopExport()
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			appLogger.Fatal("Server error: %v", err)
		}
		appLogger.Info("Server exited")
		return
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	appLogger.Info("Initiating server shutdown...")
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		appLogger.Fatal("Shutdown error: %v", err)
	}

	// Wait for server goroutine to return.
	if err := <-serverErrCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		appLogger.Fatal("Server exit error: %v", err)
	}

	appLogger.Info("Server stopped, all connections closed")
}

/*
runDailyExport 在后台无限循环，等待到达每日配置的执行时刻，
然后导出前一天的数据日志并上传至数据集平台。

@author chensong
@date   2026-04-26
@brief 每日定时导出调度函数：按配置时间触发前一日数据导出，并上传至 ModelScope/HuggingFace。

处理流程：
 1. 解析每日导出配置（时区、输出目录、文件前缀、执行时间）
 2. 时区处理：若配置了非空且非 "Local" 的时区字符串，调用 time.LoadLocation() 加载
    若加载失败，输出警告并回退为 time.Local
 3. 输出目录默认值为 "./exports"，文件前缀默认值为 "dataset-"
 4. 执行时分参数合法性校验：Hour 限制 [0,23]，Minute 限制 [0,59]，非法值回退为 0
 5. 进入无限循环，计算下一次执行时刻（若今日执行时间已过，则推迟至明日同时刻）
 6. 计算当前时刻到下一次执行时刻的时间间隔 d
 7. 通过 select 等待 ctx.Done()（取消信号）或 time.After(d)（定时触发）
 8. 触发后，获取前一日日期（基于当前时刻的时区计算 y = now - 1天）
 9. 根据 ExportFormat 配置选择导出方式：
    - "messages": 调用 exporter.ExportMessagesDay() 导出为消息格式
    - "dataset":  调用 exporter.ExportDatasetDay() 导出为数据集格式
                  包含 prompts、responses、revisions、feedback 统计
    - 默认:      调用 exporter.ExportDay() 导出为基础 JSONL 格式
10. 若导出失败，记录警告日志并 continue 等待下一轮
11. 导出成功时记录日志（基础/消息格式输出行数；数据集格式输出各项统计）
12. 遍历 ModelScope 和 HuggingFace 两个上传平台：
    - 若平台未启用（Enable=false），跳过
    - 等待配置的延迟秒数（默认 10 秒）后执行上传
    - 调用 ul.UploadLatestExport() 上传 outDir 中最新的导出文件
    - 若上传失败，记录警告日志

@param ctx context.Context - 用于接收取消信号以终止无限循环
@param cfg *config.Config   - 全局配置对象，包含 DailyExport、Storage 和上传平台配置
@param appLogger *logger.Logger - 应用日志记录器，用于记录导出和上传过程中的信息与警告
@return 无 - 通过 context 取消退出，所有状态通过日志输出
@note
  - 导出计算的前一天日期是基于时区的自然日（取当日 12:00:00 作为日期的代表时间戳）
  - 导出只在到达执行时刻的那一刻触发一次，不会补偿错过的执行
  - 上传延迟用于确保文件写入完成后再开始上传，可在配置中自定义秒数
  - 当 ctx 被取消时（服务关闭），函数立即返回以确保优雅退出
*/
func runDailyExport(ctx context.Context, cfg *config.Config, appLogger *logger.Logger) {
	c := &cfg.DailyExport
	loc := time.Local
	tz := strings.TrimSpace(c.Timezone)
	if tz != "" && tz != "Local" {
		if l, e := time.LoadLocation(tz); e == nil {
			loc = l
		} else {
			appLogger.Warn("daily_export: invalid timezone %q, using Local: %v", c.Timezone, e)
		}
	}
	outDir := c.OutputDir
	if outDir == "" {
		outDir = "./exports"
	}
	prefix := c.FilePrefix
	if prefix == "" {
		prefix = "dataset-"
	}
	h, m := c.RunHour, c.RunMinute
	if h < 0 || h > 23 {
		h = 0
	}
	if m < 0 || m > 59 {
		m = 0
	}
	for {
		now := time.Now().In(loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		d := time.Until(next)
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		y := time.Now().In(loc).AddDate(0, 0, -1)
		day := time.Date(y.Year(), y.Month(), y.Day(), 12, 0, 0, 0, loc)
		var n int
		var exportErr error
		var exportPath string
		switch strings.ToLower(c.ExportFormat) {
		case "messages":
			exportPath = filepath.Join(outDir, prefix+y.Format("20060102")+".jsonl")
			n, exportErr = exporter.ExportMessagesDay(cfg.Storage.Directory, day, exportPath)
		case "dataset":
			exportPath = filepath.Join(outDir, y.Format("20060102"))
			stats, err := exporter.ExportDatasetDay(cfg.Storage.Directory, day, exportPath)
			if err != nil {
				exportErr = err
			} else {
				n = stats.Prompts
				appLogger.Info("daily export (dataset): %d prompts, %d responses, %d revisions, %d feedback -> %s",
					stats.Prompts, stats.Responses, stats.Revisions, stats.Feedback, exportPath)
			}
		default:
			exportPath = filepath.Join(outDir, prefix+y.Format("20060102")+".jsonl")
			n, exportErr = exporter.ExportDay(cfg.Storage.Directory, day, exportPath)
		}
		if exportErr != nil {
			appLogger.Warn("daily export: %v", exportErr)
			continue
		}
		if c.ExportFormat != "dataset" {
			appLogger.Info("daily export (%s): %d rows -> %s", c.ExportFormat, n, exportPath)
		}

		// ── Dataset uploads (ModelScope / HuggingFace) ─────────────────
		for _, plat := range []struct {
			name  string
			cfg   config.DatasetRepoConfig
			newFn func(config.DatasetRepoConfig, *logger.Logger) *uploader.DatasetUploader
		}{
			{"modelscope", cfg.ModelScope, uploader.NewModelScope},
			{"huggingface", cfg.HuggingFace, uploader.NewHuggingFace},
		} {
			if !plat.cfg.Enable {
				continue
			}
			delay := plat.cfg.UploadDelay
			if delay <= 0 {
				delay = 10
			}
			appLogger.Info("%s: waiting %ds before upload...", plat.name, delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(delay) * time.Second):
			}

			ul := plat.newFn(plat.cfg, appLogger)
			if err := ul.UploadLatestExport(ctx, outDir); err != nil {
				appLogger.Warn("%s: upload failed: %v", plat.name, err)
			}
		}
	}
}
