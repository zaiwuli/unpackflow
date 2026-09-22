package unpackerr

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Unpackerr/unpackerr/pkg/clouddrive"
	"golift.io/xtractr"
)

func (u *Unpackerr) startCloudDriveMonitor() {
	cfg := u.CloudDrive2
	if !cfg.Enabled {
		return
	}
	if cfg.URL == "" || cfg.Token == "" {
		u.Errorf("CloudDrive2 直连需要填写服务地址和 Token")
		return
	}
	client := &clouddrive.Client{BaseURL: cfg.URL, Token: cfg.Token}
	watchPaths := cloudDriveManualWatchPaths(cfg)
	monitor := &clouddrive.Monitor{
		Client: client,
		Config: clouddrive.MonitorConfig{
			ReconnectMin: cfg.ReconnectMin.Duration,
			ReconnectMax: cfg.ReconnectMax.Duration,
		},
		PathOverrides: cfg.PathOverrides,
		OnChange: func(change clouddrive.Change, paths []string) error {
			if u.taskSystemPaused.Load() {
				return nil
			}
			currentWatchPaths := cloudDriveManualWatchPaths(u.CloudDrive2)
			if !cloudDrivePathMatches(change.Path, currentWatchPaths) && !cloudDrivePathMatches(change.NewPath, currentWatchPaths) {
				return nil
			}
			eventPath := change.Path
			if change.NewPath != "" {
				eventPath = change.NewPath
			}
			if change.Type == 1 || change.IsDirectory || !isCloudDriveArchiveEvent(eventPath) { // delete, directory or irrelevant file
				return nil
			}
			// CloudDrive2 can publish the remote event before its mounted
			// filesystem exposes the file. Refresh the affected remote directory
			// and wait briefly for the mapped path before starting the copy.
			currentPaths := clouddrive.MapCloudPathWithOverrides(eventPath, nil, u.CloudDrive2.PathOverrides)
			if len(currentPaths) == 0 {
				currentPaths = paths
			}
			go u.handleCloudDriveChange(client, change, currentPaths)
			return nil
		},
		OnStatus: func(status clouddrive.Status) {
			if status.LastError != "" {
				u.Errorf("CloudDrive2 监控：%s", status.LastError)
			}
		},
	}
	u.cd2Mu.Lock()
	u.cd2Client = monitor.Client
	u.cd2Mu.Unlock()
	go monitor.Run(context.Background())
	u.Printf("CloudDrive2 监控已连接：%s", cfg.URL)
	go u.resume115LocalDownloads()
	if cfg.FallbackScanEnabled {
		go u.cloudDriveFallbackScanPaths(monitor.Client, watchPaths, cfg.PathOverrides)
	}
	go u.cloudDriveRetryLoop()
	if cfg.RefreshInterval.Duration > 0 {
		go u.cloudDriveRefreshLoop(monitor.Client, cfg.RefreshInterval.Duration)
	}
	if cfg.FallbackScanEnabled && cfg.FallbackScanInterval.Duration > 0 {
		go u.cloudDriveFallbackLoop(monitor.Client, cfg.FallbackScanInterval.Duration)
	}
}

func (u *Unpackerr) resume115LocalDownloads() {
	if u.state == nil || u.taskSystemPaused.Load() {
		return
	}
	u.state.mu.RLock()
	items := make([]Pending115, 0, len(u.state.Fallback115))
	seen := make(map[string]struct{})
	for _, item := range u.state.Fallback115 {
		identity := item.TaskKey
		if identity == "" {
			identity = item.SourceCID + "|" + item.FID
		}
		if item.Approval {
			continue
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		items = append(items, item)
	}
	u.state.mu.RUnlock()
	for _, item := range items {
		if item.FID == "" || item.FileName == "" || !isCloudDriveArchiveEvent(item.FileName) {
			u.Systemf("清理无效的 115 待处理下载记录：%s", item.TaskKey)
			u.removePending115Task(item.TaskKey)
			continue
		}
		currentPath, ok := u.current115PendingPath(item)
		if !ok {
			u.Systemf("115 待处理任务已不属于当前配置，停止恢复：%s", item.FileName)
			u.removePending115Task(item.TaskKey)
			continue
		}
		u.refresh115Fallback(N115Mapping{FallbackCID: item.FallbackCID, CD2Path: currentPath}, n115File{FID: item.FID, Name: item.FileName, Size: item.Size, MTime: item.MTime})
	}
}

func (u *Unpackerr) current115PendingPath(item Pending115) (string, bool) {
	if item.Kind == "cloud_failure" {
		value := normalizeCloudDrivePath(u.CloudDrive2.N115FailureCD2Path)
		return value, value != ""
	}
	for _, mapping := range parse115DownloadMappings(u.CloudDrive2.N115DownloadMappings) {
		if mapping.CID == item.SourceCID || mapping.CID == item.FallbackCID {
			return mapping.CD2Path, true
		}
	}
	return "", false
}

func (u *Unpackerr) handleCloudDriveChange(client *clouddrive.Client, change clouddrive.Change, paths []string) {
	remotePath := change.Path
	if change.NewPath != "" {
		remotePath = change.NewPath
	}
	taskKey := u.beginCD2EventTask(paths, remotePath)
	refreshPath := path.Dir(remotePath)
	if change.IsDirectory {
		refreshPath = remotePath
	}
	if refreshPath == "." || refreshPath == "" {
		refreshPath = "/"
	}
	if err := client.ForceRefresh(context.Background(), refreshPath); err != nil {
		u.Debugf("CloudDrive2 事件目录刷新失败，继续等待挂载文件：%s：%v", refreshPath, err)
	} else {
		u.Debugf("CloudDrive2 已刷新事件目录：%s", refreshPath)
	}

	delays := []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second}
	for index, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
			if err := client.ForceRefresh(context.Background(), refreshPath); err != nil {
				u.Debugf("CloudDrive2 第 %d 次事件目录刷新失败：%v", index+1, err)
			}
		}
		if visibleCD2Paths(paths) {
			if taskKey != "" {
				u.updateCD2Transfer(taskKey, firstVisibleCD2Path(paths, remotePath), "检查文件完整性", nil)
			}
			u.cacheCloudDrivePaths(paths)
			return
		}
		u.Debugf("CloudDrive2 实时事件已收到，但挂载文件尚未出现，等待第 %d 次重试", index+1)
	}
	source := "CD2 实时推送"
	if value, ok := u.cd2Tasks.Load(taskKey); ok {
		if transfer, valid := value.(*CD2Transfer); valid && transfer != nil && transfer.Source != "" {
			source = transfer.Source
		}
	}
	u.Errorf("[%s] 挂载文件未出现：%s", source, remotePath)
	if taskKey != "" {
		u.updateCD2Transfer(taskKey, remotePath, "等待文件可见", func(transfer *CD2Transfer) {
			transfer.Error = "挂载文件尚未出现，将等待补偿扫描"
		})
	}
}

func firstVisibleCD2Path(paths []string, fallback string) string {
	for _, file := range paths {
		if _, err := os.Stat(file); err == nil {
			return file
		}
	}
	return fallback
}

func visibleCD2Paths(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, file := range paths {
		if _, err := os.Stat(file); err == nil {
			return true
		}
	}
	return false
}

func cloudDrivePathMatch(value, root string) bool {
	root = path.Clean("/" + strings.TrimLeft(strings.TrimSpace(root), "/"))
	value = path.Clean("/" + strings.TrimLeft(strings.TrimSpace(value), "/"))
	return root == "/" || value == root || strings.HasPrefix(value, root+"/")
}

func cloudDrivePathMatches(value string, roots []string) bool {
	for _, root := range roots {
		if strings.TrimSpace(root) != "" && cloudDrivePathMatch(value, root) {
			return true
		}
	}
	return false
}

// cloudDriveManualWatchPaths are folders where a user deliberately puts an
// archive for local cache-and-extract. They are independent from 115's failed
// cloud-extract fallback folders.
func cloudDriveManualWatchPaths(cfg CloudDriveConfig) []string {
	paths := make([]string, 0, len(cfg.ManualWatchPaths)+len(cfg.N115DownloadMappings)+2)
	seen := make(map[string]struct{})
	values := append([]string(nil), cfg.ManualWatchPaths...)
	// WatchPath is retained only as an upgrade bridge. A root value used by
	// older defaults must not turn approval-only folders into automatic jobs.
	if cfg.ManualWatchPaths == nil && strings.TrimSpace(cfg.WatchPath) != "" && strings.TrimSpace(cfg.WatchPath) != "/" {
		values = append(values, cfg.WatchPath)
	}
	for _, mapping := range parse115DownloadMappings(cfg.N115DownloadMappings) {
		if !mapping.Approval {
			values = append(values, mapping.CD2Path)
		}
	}
	if cfg.N115AutoFallback && strings.TrimSpace(cfg.N115FailureCD2Path) != "" {
		values = append(values, cfg.N115FailureCD2Path)
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		value = path.Clean("/" + strings.TrimLeft(value, "/"))
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			paths = append(paths, value)
		}
	}
	return paths
}

func cloudDriveConfiguredRefreshPaths(cfg CloudDriveConfig) []string {
	values := append([]string(nil), cloudDriveManualWatchPaths(cfg)...)
	for _, mapping := range parse115DownloadMappings(cfg.N115DownloadMappings) {
		values = append(values, mapping.CD2Path)
	}
	if strings.TrimSpace(cfg.N115FailureCD2Path) != "" {
		values = append(values, cfg.N115FailureCD2Path)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = path.Clean("/" + strings.TrimLeft(strings.TrimSpace(value), "/"))
		if value == "/" || value == "." {
			continue
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func (u *Unpackerr) cloudDriveRefreshLoop(client *clouddrive.Client, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if u.taskSystemPaused.Load() {
			continue
		}
		for _, refreshPath := range cloudDriveConfiguredRefreshPaths(u.CloudDrive2) {
			if err := client.ForceRefresh(context.Background(), refreshPath); err != nil {
				u.Errorf("CloudDrive2 定时刷新失败：%s：%v", refreshPath, err)
			} else {
				u.Debugf("CloudDrive2 定时刷新完成：%s", refreshPath)
			}
		}
	}
}

func (u *Unpackerr) cloudDriveFallbackLoop(client *clouddrive.Client, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if !u.taskSystemPaused.Load() && u.CloudDrive2.FallbackScanEnabled {
			u.cloudDriveFallbackScanPaths(client, cloudDriveManualWatchPaths(u.CloudDrive2), u.CloudDrive2.PathOverrides)
		}
	}
}

// cloudDriveFallbackScan compensates for delayed or missed change events. It
// scans only the configured watch path after mapping it to the mounted path.
func (u *Unpackerr) cloudDriveFallbackScan(client *clouddrive.Client, watchPath string, overrides []string) int {
	return u.cloudDriveFallbackScanPaths(client, []string{watchPath}, overrides)
}

func (u *Unpackerr) cloudDriveFallbackScanPaths(client *clouddrive.Client, watchPaths []string, overrides []string) int {
	total := 0
	for _, watchPath := range watchPaths {
		total += u.cloudDriveFallbackScanOne(client, watchPath, overrides)
	}
	return total
}

func (u *Unpackerr) cloudDriveFallbackScanOne(client *clouddrive.Client, watchPath string, overrides []string) int {
	// A user-provided direct mapping is the most reliable source inside a
	// container. Use it without requiring CD2's mount-point API to succeed.
	roots := clouddrive.MapCloudPathWithOverrides(watchPath, nil, overrides)
	if len(roots) == 0 {
		mounts, err := client.GetMountPoints(context.Background())
		if err != nil {
			u.Errorf("CloudDrive2 补偿扫描读取挂载点失败：%v；请检查路径映射", err)
			return 0
		}
		roots = clouddrive.MapCloudPathWithOverrides(watchPath, mounts, overrides)
	}
	if len(roots) == 0 {
		u.Errorf("CloudDrive2 补偿扫描无法映射监控路径：%s", watchPath)
		return 0
	}
	found := 0
	for _, root := range roots {
		root = filepath.Clean(root)
		paths := make([]string, 0, 16)
		stat, statErr := os.Stat(root)
		if statErr != nil {
			u.Errorf("CloudDrive2 补偿扫描目录不可访问 %s：%v", root, statErr)
			continue
		}
		if !stat.IsDir() {
			u.Errorf("CloudDrive2 补偿扫描路径不是目录：%s", root)
			continue
		}
		err := filepath.WalkDir(root, func(file string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				u.Errorf("CloudDrive2 补偿扫描跳过不可访问路径 %s：%v", file, walkErr)
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() && xtractr.IsArchiveFile(entry.Name()) {
				paths = append(paths, file)
			}
			return nil
		})
		if err != nil {
			u.Errorf("CloudDrive2 补偿扫描失败 %s：%v", root, err)
			continue
		}
		if len(paths) > 0 {
			found += len(paths)
			submitted := u.cacheCloudDrivePaths(paths)
			u.Printf("CloudDrive2 补偿扫描：在 %s 发现 %d 个压缩文件，提交 %d 个复制任务", root, len(paths), submitted)
		} else {
			u.Debugf("CloudDrive2 补偿扫描：%s 暂无压缩文件", root)
		}
	}
	return found
}

func (u *Unpackerr) cloudDriveRetryLoop() {
	u.resumeCD2Pending()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if !u.taskSystemPaused.Load() {
			u.resumeCD2Pending()
		}
	}
}

func changeName(value int) string {
	switch value {
	case 1:
		return "delete"
	case 2:
		return "rename"
	default:
		return "create"
	}
}
