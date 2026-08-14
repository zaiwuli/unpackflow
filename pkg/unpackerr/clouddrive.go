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
	monitor := &clouddrive.Monitor{
		Client: client,
		Config: clouddrive.MonitorConfig{
			ReconnectMin: cfg.ReconnectMin.Duration,
			ReconnectMax: cfg.ReconnectMax.Duration,
		},
		PathOverrides: cfg.PathOverrides,
		OnChange: func(change clouddrive.Change, paths []string) error {
			if !cloudDrivePathMatch(change.Path, cfg.WatchPath) && !cloudDrivePathMatch(change.NewPath, cfg.WatchPath) {
				return nil
			}
			if change.Type == 1 { // delete
				return nil
			}
			// CloudDrive2 can publish the remote event before its mounted
			// filesystem exposes the file. Refresh the affected remote directory
			// and wait briefly for the mapped path before starting the copy.
			go u.handleCloudDriveChange(client, change, paths)
			return nil
		},
		OnStatus: func(status clouddrive.Status) {
			if status.LastError != "" {
				u.Errorf("CloudDrive2 监控：%s", status.LastError)
			}
			if status.MessagesReceived > 0 {
				u.Debugf("CloudDrive2 实时推送：已收到 %d 条消息，文件变化 %d 条，最后类型 %d", status.MessagesReceived, status.ChangesReceived, status.LastMessageType)
			}
		},
	}
	u.cd2Mu.Lock()
	u.cd2Client = monitor.Client
	u.cd2Mu.Unlock()
	go monitor.Run(context.Background())
	u.Printf("CloudDrive2 监控已连接：%s", cfg.URL)
	go u.cloudDriveFallbackScan(monitor.Client, cfg.WatchPath, cfg.PathOverrides)
	go u.cloudDriveRetryLoop()
	if cfg.RefreshInterval.Duration > 0 {
		go u.cloudDriveRefreshLoop(monitor.Client, cfg.RefreshInterval.Duration, cfg.RefreshPath, cfg.WatchPath, cfg.PathOverrides)
	}
}

func (u *Unpackerr) handleCloudDriveChange(client *clouddrive.Client, change clouddrive.Change, paths []string) {
	remotePath := change.Path
	if change.NewPath != "" {
		remotePath = change.NewPath
	}
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
			submitted := u.cacheCloudDrivePaths(paths)
			if submitted > 0 {
				u.Printf("CloudDrive2 实时事件已提交：发现 %d 个复制任务", submitted)
			}
			return
		}
		u.Debugf("CloudDrive2 实时事件已收到，但挂载文件尚未出现，等待第 %d 次重试", index+1)
	}
	u.Errorf("CloudDrive2 实时事件对应的挂载文件未出现：%s", remotePath)
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

func (u *Unpackerr) cloudDriveRefreshLoop(client *clouddrive.Client, interval time.Duration, refreshPath, watchPath string, overrides []string) {
	if refreshPath == "" {
		refreshPath = "/"
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if err := client.ForceRefresh(context.Background(), refreshPath); err != nil {
			u.Errorf("CloudDrive2 定时刷新失败：%s", err)
		} else {
			u.Debugf("CloudDrive2 定时刷新完成")
		}
		u.cloudDriveFallbackScan(client, watchPath, overrides)
	}
}

// cloudDriveFallbackScan compensates for delayed or missed change events. It
// scans only the configured watch path after mapping it to the mounted path.
func (u *Unpackerr) cloudDriveFallbackScan(client *clouddrive.Client, watchPath string, overrides []string) int {
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
		u.resumeCD2Pending()
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
