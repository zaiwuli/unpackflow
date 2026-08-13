package unpackerr

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/Unpackerr/unpackerr/pkg/clouddrive"
)

func (u *Unpackerr) startCloudDriveMonitor() {
	cfg := u.CloudDrive2
	if !cfg.Enabled {
		return
	}
	if cfg.URL == "" || cfg.Token == "" {
		u.Errorf("CloudDrive2 direct mode requires URL and token")
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
				u.Errorf("CloudDrive2 monitor: %s", status.LastError)
			}
		},
	}
	go monitor.Run(context.Background())
	u.Printf("CloudDrive2 监控已连接：%s", cfg.URL)
	if cfg.RefreshInterval.Duration > 0 {
		go u.cloudDriveRefreshLoop(monitor.Client, cfg.RefreshInterval.Duration, cfg.RefreshPath)
	}
}

func cloudDrivePathMatch(value, root string) bool {
	root = path.Clean("/" + strings.TrimLeft(strings.TrimSpace(root), "/"))
	value = path.Clean("/" + strings.TrimLeft(strings.TrimSpace(value), "/"))
	return root == "/" || value == root || strings.HasPrefix(value, root+"/")
}

func (u *Unpackerr) cloudDriveRefreshLoop(client *clouddrive.Client, interval time.Duration, refreshPath string) {
	if refreshPath == "" {
		refreshPath = "/"
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if err := client.ForceRefresh(context.Background(), refreshPath); err != nil {
			u.Errorf("CloudDrive2 定时刷新失败：%s", err)
		} else {
			u.Debugf("CloudDrive2 force refresh completed")
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
