package deploy

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CollectLogsOptions struct {
	OutputDir string
}

type StatsLogsOptions struct {
	OutputDir string
}

type LogCollectManifest struct {
	CollectedAt string                    `json:"collectedAt"`
	Entries     []LogCollectManifestEntry `json:"entries"`
}

type LogCollectManifestEntry struct {
	IP             string `json:"ip"`
	Name           string `json:"name"`
	ServiceType    string `json:"serviceType"`
	Category       string `json:"category"` // deploy | runtime
	RemotePath     string `json:"remotePath"`
	LocalGz        string `json:"localGz"`
	Lines          int64  `json:"lines"`
	SizeCompressed int64  `json:"sizeCompressed"`
	Skipped        bool   `json:"skipped"`
	SkipReason     string `json:"skipReason,omitempty"`
}

type remoteLogTarget struct {
	Category     string
	RemotePath   string
	LocalNameGz  string
	SkipByDesign bool
	SkipReason   string
}

type logStatRow struct {
	Path       string
	Category   string
	Source     string
	IP         string
	Name       string
	Lines      int64
	SizeBytes  int64
	Compressed bool
}

func CollectLogs(ctx context.Context, commonCfg CommonConfig, opts CollectLogsOptions) error {
	outputDir := resolveOutputDir(commonCfg, opts.OutputDir)
	mgr, err := LoadOutputManager(outputDir)
	if err != nil {
		return fmt.Errorf("加载 output 失败: %w", err)
	}
	statuses := mgr.SnapshotStatuses()
	if len(statuses) == 0 {
		return fmt.Errorf("未找到可收集节点（script_status.json 为空）")
	}
	sort.Slice(statuses, func(i, j int) bool {
		li := fmt.Sprintf("%s|%s|%s", statuses[i].ServiceType, statuses[i].Name, statuses[i].IP)
		lj := fmt.Sprintf("%s|%s|%s", statuses[j].ServiceType, statuses[j].Name, statuses[j].IP)
		return li < lj
	})

	collectNodeCount := 0
	for _, st := range statuses {
		if st == nil {
			continue
		}
		if strings.EqualFold(st.ServiceType, "xjst") && !shouldCollectXjstNodeByName(st.Name) {
			continue
		}
		collectNodeCount++
	}

	keyPath := buildSSHKeyPath(commonCfg)
	collectedDir := filepath.Join(commonCfg.LogDir, "collected")
	if err := os.MkdirAll(collectedDir, 0o755); err != nil {
		return fmt.Errorf("创建 collected 目录失败: %w", err)
	}

	log.Printf("👉 [collect-logs] 开始收集，script_status 节点数=%d，可收集节点数=%d，collected 目录=%s\n",
		len(statuses), collectNodeCount, collectedDir)

	manifest := LogCollectManifest{
		CollectedAt: time.Now().UTC().Format(time.RFC3339),
		Entries:     make([]LogCollectManifestEntry, 0),
	}
	var allErrs []error

	for _, st := range statuses {
		if st == nil {
			continue
		}
		if strings.EqualFold(st.ServiceType, "xjst") && !shouldCollectXjstNodeByName(st.Name) {
			log.Printf("ℹ️ [collect-logs] 跳过非 node-1 XJST 节点: %s (%s)\n", st.Name, st.IP)
			continue
		}
		targets := buildCollectTargets(st)
		serverDirName := fmt.Sprintf("%s_%s", strings.TrimSpace(st.IP), strings.TrimSpace(st.Name))
		serverLocalDir := filepath.Join(collectedDir, serverDirName)
		if err := os.MkdirAll(serverLocalDir, 0o755); err != nil {
			allErrs = append(allErrs, fmt.Errorf("[%s][%s] 创建本地目录失败: %w", st.IP, st.Name, err))
			continue
		}

		log.Printf("👉 [collect-logs][%s][%s] 开始收集 (%s)，目标文件数=%d\n",
			st.IP, st.Name, st.ServiceType, len(targets))

		for _, target := range targets {
			entry := LogCollectManifestEntry{
				IP:          st.IP,
				Name:        st.Name,
				ServiceType: st.ServiceType,
				Category:    target.Category,
				RemotePath:  target.RemotePath,
				LocalGz: filepath.ToSlash(filepath.Join(
					commonCfg.LogDir,
					"collected",
					serverDirName,
					target.LocalNameGz,
				)),
			}

			log.Printf("  ▶ [%s][%s] %s -> %s\n",
				target.Category, target.LocalNameGz, target.RemotePath, entry.LocalGz)

			if target.SkipByDesign {
				entry.Skipped = true
				entry.SkipReason = target.SkipReason
				log.Printf("  ℹ️ [%s][%s] 按设计跳过: %s\n", st.IP, st.Name, target.SkipReason)
				manifest.Entries = append(manifest.Entries, entry)
				continue
			}

			log.Printf("  📊 [%s][%s] 统计远端行数: %s\n", st.IP, st.Name, target.RemotePath)
			lines, exists, err := probeRemoteFileLines(ctx, commonCfg.SSHUser, keyPath, st.IP, target.RemotePath)
			if err != nil {
				entry.Skipped = true
				entry.SkipReason = err.Error()
				allErrs = append(allErrs, fmt.Errorf("[%s][%s] 统计行数失败(%s): %w", st.IP, st.Name, target.RemotePath, err))
				log.Printf("  ⚠️ [%s][%s] 统计行数失败: %v\n", st.IP, st.Name, err)
				manifest.Entries = append(manifest.Entries, entry)
				continue
			}
			if !exists {
				entry.Skipped = true
				entry.SkipReason = "远端文件不存在"
				log.Printf("  ℹ️ [%s][%s] 远端文件不存在，跳过: %s\n", st.IP, st.Name, target.RemotePath)
				manifest.Entries = append(manifest.Entries, entry)
				continue
			}

			entry.Lines = lines
			log.Printf("  📊 [%s][%s] 行数=%d\n", st.IP, st.Name, lines)
			remoteTmpPath := filepath.ToSlash(filepath.Join(
				"/tmp",
				fmt.Sprintf("ydyl-collect-%d-%s", time.Now().UnixNano(), target.LocalNameGz),
			))
			log.Printf("  🗜️ [%s][%s] 远端压缩中: %s\n", st.IP, st.Name, target.RemotePath)
			if err := gzipRemoteFile(ctx, commonCfg.SSHUser, keyPath, st.IP, target.RemotePath, remoteTmpPath); err != nil {
				entry.Skipped = true
				entry.SkipReason = err.Error()
				allErrs = append(allErrs, fmt.Errorf("[%s][%s] 压缩失败(%s): %w", st.IP, st.Name, target.RemotePath, err))
				log.Printf("  ⚠️ [%s][%s] 压缩失败: %v\n", st.IP, st.Name, err)
				manifest.Entries = append(manifest.Entries, entry)
				continue
			}
			log.Printf("  🗜️ [%s][%s] 远端压缩完成\n", st.IP, st.Name)

			log.Printf("  📥 [%s][%s] rsync 拉回中...\n", st.IP, st.Name)
			rsyncErr := rsyncRemoteFile(ctx, commonCfg.SSHUser, keyPath, st.IP, remoteTmpPath, serverLocalDir)
			if cleanupErr := removeRemoteFile(ctx, commonCfg.SSHUser, keyPath, st.IP, remoteTmpPath); cleanupErr != nil {
				allErrs = append(allErrs, fmt.Errorf("[%s][%s] 清理远端临时压缩包失败(%s): %w", st.IP, st.Name, remoteTmpPath, cleanupErr))
				log.Printf("  ⚠️ [%s][%s] 清理远端临时压缩包失败: %v\n", st.IP, st.Name, cleanupErr)
			}
			if rsyncErr != nil {
				entry.Skipped = true
				entry.SkipReason = rsyncErr.Error()
				allErrs = append(allErrs, fmt.Errorf("[%s][%s] rsync 失败(%s): %w", st.IP, st.Name, remoteTmpPath, rsyncErr))
				log.Printf("  ⚠️ [%s][%s] rsync 失败: %v\n", st.IP, st.Name, rsyncErr)
				manifest.Entries = append(manifest.Entries, entry)
				continue
			}

			downloadedPath := filepath.Join(serverLocalDir, filepath.Base(remoteTmpPath))
			finalPath := filepath.Join(serverLocalDir, target.LocalNameGz)
			if err := os.Rename(downloadedPath, finalPath); err != nil {
				entry.Skipped = true
				entry.SkipReason = fmt.Sprintf("重命名本地文件失败: %v", err)
				allErrs = append(allErrs, fmt.Errorf("[%s][%s] 重命名本地文件失败: %w", st.IP, st.Name, err))
				log.Printf("  ⚠️ [%s][%s] 重命名本地文件失败: %v\n", st.IP, st.Name, err)
				manifest.Entries = append(manifest.Entries, entry)
				continue
			}

			info, statErr := os.Stat(finalPath)
			if statErr != nil {
				entry.Skipped = true
				entry.SkipReason = fmt.Sprintf("读取本地压缩包大小失败: %v", statErr)
				allErrs = append(allErrs, fmt.Errorf("[%s][%s] 读取本地压缩包失败: %w", st.IP, st.Name, statErr))
				log.Printf("  ⚠️ [%s][%s] 读取本地压缩包失败: %v\n", st.IP, st.Name, statErr)
				manifest.Entries = append(manifest.Entries, entry)
				continue
			}
			entry.SizeCompressed = info.Size()
			entry.LocalGz = filepath.ToSlash(filepath.Join(commonCfg.LogDir, "collected", serverDirName, target.LocalNameGz))
			manifest.Entries = append(manifest.Entries, entry)
			log.Printf("  ✅ [%s][%s] 已保存 %s（%d 行, %d 字节）\n",
				st.IP, st.Name, entry.LocalGz, entry.Lines, entry.SizeCompressed)
		}
	}

	manifestPath := filepath.Join(collectedDir, "manifest.json")
	log.Printf("👉 [collect-logs] 写入 manifest: %s\n", manifestPath)
	if err := writeJSONFile(manifestPath, manifest); err != nil {
		return fmt.Errorf("写 manifest 失败: %w", err)
	}
	if len(allErrs) > 0 {
		log.Printf("⚠️ [collect-logs] 完成但有 %d 项失败，manifest 条目数=%d\n", len(allErrs), len(manifest.Entries))
		return errors.Join(allErrs...)
	}
	log.Printf("✅ [collect-logs] 全部完成，manifest 条目数=%d\n", len(manifest.Entries))
	return nil
}

func StatsLogs(_ context.Context, commonCfg CommonConfig, opts StatsLogsOptions) (string, error) {
	outputDir := resolveOutputDir(commonCfg, opts.OutputDir)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("创建 output 目录失败: %w", err)
	}

	manifestPath := filepath.Join(commonCfg.LogDir, "collected", "manifest.json")
	manifestMap := loadManifestMap(manifestPath)
	rows := make([]logStatRow, 0)

	if clientRows, err := collectClientLogRows(commonCfg.LogDir); err == nil {
		rows = append(rows, clientRows...)
	} else {
		return "", err
	}
	if pipeRows, err := collectPipeLogRows(commonCfg.LogDir); err == nil {
		rows = append(rows, pipeRows...)
	} else {
		return "", err
	}
	if gzRows, err := collectCompressedRows(commonCfg.LogDir, manifestMap); err == nil {
		rows = append(rows, gzRows...)
	} else {
		return "", err
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Category != rows[j].Category {
			return rows[i].Category < rows[j].Category
		}
		return rows[i].Path < rows[j].Path
	})

	outPath := filepath.Join(outputDir, "log_stats.csv")
	if err := writeLogStatsCSV(outPath, rows); err != nil {
		return "", err
	}
	return outPath, nil
}

func resolveOutputDir(commonCfg CommonConfig, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	if strings.TrimSpace(commonCfg.OutputDir) != "" {
		return strings.TrimSpace(commonCfg.OutputDir)
	}
	return filepath.Join(commonCfg.LogDir, "output")
}

func buildCollectTargets(st *ScriptStatus) []remoteLogTarget {
	if st == nil {
		return nil
	}
	targets := []remoteLogTarget{
		{
			Category:    "deploy",
			RemotePath:  st.LogPath,
			LocalNameGz: "deploy-pipe.log.gz",
		},
	}

	switch strings.ToLower(strings.TrimSpace(st.ServiceType)) {
	case "cdk":
		targets = append(targets,
			remoteLogTarget{
				Category:    "deploy",
				RemotePath:  "/home/ubuntu/workspace/ydyl-deployment-suite/cdk-work/scripts/deploy-gen.log",
				LocalNameGz: "deploy-kurtosis-cdk.log.gz",
			},
			remoteLogTarget{
				Category:    "runtime",
				RemotePath:  buildRuntimeLogPath(st.Name),
				LocalNameGz: "runtime.log.gz",
			},
		)
	case "op":
		targets = append(targets,
			remoteLogTarget{
				Category:    "deploy",
				RemotePath:  "/home/ubuntu/workspace/ydyl-deployment-suite/op-work/scripts/deploy-gen.log",
				LocalNameGz: "deploy-kurtosis-op.log.gz",
			},
			remoteLogTarget{
				Category:    "runtime",
				RemotePath:  buildRuntimeLogPath(st.Name),
				LocalNameGz: "runtime.log.gz",
			},
		)
	case "xjst":
		targets = append(targets, remoteLogTarget{
			Category:    "runtime",
			RemotePath:  buildRuntimeLogPath(st.Name),
			LocalNameGz: "runtime.log.gz",
		})
	}
	return targets
}

func shouldCollectXjstNodeByName(name string) bool {
	re := regexp.MustCompile(`-(\d+)$`)
	matches := re.FindStringSubmatch(strings.TrimSpace(name))
	if len(matches) != 2 {
		return false
	}
	idx, err := strconv.Atoi(matches[1])
	if err != nil || idx <= 0 {
		return false
	}
	return (idx-1)%4 == 0
}

func probeRemoteFileLines(ctx context.Context, user, keyPath, ip, remotePath string) (int64, bool, error) {
	quoted := shellQuote(remotePath)
	cmd := fmt.Sprintf("if [ -f %s ]; then wc -l < %s; else echo __MISSING__; fi", quoted, quoted)
	out, err := runSSH(ctx, user, keyPath, ip, cmd)
	if err != nil {
		return 0, false, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "__MISSING__" {
		return 0, false, nil
	}
	lines, parseErr := strconv.ParseInt(trimmed, 10, 64)
	if parseErr != nil {
		return 0, false, fmt.Errorf("解析 wc 输出失败: %q", trimmed)
	}
	return lines, true, nil
}

func gzipRemoteFile(ctx context.Context, user, keyPath, ip, remotePath, remoteTmpPath string) error {
	cmd := fmt.Sprintf("gzip -c %s > %s", shellQuote(remotePath), shellQuote(remoteTmpPath))
	_, err := runSSH(ctx, user, keyPath, ip, cmd)
	return err
}

func rsyncRemoteFile(ctx context.Context, user, keyPath, ip, remotePath, localDir string) error {
	sshSpec := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o IdentitiesOnly=yes -i %s", keyPath)
	src := fmt.Sprintf("%s@%s:%s", user, ip, remotePath)
	cmd := exec.CommandContext(ctx, "rsync", "-az", "-e", sshSpec, src, localDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync 失败: %w, output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeRemoteFile(ctx context.Context, user, keyPath, ip, remotePath string) error {
	_, err := runSSH(ctx, user, keyPath, ip, fmt.Sprintf("rm -f %s", shellQuote(remotePath)))
	return err
}

func runSSH(ctx context.Context, user, keyPath, ip, remoteCmd string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-i", keyPath,
		fmt.Sprintf("%s@%s", user, ip),
		remoteCmd,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh 失败: %w, output=%s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func collectClientLogRows(logDir string) ([]logStatRow, error) {
	pattern := filepath.Join(logDir, "client", "*.log")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("扫描 client 日志失败: %w", err)
	}
	rows := make([]logStatRow, 0, len(matches))
	for _, p := range matches {
		lines, size, err := countPlainFileLinesAndSize(p)
		if err != nil {
			return nil, err
		}
		rows = append(rows, logStatRow{
			Path:       filepath.ToSlash(p),
			Category:   "client",
			Source:     "local",
			Lines:      lines,
			SizeBytes:  size,
			Compressed: false,
		})
	}
	return rows, nil
}

func collectPipeLogRows(logDir string) ([]logStatRow, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return nil, fmt.Errorf("扫描 pipe 日志失败: %w", err)
	}
	rows := make([]logStatRow, 0)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(e.Name())) != ".log" {
			continue
		}
		fullPath := filepath.Join(logDir, e.Name())
		lines, size, err := countPlainFileLinesAndSize(fullPath)
		if err != nil {
			return nil, err
		}
		name, ip := parseNameAndIPFromPipeLog(e.Name())
		rows = append(rows, logStatRow{
			Path:       filepath.ToSlash(fullPath),
			Category:   "pipe",
			Source:     "local",
			IP:         ip,
			Name:       name,
			Lines:      lines,
			SizeBytes:  size,
			Compressed: false,
		})
	}
	return rows, nil
}

func collectCompressedRows(logDir string, manifest map[string]LogCollectManifestEntry) ([]logStatRow, error) {
	collectedDir := filepath.Join(logDir, "collected")
	if _, err := os.Stat(collectedDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	rows := make([]logStatRow, 0)
	walkErr := filepath.WalkDir(collectedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".gz" {
			return nil
		}

		info, statErr := os.Stat(path)
		if statErr != nil {
			return statErr
		}

		entry, ok := lookupManifestEntry(manifest, path)
		lines := int64(0)
		category := "pipe"
		ip := ""
		name := ""
		if ok {
			lines = entry.Lines
			ip = entry.IP
			name = entry.Name
			if strings.EqualFold(entry.Category, "runtime") {
				category = "runtime"
			} else if strings.Contains(strings.ToLower(entry.RemotePath), "deploy-gen.log") {
				category = "kurtosis-deploy"
			}
		} else {
			cnt, cntErr := countGzipLines(path)
			if cntErr != nil {
				return cntErr
			}
			lines = cnt
			if strings.Contains(strings.ToLower(path), "runtime") {
				category = "runtime"
			}
		}

		rows = append(rows, logStatRow{
			Path:       filepath.ToSlash(path),
			Category:   category,
			Source:     "collected",
			IP:         ip,
			Name:       name,
			Lines:      lines,
			SizeBytes:  info.Size(),
			Compressed: true,
		})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("扫描 collected 压缩包失败: %w", walkErr)
	}
	return rows, nil
}

func writeLogStatsCSV(path string, rows []logStatRow) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"path", "category", "source", "ip", "name", "lines", "size_bytes", "compressed"}); err != nil {
		return err
	}
	for _, row := range rows {
		record := []string{
			row.Path,
			row.Category,
			row.Source,
			row.IP,
			row.Name,
			strconv.FormatInt(row.Lines, 10),
			strconv.FormatInt(row.SizeBytes, 10),
			strconv.FormatBool(row.Compressed),
		}
		if err := w.Write(record); err != nil {
			return err
		}
	}
	return w.Error()
}

func countPlainFileLinesAndSize(path string) (int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("打开日志文件失败(%s): %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("读取日志文件信息失败(%s): %w", path, err)
	}

	var lines int64
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("读取日志行失败(%s): %w", path, err)
	}
	return lines, info.Size(), nil
}

func countGzipLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("打开压缩日志失败(%s): %w", path, err)
	}
	defer f.Close()
	gzReader, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("打开 gzip reader 失败(%s): %w", path, err)
	}
	defer gzReader.Close()

	var lines int64
	reader := bufio.NewReader(gzReader)
	for {
		_, readErr := reader.ReadString('\n')
		if readErr == nil {
			lines++
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return 0, fmt.Errorf("读取压缩日志行失败(%s): %w", path, readErr)
	}
	return lines, nil
}

func loadManifestMap(path string) map[string]LogCollectManifestEntry {
	out := make(map[string]LogCollectManifestEntry)
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var manifest LogCollectManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return out
	}
	for _, entry := range manifest.Entries {
		key := filepath.ToSlash(filepath.Clean(entry.LocalGz))
		out[key] = entry
		if filepath.IsAbs(entry.LocalGz) {
			continue
		}
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			continue
		}
		absKey := filepath.ToSlash(filepath.Clean(filepath.Join(wd, entry.LocalGz)))
		out[absKey] = entry
	}
	return out
}

func lookupManifestEntry(manifest map[string]LogCollectManifestEntry, path string) (LogCollectManifestEntry, bool) {
	key := filepath.ToSlash(filepath.Clean(path))
	if entry, ok := manifest[key]; ok {
		return entry, true
	}
	wd, err := os.Getwd()
	if err == nil {
		if rel, relErr := filepath.Rel(wd, path); relErr == nil {
			relKey := filepath.ToSlash(filepath.Clean(rel))
			if entry, ok := manifest[relKey]; ok {
				return entry, true
			}
		}
	}
	return LogCollectManifestEntry{}, false
}

func parseNameAndIPFromPipeLog(fileName string) (name, ip string) {
	re := regexp.MustCompile(`^(.*)-((?:\d{1,3}\.){3}\d{1,3})\.log$`)
	matches := re.FindStringSubmatch(strings.TrimSpace(fileName))
	if len(matches) != 3 {
		return "", ""
	}
	return matches[1], matches[2]
}
