package unpackerr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type taskControlAction struct {
	Action string
	result chan taskControlResult
}

type taskControlResult struct {
	Cleared int
	Error   error
}

func (u *Unpackerr) handleTaskControlAction(action taskControlAction) taskControlResult {
	switch action.Action {
	case "stop_clear":
		if err := u.setTaskSystemPaused(true); err != nil {
			return taskControlResult{Error: err}
		}
		return taskControlResult{Cleared: u.clearWaitingTasks()}
	case "resume":
		if err := u.setTaskSystemPaused(false); err != nil {
			return taskControlResult{Error: err}
		}
		go u.resumeTaskDiscovery()
		return taskControlResult{}
	case "clear_cache":
		cleared, err := u.clearAllCache()
		return taskControlResult{Cleared: cleared, Error: err}
	case "clear_history":
		if err := u.clearAllHistory(); err != nil {
			return taskControlResult{Error: err}
		}
		u.Finished = 0
		for index := range u.Items {
			u.Items[index] = ""
		}
		return taskControlResult{}
	default:
		return taskControlResult{Error: fmt.Errorf("不支持的任务控制操作")}
	}
}

func (u *Unpackerr) clearWaitingTasks() int {
	cleared := 0
	u.cd2Cancel.Range(func(_, value any) bool {
		if cancel, ok := value.(context.CancelFunc); ok && cancel != nil {
			cancel()
		}
		return true
	})
	for name, item := range u.Map {
		if item == nil || item.Status == EXTRACTING {
			continue
		}
		u.cancelled.Store(name, struct{}{})
		delete(u.Map, name)
		if u.folders != nil {
			delete(u.folders.Folders, name)
			u.folders.Remove(name)
		}
		cleared++
	}
	u.cd2Tasks.Range(func(key, value any) bool {
		transfer, _ := value.(*CD2Transfer)
		if transfer != nil && strings.Contains(transfer.State, "云端解压中") {
			return true
		}
		u.cancelled.Store(key, struct{}{})
		u.cd2Tasks.Delete(key)
		u.cd2Copy.Delete(key)
		cleared++
		return true
	})
	if u.state != nil {
		u.state.mu.Lock()
		for key, pending := range u.state.Pending {
			if pending.CachedPrimary != "" {
				if item, ok := u.Map[pending.CachedPrimary]; ok && item != nil && item.Status == EXTRACTING {
					continue
				}
				_ = os.Remove(pending.CachedPrimary)
			}
			delete(u.state.Pending, key)
			cleared++
		}
		cleared += len(u.state.Fallback115)
		u.state.Fallback115 = make(map[string]Pending115)
		u.state.mu.Unlock()
		if err := u.saveProcessingState(); err != nil {
			u.Errorf("清理等待任务状态失败：%v", err)
		}
	}
	_ = os.RemoveAll(cacheStagingRoot(u.CloudDrive2.CacheDir))
	_ = os.MkdirAll(cacheStagingRoot(u.CloudDrive2.CacheDir), 0o755)
	return cleared
}

func (u *Unpackerr) resumeTaskDiscovery() {
	u.scanExistingFolderArchives()
	u.poll115RecentOperations()
	u.cd2Mu.RLock()
	client := u.cd2Client
	u.cd2Mu.RUnlock()
	if client != nil {
		for _, refreshPath := range cloudDriveConfiguredRefreshPaths(u.CloudDrive2) {
			if err := client.ForceRefresh(context.Background(), refreshPath); err != nil {
				u.Errorf("恢复任务后刷新 CD2 失败：%s：%v", refreshPath, err)
			}
		}
		u.cloudDriveFallbackScanPaths(client, cloudDriveManualWatchPaths(u.CloudDrive2), u.CloudDrive2.PathOverrides)
	}
	u.resumeCD2Pending()
	u.Printf("任务系统已恢复，并完成一次补偿同步")
}

func (u *Unpackerr) clearAllCache() (int, error) {
	if !u.taskSystemPaused.Load() {
		return 0, fmt.Errorf("请先停止并清空等待任务")
	}
	for name, item := range u.Map {
		if item != nil && item.Status == EXTRACTING && dashboardPathPrefix(name, u.CloudDrive2.CacheDir) {
			return 0, fmt.Errorf("仍有缓存任务正在解压，请等待完成")
		}
	}
	entries, err := os.ReadDir(u.CloudDrive2.CacheDir)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	cleared := 0
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(u.CloudDrive2.CacheDir, entry.Name())); err != nil {
			return cleared, err
		}
		cleared++
	}
	if err := os.MkdirAll(cacheStagingRoot(u.CloudDrive2.CacheDir), 0o755); err != nil {
		return cleared, err
	}
	u.cd2Cache.Range(func(key, _ any) bool { u.cd2Cache.Delete(key); return true })
	u.cd2Resume.Range(func(key, _ any) bool { u.cd2Resume.Delete(key); return true })
	return cleared, nil
}

func (u *Unpackerr) clearAllHistory() error {
	if u.state == nil {
		return nil
	}
	u.state.mu.Lock()
	u.state.Processed = make(map[string]ProcessedSource)
	u.state.mu.Unlock()
	return u.saveProcessingState()
}
