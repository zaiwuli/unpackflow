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
	monitor := &clouddrive.Monitor{
		Client: &clouddrive.Client{BaseURL: cfg.URL, Token: cfg.Token},
		Config: clouddrive.MonitorConfig{
			ReconnectMin: cfg.ReconnectMin.Duration,
			ReconnectMax: cfg.ReconnectMax.Duration,
		},
		PathOverrides: cfg.PathOverrides,
		OnChange: func(change clouddrive.Change, paths []string) error {
			if !cloudDrivePathMatch(change.Path, cfg.WatchPath) && !cloudDrivePathMatch(change.NewPath, cfg.WatchPath) {
				return nil
			}
			u.cacheCloudDrivePaths(paths)
			return nil
		},
		OnStatus: func(status clouddrive.Status) {
			if status.LastError != "" {
				u.Errorf("CloudDrive2 监控：%s", status.LastError)
			}
		},
	}
	go monitor.Run(context.Background())
	u.Printf("CloudDrive2 监控已连接：%s", cfg.URL)
	go u.cloudDriveFallbackScan(monitor.Client, cfg.WatchPath, cfg.PathOverrides)
	go u.cloudDriveRetryLoop()
	if cfg.RefreshInterval.Duration > 0 {
		go u.cloudDriveRefreshLoop(monitor.Client, cfg.RefreshInterval.Duration, cfg.RefreshPath, cfg.WatchPath, cfg.PathOverrides)
	}
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
func (u *Unpackerr) cloudDriveFallbackScan(client *clouddrive.Client, watchPath string, overrides []string) {
	// A user-provided direct mapping is the most reliable source inside a
	// container. Use it without requiring CD2's mount-point API to succeed.
	roots := clouddrive.MapCloudPathWithOverrides(watchPath, nil, overrides)
	if len(roots) == 0 {
		mounts, err := client.GetMountPoints(context.Background())
		if err != nil {
			u.Errorf("CloudDrive2 补偿扫描读取挂载点失败：%v；请检查路径映射", err)
			return
		}
		roots = clouddrive.MapCloudPathWithOverrides(watchPath, mounts, overrides)
	}
	if len(roots) == 0 {
		u.Errorf("CloudDrive2 补偿扫描无法映射监控路径：%s", watchPath)
		return
	}
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
			u.Debugf("CloudDrive2 补偿扫描：在 %s 发现 %d 个压缩文件", root, len(paths))
			u.cacheCloudDrivePaths(paths)
		} else {
			u.Debugf("CloudDrive2 补偿扫描：%s 暂无压缩文件", root)
		}
	}
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
