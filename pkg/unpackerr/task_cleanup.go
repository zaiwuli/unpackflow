package unpackerr

import (
	"fmt"
	"os"
	"path/filepath"
)

func (u *Unpackerr) queueLocalSourceDelete(name string, files []string) {
	version, err := sourceVersion("local", name)
	if err != nil {
		version = ProcessedSource{Key: name, Source: "local", Path: name}
	}
	version.Stage, version.CleanupKind = "cleanup", "delete"
	if u.CloudDrive2.CacheDir != "" && dashboardPathPrefix(name, u.CloudDrive2.CacheDir) {
		version.Key = "cache-cleanup|" + version.Key
	}
	version.Files = append([]string(nil), files...)
	if _, loaded := u.cleanupRunning.LoadOrStore(version.Key, struct{}{}); loaded {
		return
	}
	u.delChan <- &fileDeleteReq{Paths: files, Version: &version}
}

func (u *Unpackerr) finishLocalDelete(version ProcessedSource, files []string) {
	defer u.cleanupRunning.Delete(version.Key)
	remaining := make([]string, 0, len(files))
	for _, path := range files {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			remaining = append(remaining, path)
		}
	}
	if len(remaining) > 0 {
		version.Files, version.Error = remaining, "原包删除失败，请检查目录权限"
		u.markFailed(version)
		return
	}
	u.clearFailedHistory(version.Key)
}

func (u *Unpackerr) retrySourceDelete(item ProcessedSource) error {
	if item.CleanupKind == "cd2_delete" {
		if !u.CloudDrive2.DeleteSource {
			return fmt.Errorf("当前配置已关闭 CD2 原包删除")
		}
	} else {
		if u.folders == nil {
			return fmt.Errorf("目录监控尚未启动")
		}
		for _, path := range item.Files {
			configured := false
			for _, config := range u.folders.Config {
				if config.DeleteOrig && path != config.Path && dashboardPathPrefix(path, config.Path) && !config.isExcludedPath(path) {
					configured = true
					break
				}
			}
			if !configured {
				return fmt.Errorf("待清理文件不在当前允许删除的目录中：%s", path)
			}
		}
	}
	if len(item.Files) == 0 {
		return fmt.Errorf("记录缺少待清理文件列表")
	}
	if _, loaded := u.cleanupRunning.LoadOrStore(item.Key, struct{}{}); loaded {
		return fmt.Errorf("原包清理已在处理中")
	}
	if item.CleanupKind == "cd2_delete" {
		go func() {
			defer u.cleanupRunning.Delete(item.Key)
			u.deleteCD2Sources(item)
		}()
	} else {
		u.delChan <- &fileDeleteReq{Paths: append([]string(nil), item.Files...), Version: &item}
	}
	return nil
}

func (u *Unpackerr) deleteCD2Sources(version ProcessedSource) {
	var remaining []string
	for _, source := range version.Files {
		if err := removeCloudDriveSource(source); err != nil {
			remaining = append(remaining, source)
			version.Error = err.Error()
			u.Errorf("CloudDrive2 原包删除失败 %s: %v", source, err)
		} else {
			u.Printf("CloudDrive2 原包已删除：%s", source)
		}
	}
	if len(remaining) > 0 {
		version.Files = remaining
		version.Stage, version.CleanupKind = "cleanup", "cd2_delete"
		u.markFailed(version)
		return
	}
	u.clearFailedHistory(version.Key)
}

func (u *Unpackerr) cd2CleanupVersion(cachePath string, files []string) ProcessedSource {
	if u.state != nil {
		u.state.mu.RLock()
		defer u.state.mu.RUnlock()
		for _, item := range u.state.Processed {
			if item.Source == "cd2" && filepath.Base(item.Path) == filepath.Base(cachePath) {
				item.Files = append([]string(nil), files...)
				return item
			}
		}
	}
	version, _ := sourceGroupVersion("cd2", files)
	if version.Key == "" {
		version = ProcessedSource{Key: "cleanup|" + cachePath, Source: "cd2", Path: cachePath, Files: append([]string(nil), files...)}
	}
	return version
}
