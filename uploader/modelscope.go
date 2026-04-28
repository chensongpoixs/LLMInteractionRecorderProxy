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

package uploader

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"proxy-llm/config"
	"proxy-llm/exporter"
	"proxy-llm/logger"
)

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   DatasetUploader 是数据集上传器，负责将本地导出的数据集文件同步到
 *          ModelScope 或 HuggingFace 等远程数据集仓库。
 *
 * 该结构体通过 Git 工作流管理数据集版本：clone 仓库 → 复制导出文件 →
 * 生成元数据 → git add/commit/push。支持按天增量上传和全量导出上传两种模式。
 *
 * @field cfg       - 数据集仓库配置（仓库 URL、本地目录、Git 身份、目标分支等）
 * @field appLogger - 应用级结构化日志记录器，用于输出上传过程的日志信息
 * @field platform  - 平台标识字符串，"modelscope" 或 "huggingface"，用于日志区分
 */
type DatasetUploader struct {
	cfg       config.DatasetRepoConfig
	appLogger *logger.Logger
	platform  string // "modelscope" or "huggingface"
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   NewModelScope 创建一个面向 ModelScope 平台的数据集上传器。
 *
 * 处理流程：
 *   1. 接收仓库配置和日志记录器
 *   2. 构造 DatasetUploader 实例，将 platform 字段固定为 "modelscope"
 *   3. 返回该实例指针
 *
 * @param   cfg       - 数据集仓库配置，包含 repo_url、repo_dir、git_user 等
 * @param   appLogger - 应用级日志记录器
 * @return  *DatasetUploader - 初始化完成的 ModelScope 上传器实例
 *
 * @note    该构造函数仅为赋值操作，不执行任何 I/O 或网络操作。
 *          实际仓库连接在 uploadDay / UploadLatestExport 调用时建立。
 */
func NewModelScope(cfg config.DatasetRepoConfig, appLogger *logger.Logger) *DatasetUploader {
	return &DatasetUploader{cfg: cfg, appLogger: appLogger, platform: "modelscope"}
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   NewHuggingFace 创建一个面向 HuggingFace 平台的数据集上传器。
 *
 * 处理流程：
 *   1. 接收仓库配置和日志记录器
 *   2. 构造 DatasetUploader 实例，将 platform 字段固定为 "huggingface"
 *   3. 返回该实例指针
 *
 * @param   cfg       - 数据集仓库配置，包含 repo_url、repo_dir、git_user 等
 * @param   appLogger - 应用级日志记录器
 * @return  *DatasetUploader - 初始化完成的 HuggingFace 上传器实例
 *
 * @note    与 NewModelScope 结构相同，仅平台标识不同。
 *          HuggingFace 数据集仓库默认使用 main 分支（ModelScope 使用 master）。
 */
func NewHuggingFace(cfg config.DatasetRepoConfig, appLogger *logger.Logger) *DatasetUploader {
	return &DatasetUploader{cfg: cfg, appLogger: appLogger, platform: "huggingface"}
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   UploadDay 导出指定日期的请求/响应数据并推送到远程数据集仓库。
 *
 * 处理流程：
 *   1. 格式化日期字符串（YYYYMMDD）
 *   2. 调用 syncRepo 同步远程仓库（首次 clone，后续 pull）
 *   3. 在仓库的 DataSubdir 下创建以日期命名的子目录
 *   4. 调用 exporter.ExportDay 导出推理格式数据到 reasoning.jsonl
 *   5. 调用 exporter.ExportMessagesDay 导出消息格式数据到 messages.jsonl
 *      （该步骤失败为非致命错误，仅记录警告日志）
 *   6. 调用 generateMetadataCSV 生成/更新 metadata.csv 聚合统计文件
 *      （该步骤失败为非致命错误）
 *   7. 调用 gitAddCommitPush 将本次变更提交并推送到远程仓库
 *   8. 返回 nil 表示上传成功，或返回错误信息
 *
 * @param   ctx          - 上下文对象，用于取消和超时控制
 * @param   storageDir   - 原始 JSONL 日志存储目录（作为导出数据源）
 * @param   exportDir    - 导出输出目录（当前实现未使用此参数）
 * @param   exportPrefix - 导出文件名前缀（当前实现未使用此参数）
 * @param   day          - 要导出的目标日期
 * @return  error - 成功返回 nil，失败返回包含平台前缀和步骤信息的错误
 *
 * @note    步骤 5 和步骤 6 的失败不会中断上传流程，因为这些数据可通过
 *          后续重新导出来弥补。只有网络/Git 操作失败才会返回错误。
 */
func (u *DatasetUploader) UploadDay(ctx context.Context, storageDir, exportDir, exportPrefix string, day time.Time) error {
	dateStr := day.Format("20060102")

	if err := u.syncRepo(ctx); err != nil {
		return fmt.Errorf("%s sync repo: %w", u.platform, err)
	}

	targetDir := filepath.Join(u.cfg.RepoDir, u.cfg.DataSubdir, dateStr)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("%s create target dir %s: %w", u.platform, targetDir, err)
	}

	reasoningPath := filepath.Join(targetDir, "reasoning.jsonl")
	n, err := exporter.ExportDay(storageDir, day, reasoningPath)
	if err != nil {
		return fmt.Errorf("%s export reasoning: %w", u.platform, err)
	}
	u.appLogger.Info("%s: exported %d reasoning rows to %s", u.platform, n, reasoningPath)

	messagesPath := filepath.Join(targetDir, "messages.jsonl")
	nm, err := exporter.ExportMessagesDay(storageDir, day, messagesPath)
	if err != nil {
		u.appLogger.Warn("%s: export messages failed (non-fatal): %v", u.platform, err)
	} else {
		u.appLogger.Info("%s: exported %d messages rows to %s", u.platform, nm, messagesPath)
	}

	if err := u.generateMetadataCSV(); err != nil {
		u.appLogger.Warn("%s: generate metadata.csv failed (non-fatal): %v", u.platform, err)
	}

	if err := u.gitAddCommitPush(ctx, dateStr, n); err != nil {
		return fmt.Errorf("%s git push: %w", u.platform, err)
	}

	u.appLogger.Info("%s: successfully uploaded day %s (%d rows)", u.platform, dateStr, n)
	return nil
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   UploadLatestExport 将导出目录中的全部文件复制到数据集仓库并推送至远程。
 *
 * 该函数支持两种导出格式：
 *   - 扁平 JSONL 文件（reasoning/messages 格式）：直接复制到仓库 data 目录下
 *   - 日期目录树（dataset 格式）：按日期子目录递归复制整个目录树
 *
 * 处理流程：
 *   1. 调用 syncRepo 同步远程仓库到本地
 *   2. 确保仓库的 DataSubdir 目录存在
 *   3. 遍历导出目录下的所有条目：
 *      a. 跳过 metadata.csv 和 .git 目录（避免覆盖或污染仓库）
 *      b. 对于目录：如果目录名为 8 位纯数字（YYYYMMDD），则递归复制整个目录树
 *      c. 对于 .jsonl 文件：直接复制到仓库 data 目录下
 *   4. 如果没有任何文件被复制，返回错误
 *   5. 调用 generateMetadataCSV 生成聚合统计文件（非致命错误）
 *   6. 调用 gitAddCommitPush 提交并推送所有变更
 *
 * @param   ctx       - 上下文对象，用于取消和超时控制
 * @param   exportDir - 包含导出文件的源目录
 * @return  error - 成功返回 nil，失败返回包含平台前缀和步骤信息的错误
 *
 * @note    metadata.csv 和 .git 目录会被显式跳过，避免从导出目录意外覆盖
 *          仓库自身的元数据文件。
 */
func (u *DatasetUploader) UploadLatestExport(ctx context.Context, exportDir string) error {
	if err := u.syncRepo(ctx); err != nil {
		return fmt.Errorf("%s sync repo: %w", u.platform, err)
	}

	dataDir := filepath.Join(u.cfg.RepoDir, u.cfg.DataSubdir)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("%s create data dir: %w", u.platform, err)
	}

	entries, err := os.ReadDir(exportDir)
	if err != nil {
		return fmt.Errorf("%s read export dir: %w", u.platform, err)
	}

	copied := 0
	hasDatasetDirs := false
	for _, entry := range entries {
		// Skip metadata files and git artifacts
		if entry.Name() == "metadata.csv" || entry.Name() == ".git" {
			continue
		}

		src := filepath.Join(exportDir, entry.Name())

		if entry.IsDir() {
			// Day-directory (dataset format): copy entire tree
			if len(entry.Name()) == 8 && isNumeric(entry.Name()) {
				hasDatasetDirs = true
				dst := filepath.Join(dataDir, entry.Name())
				n, err := copyDir(src, dst)
				if err != nil {
					u.appLogger.Warn("%s: copy dir %s failed: %v", u.platform, entry.Name(), err)
					continue
				}
				copied += n
			}
		} else if strings.HasSuffix(entry.Name(), ".jsonl") {
			// Flat JSONL file (reasoning/messages format)
			dst := filepath.Join(dataDir, entry.Name())
			if err := copyFile(src, dst); err != nil {
				u.appLogger.Warn("%s: copy %s failed: %v", u.platform, entry.Name(), err)
				continue
			}
			copied++
		}
	}

	if copied == 0 {
		return fmt.Errorf("no export files found in %s", exportDir)
	}

	if err := u.generateMetadataCSV(); err != nil {
		u.appLogger.Warn("%s: generate metadata.csv failed (non-fatal): %v", u.platform, err)
	}

	label := "latest"
	if hasDatasetDirs {
		label = "dataset"
	}
	if err := u.gitAddCommitPush(ctx, label, copied); err != nil {
		return fmt.Errorf("%s git push: %w", u.platform, err)
	}

	u.appLogger.Info("%s: successfully uploaded %d files", u.platform, copied)
	return nil
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   isNumeric 判断字符串是否全部由数字字符（'0'-'9'）组成。
 *
 * 处理流程：
 *   1. 遍历字符串中的每个 rune
 *   2. 如果存在任意一个字符不在 '0'~'9' 范围内，返回 false
 *   3. 全部通过检查则返回 true
 *
 * @param   s - 待检查的字符串
 * @return  bool - 全为数字返回 true，否则返回 false；空字符串返回 true
 *
 * @note    该函数用于判断目录名是否为合法的 YYYYMMDD 日期格式前缀，
 *          即 8 位纯数字字符串。
 */
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   copyDir 递归复制整个目录树，从源路径复制到目标路径。
 *
 * 处理流程：
 *   1. 使用 filepath.WalkDir 遍历源目录下的所有文件和子目录
 *   2. 对于每个条目，计算相对于源目录的相对路径
 *   3. 如果条目是目录，在目标路径创建对应的目录（含所有父目录）
 *   4. 如果条目是文件，调用 copyFile 复制文件内容
 *   5. 统计成功复制的文件数量
 *
 * @param   src - 源目录路径
 * @param   dst - 目标目录路径（不存在的父目录会自动创建）
 * @return  int  - 成功复制的文件数量
 * @return  error - 遍历或复制过程中遇到的第一个错误
 *
 * @note    目录项不会计入 count 中，只统计文件复制数量。
 *          遇到第一个错误时会立即中断遍历并返回。
 */
func copyDir(src, dst string) (int, error) {
	count := 0
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   syncRepo 将远程数据集仓库同步到本地。
 *
 * 处理流程：
 *   1. 检查仓库本地目录下是否存在 .git 子目录
 *   2. 如果不存在（首次使用）：
 *      a. 执行 git clone --depth 1 浅克隆远程仓库
 *      b. 克隆失败则返回错误
 *   3. 如果已存在：
 *      a. 执行 git pull --rebase 拉取最新变更
 *      b. 拉取失败则返回错误
 *   4. 如果配置了 GitUser，设置仓库级别的 user.name
 *   5. 如果配置了 GitEmail，设置仓库级别的 user.email
 *
 * @param   ctx - 上下文对象，用于取消克隆/拉取操作
 * @return  error - 成功返回 nil，Git 操作失败返回包含详细信息的错误
 *
 * @note    使用 --depth 1 进行浅克隆以减少网络传输和磁盘占用。
 *          git config 设置的 user.name 和 user.email 仅作用于该本地仓库，
 *          不会影响全局 Git 配置。即使设置失败也不会中断流程。
 */
func (u *DatasetUploader) syncRepo(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(u.cfg.RepoDir, ".git")); os.IsNotExist(err) {
		u.appLogger.Info("%s: cloning repo from %s", u.platform, u.cfg.RepoURL)
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", u.cfg.RepoURL, u.cfg.RepoDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
	} else {
		u.appLogger.Debug("%s: pulling latest from repo", u.platform)
		cmd := exec.CommandContext(ctx, "git", "-C", u.cfg.RepoDir, "pull", "--rebase")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git pull: %w", err)
		}
	}

	if u.cfg.GitUser != "" {
		exec.CommandContext(ctx, "git", "-C", u.cfg.RepoDir, "config", "user.name", u.cfg.GitUser).Run()
	}
	if u.cfg.GitEmail != "" {
		exec.CommandContext(ctx, "git", "-C", u.cfg.RepoDir, "config", "user.email", u.cfg.GitEmail).Run()
	}

	return nil
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   gitAddCommitPush 将本地仓库的变更暂存、提交并推送到远程分支。
 *
 * 处理流程：
 *   1. 确定目标分支名：如果配置了 GitBranch 则使用配置值，否则默认使用 "master"
 *   2. 执行 git add -A 暂存所有变更（新增、修改、删除）
 *   3. 执行 git diff --cached --quiet 检查是否有实际变更：
 *      a. 如果返回值 0（无差异），记录日志并直接返回 nil（无需提交）
 *      b. 如果返回值非 0（有差异），继续后续步骤
 *   4. 构造提交信息：格式为 "auto: upload dataset <dateLabel> (<rowCount> rows)"
 *      附加提交信息：上传时间戳（RFC3339 格式）
 *   5. 执行 git commit 创建提交
 *   6. 执行 git push origin <branch> 推送到远程仓库
 *
 * @param   ctx       - 上下文对象，用于取消 Git 操作
 * @param   dateLabel - 提交标签，通常为日期字符串 "YYYYMMDD" 或 "latest"/"dataset"
 * @param   rowCount  - 本次上传的数据行数，用于提交信息中展示
 * @return  error - 成功返回 nil，Git 操作失败返回包含详细信息的错误
 *
 * @note    使用 git diff --cached --quiet 预检查变更，避免产生空提交。
 *          如果仓库中没有任何变更，函数正常返回 nil 而不是错误。
 *          git push 的分支默认值为 "master"（不同于 HuggingFace 的 "main"），
 *          应通过配置文件中的 git_branch 字段指定正确的分支名。
 */
func (u *DatasetUploader) gitAddCommitPush(ctx context.Context, dateLabel string, rowCount int) error {
	branch := u.cfg.GitBranch
	if branch == "" {
		branch = "master"
	}

	cmd := exec.CommandContext(ctx, "git", "-C", u.cfg.RepoDir, "add", "-A")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	statusCmd := exec.CommandContext(ctx, "git", "-C", u.cfg.RepoDir, "diff", "--cached", "--quiet")
	if err := statusCmd.Run(); err == nil {
		u.appLogger.Info("%s: no changes to commit", u.platform)
		return nil
	}

	msg := fmt.Sprintf("auto: upload dataset %s (%d rows)", dateLabel, rowCount)
	nowTag := time.Now().UTC().Format(time.RFC3339)
	cmd = exec.CommandContext(ctx, "git", "-C", u.cfg.RepoDir, "commit", "-m", msg, "-m", "Uploaded at "+nowTag)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	u.appLogger.Info("%s: pushing to %s branch...", u.platform, branch)
	cmd = exec.CommandContext(ctx, "git", "-C", u.cfg.RepoDir, "push", "origin", branch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push: %w", err)
	}

	return nil
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   generateMetadataCSV 扫描仓库中所有日期目录下的 data 文件，
 *          生成聚合统计元数据 CSV。
 *
 * 该 CSV 文件用于数据集平台（ModelScope/HuggingFace）展示数据集的
 * 摘要信息，包括每日数据行数、模型分布、唯一哈希数、累计统计等。
 *
 * 处理流程：
 *   1. 检查仓库的 DataSubdir 目录是否存在，不存在则直接返回
 *   2. 遍历 DataSubdir 下的所有子目录（8 位日期目录）
 *   3. 对每个日期目录：
 *      a. 打开 reasoning.jsonl 文件
 *      b. 使用 bufio.Scanner 逐行扫描 JSONL 记录
 *      c. 反序列化为 exporter.DatasetRow，收集 rows 计数、model 统计、hash 去重
 *      d. 关闭文件，将有数据的 day 元信息追加到汇总列表
 *   4. 按日期升序排列所有天的元数据
 *   5. 在仓库根目录创建 metadata.csv 文件
 *   6. 写入 CSV 表头：date, rows, unique_hashes, models, cumulative_rows, cumulative_unique
 *   7. 逐日写入统计行：日期、当日行数、当日去重哈希数、模型列表（分号分隔）、累计行数、累计去重哈希数
 *
 * @return  error - 成功返回 nil，文件读写失败返回错误
 *
 * @note    CSV 中的 models 列以分号 ";" 分隔多个模型名，按字母序排列。
 *          cumulative_unique 为全局去重哈希数（所有日期的唯一哈希总数），
 *          而非逐日累计值。Scanner 缓冲区设置为 16MB 以处理大文件行。
 *          如果 data 目录为空或无有效数据，函数正常返回 nil 不创建 CSV。
 */
func (u *DatasetUploader) generateMetadataCSV() error {
	dataDir := filepath.Join(u.cfg.RepoDir, u.cfg.DataSubdir)
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		return nil
	}

	type dayMeta struct {
		date   string
		rows   int
		models map[string]int
		hashes map[string]struct{}
	}

	var allDays []dayMeta
	totalRows := 0
	allModels := make(map[string]int)
	allHashes := make(map[string]struct{})

	dayEntries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}

	for _, dayEntry := range dayEntries {
		if !dayEntry.IsDir() {
			continue
		}
		dateStr := dayEntry.Name()
		if len(dateStr) != 8 {
			continue
		}

		reasoningFile := filepath.Join(dataDir, dateStr, "reasoning.jsonl")
		f, err := os.Open(reasoningFile)
		if err != nil {
			continue
		}

		meta := dayMeta{date: dateStr, models: make(map[string]int), hashes: make(map[string]struct{})}
		sc := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 16*1024*1024)
		for sc.Scan() {
			b := sc.Bytes()
			if len(b) == 0 {
				continue
			}
			var row exporter.DatasetRow
			if json.Unmarshal(b, &row) != nil {
				continue
			}
			meta.rows++
			if row.Model != "" {
				meta.models[row.Model]++
			}
			if row.Hash != "" {
				meta.hashes[row.Hash] = struct{}{}
			}
		}
		f.Close()

		if meta.rows > 0 {
			allDays = append(allDays, meta)
			totalRows += meta.rows
			for m, c := range meta.models {
				allModels[m] += c
			}
			for h := range meta.hashes {
				allHashes[h] = struct{}{}
			}
		}
	}

	if len(allDays) == 0 {
		return nil
	}

	sort.Slice(allDays, func(i, j int) bool { return allDays[i].date < allDays[j].date })

	csvPath := filepath.Join(u.cfg.RepoDir, "metadata.csv")
	out, err := os.Create(csvPath)
	if err != nil {
		return err
	}
	defer out.Close()

	out.WriteString("date,rows,unique_hashes,models,cumulative_rows,cumulative_unique\n")

	cumulative := 0
	for _, d := range allDays {
		cumulative += d.rows
		modelList := make([]string, 0, len(d.models))
		for m := range d.models {
			modelList = append(modelList, m)
		}
		sort.Strings(modelList)
		modelStr := strings.Join(modelList, ";")
		line := fmt.Sprintf("%s,%d,%d,%s,%d,%d\n",
			d.date, d.rows, len(d.hashes), modelStr, cumulative, len(allHashes))
		out.WriteString(line)
	}

	u.appLogger.Info("%s: metadata.csv generated (%d days, %d total rows)", u.platform, len(allDays), totalRows)
	return nil
}

/*
 * @author  chensong
 * @date   2026-04-26
 * @brief   copyFile 将单个文件从源路径复制到目标路径。
 *
 * 处理流程：
 *   1. 调用 os.ReadFile 将源文件完整读入内存
 *   2. 调用 os.MkdirAll 确保目标文件的父目录存在（含所有父目录）
 *   3. 调用 os.WriteFile 将数据写入目标文件，权限设为 0644
 *
 * @param   src - 源文件路径
 * @param   dst - 目标文件路径
 * @return  error - 成功返回 nil，读取或写入失败返回系统错误
 *
 * @note    该函数将整个文件读入内存，不适合复制超大文件（> 数百 MB）。
 *          对于 JSONL 数据集文件（通常几 MB 到几十 MB）是合适的实现方式。
 *          目标文件权限为 0644（owner 读写，group 和 others 只读）。
 */
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
