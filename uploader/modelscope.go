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

// DatasetUploader pushes exported data to a dataset repository (ModelScope / HuggingFace) via Git.
type DatasetUploader struct {
	cfg       config.DatasetRepoConfig
	appLogger *logger.Logger
	platform  string // "modelscope" or "huggingface"
}

// NewModelScope creates a new ModelScope uploader.
func NewModelScope(cfg config.DatasetRepoConfig, appLogger *logger.Logger) *DatasetUploader {
	return &DatasetUploader{cfg: cfg, appLogger: appLogger, platform: "modelscope"}
}

// NewHuggingFace creates a new HuggingFace uploader.
func NewHuggingFace(cfg config.DatasetRepoConfig, appLogger *logger.Logger) *DatasetUploader {
	return &DatasetUploader{cfg: cfg, appLogger: appLogger, platform: "huggingface"}
}

// UploadDay exports the given day's data and pushes it to the repo.
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

// UploadLatestExport copies all files from the export directory into the repo and pushes.
// Handles both flat JSONL files (reasoning/messages format) and day-directory trees (dataset format).
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

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// copyDir recursively copies a directory tree.
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

// syncRepo clones the repo if it does not exist, otherwise pulls the latest.
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

// gitAddCommitPush stages all changes, commits, and pushes to the remote.
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

// generateMetadataCSV creates a metadata.csv file with aggregate statistics for all data files.
func (u *DatasetUploader) generateMetadataCSV() error {
	dataDir := filepath.Join(u.cfg.RepoDir, u.cfg.DataSubdir)
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		return nil
	}

	type dayMeta struct {
		date    string
		rows    int
		models  map[string]int
		hashes  map[string]struct{}
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
