package unpackerr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (u *Unpackerr) refreshRetry115File(item *ProcessedSource) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	files, err := u.n115ListFiles(ctx, item.SourceCID)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.FID == item.CloudFile.FID && file.PickCode != "" {
			item.CloudFile = &file
			return nil
		}
	}
	return fmt.Errorf("原目录中未找到可重试的云端文件")
}

func (u *Unpackerr) retryPending115Cloud(key string) error {
	if u.taskSystemPaused.Load() {
		return fmt.Errorf("请先恢复任务系统")
	}
	if !u.CloudDrive2.N115Enabled || strings.TrimSpace(u.CloudDrive2.N115Cookie) == "" {
		return fmt.Errorf("请先启用并配置 115")
	}
	var pending Pending115
	found := false
	if u.state != nil {
		u.state.mu.RLock()
		for _, item := range u.state.Fallback115 {
			if item.TaskKey == key || item.Key == key {
				pending, found = item, true
				break
			}
		}
		u.state.mu.RUnlock()
	}
	if !found || pending.Kind != "cloud_failure" || !pending.Approval {
		return fmt.Errorf("未找到可重试的云解压任务")
	}
	if strings.TrimSpace(pending.FallbackCID) == "" || strings.TrimSpace(pending.SourceCID) == "" {
		return fmt.Errorf("任务缺少来源或失败归档目录信息")
	}
	if strings.TrimSpace(pending.FallbackCID) != strings.TrimSpace(u.CloudDrive2.N115FailureCID) {
		return fmt.Errorf("失败归档目录已从当前配置移除")
	}
	if u.isIgnoredPath(pending.FileName) || u.taskCancelled(pending.TaskKey) {
		return fmt.Errorf("任务已忽略或取消")
	}
	if _, loaded := u.n115Running.LoadOrStore(pending.TaskKey, struct{}{}); loaded {
		return fmt.Errorf("任务已在处理中")
	}
	u.update115Transfer(pending.TaskKey, pending.FileName, "正在准备重新云解压", func(task *CD2Transfer) {
		task.CanFallback = false
		task.CanCloudRetry = false
		task.Error = ""
	})
	go func() {
		defer u.n115Running.Delete(pending.TaskKey)
		u.n115Queue <- struct{}{}
		defer func() { <-u.n115Queue }()
		if u.taskSystemPaused.Load() || u.isIgnoredPath(pending.FileName) || u.taskCancelled(pending.TaskKey) {
			u.update115Transfer(pending.TaskKey, pending.FileName, "重试已取消", nil)
			return
		}
		item := ProcessedSource{Key: pending.TaskKey, Source: "115", Path: pending.FileName, Size: pending.Size, ModifiedNS: pending.MTime,
			SourceCID: pending.FallbackCID, CloudFile: &n115File{FID: pending.FID, Name: pending.FileName, Size: pending.Size}}
		if err := u.refreshRetry115File(&item); err != nil {
			u.update115Transfer(pending.TaskKey, pending.FileName, "云解压重试准备失败", func(task *CD2Transfer) {
				task.Error = err.Error()
				task.CanFallback = true
				task.CanCloudRetry = true
			})
			return
		}
		u.removePending115Task(pending.TaskKey)
		mapping := N115Mapping{SourceCID: pending.SourceCID, FallbackCID: pending.FallbackCID, CD2Path: normalizeCloudDrivePath(u.CloudDrive2.N115FailureCD2Path),
			RouteID: pending.RouteID, RouteLabel: pending.RouteLabel, Kind: "cloud_failure"}
		u.run115CloudExtract(mapping, *item.CloudFile, item)
	}()
	return nil
}

func (u *Unpackerr) hideHistory(key string) error {
	if u.state == nil {
		return fmt.Errorf("历史记录不存在")
	}
	u.state.mu.Lock()
	found := false
	for _, records := range []map[string]ProcessedSource{u.state.Processed, u.state.Failed, u.state.Ignored} {
		if item, ok := records[key]; ok {
			item.Hidden = true
			records[key] = item
			found = true
		}
	}
	u.state.mu.Unlock()
	if !found {
		return fmt.Errorf("历史记录不存在")
	}
	return u.saveProcessingState()
}

// Called only by the main loop, which owns the extraction and folder maps.
func (u *Unpackerr) historyRetryActive(item ProcessedSource) bool {
	if _, running := u.cleanupRunning.Load(item.Key); running {
		return true
	}
	if _, running := u.n115Running.Load(item.Key); running {
		return true
	}
	for _, path := range []string{item.Path, item.CachedPath} {
		if path == "" {
			continue
		}
		if _, running := u.cd2Copy.Load(cloudDriveTaskKey(path)); running {
			return true
		}
		if tracked := u.Map[path]; tracked != nil {
			if tracked.Status <= EXTRACTING || tracked.Status == DELETING {
				return true
			}
		}
		if u.folders != nil {
			if folder := u.folders.Folders[path]; folder != nil {
				if folder.status <= EXTRACTING || (folder.status == EXTRACTFAILED && (u.MaxRetries == 0 || folder.retries < u.MaxRetries)) {
					return true
				}
			}
		}
	}
	return false
}

func (u *Unpackerr) retryHistory(key string) error {
	if u.taskSystemPaused.Load() {
		return fmt.Errorf("请先恢复任务系统")
	}
	if u.state == nil {
		return fmt.Errorf("历史记录不存在")
	}
	u.state.mu.RLock()
	item, failed := u.state.Failed[key]
	if !failed {
		item = u.state.Processed[key]
	}
	u.state.mu.RUnlock()
	if item.Key == "" {
		return fmt.Errorf("历史记录不存在")
	}
	if u.isIgnoredPath(item.Path) {
		return fmt.Errorf("请先取消忽略")
	}
	if u.historyRetryActive(item) || u.hasPending115Task(item.Key) {
		return fmt.Errorf("任务已在处理中，请勿重复提交")
	}
	if item.Download != nil {
		pending := *item.Download
		path, configured := u.current115PendingPath(pending)
		if !configured || path == "" {
			return fmt.Errorf("任务目录已从当前配置移除")
		}
		pending.CD2Path, pending.Approval = path, true
		u.cancelled.Delete(item.Key)
		u.savePending115Fallback(pending)
		u.update115Transfer(item.Key, item.Path, "等待批准本地下载", func(task *CD2Transfer) { task.CanFallback = true })
		return nil
	}
	if item.Source == "115" || item.SourceCID != "" {
		return u.retry115History(item)
	}
	if item.Stage == "cleanup" {
		if item.CleanupKind == "delete" || item.CleanupKind == "cd2_delete" {
			return u.retrySourceDelete(item)
		}
		return u.retryLocalCleanup(item)
	}
	if item.Source != "local" && item.Source != "cd2" {
		return fmt.Errorf("旧记录缺少重试所需的来源信息，请重新同步")
	}
	if u.folders == nil {
		return fmt.Errorf("目录监控尚未启动")
	}
	path := item.Path
	if item.CachedPath != "" && completeRetryCache(item) {
		path = item.CachedPath
	} else if item.Source == "cd2" {
		if u.downloadsPaused.Load() {
			return fmt.Errorf("请先恢复下载")
		}
		files := append([]string(nil), item.Files...)
		if len(files) == 0 {
			files = []string{item.Path}
		}
		for _, file := range files {
			if _, err := os.Stat(file); err != nil {
				return fmt.Errorf("缓存不可用，源文件不存在：%s", file)
			}
		}
		for _, pending := range u.pendingCD2() {
			if pending.Version.Key == item.Key || pending.CachedPrimary == item.CachedPath && item.CachedPath != "" {
				u.removePendingCD2(pending.Key)
			}
		}
		u.cancelled.Delete(cloudDriveTaskKey(archivePrimary(files)))
		u.deleteProcessed(item.Key)
		if u.cacheCloudDrivePathsForRetry(files, item.Key) == 0 {
			if !failed {
				u.restoreProcessed(item)
			}
			return fmt.Errorf("任务未提交，请检查文件完整性与当前目录配置")
		}
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("源文件不存在，无法重试：%s", path)
	}
	var config *FolderConfig
	for _, candidate := range u.folders.Config {
		if path != candidate.Path && dashboardPathPrefix(path, candidate.Path) && !candidate.isExcludedPath(path) {
			config = candidate
			break
		}
	}
	if config == nil {
		return fmt.Errorf("任务路径不在当前监控配置中")
	}
	u.deleteProcessed(item.Key)
	u.cancelled.Delete(path)
	u.cancelled.Delete(item.Key)
	if transferKey, _, linked := u.cd2TransferForCachedPath(path); linked {
		u.cancelled.Delete(transferKey)
	}
	delete(u.Map, path)
	// Queue directly on the owner loop, avoiding a send to its own event channel.
	u.folders.Folders[path] = &Folder{config: config, status: WAITING, updated: time.Now(), created: time.Now()}
	if path == item.CachedPath {
		u.cd2Cache.Store(filepath.Clean(path), append([]string(nil), item.Files...))
		u.cd2Resume.Store(filepath.Clean(path), struct{}{})
		u.updateCD2TransferForCachedPath(path, "等待本地解压")
		if _, exists := u.pendingCD2ForPath(path); !exists {
			u.savePendingCD2(PendingCD2{Key: filepath.Clean(path), Files: append([]string(nil), item.Files...), CachedPrimary: path, Version: item})
		}
	}
	return nil
}

func completeRetryCache(item ProcessedSource) bool {
	if item.CachedPath == "" || item.Size <= 0 {
		return false
	}
	files := item.Files
	if len(files) == 0 {
		files = []string{item.CachedPath}
	}
	var total int64
	for _, source := range files {
		cached := filepath.Join(filepath.Dir(item.CachedPath), filepath.Base(source))
		info, err := os.Stat(cached)
		if err != nil || info.IsDir() {
			return false
		}
		total += info.Size()
	}
	return total == item.Size
}

func (u *Unpackerr) retryLocalCleanup(item ProcessedSource) error {
	if u.folders == nil {
		return fmt.Errorf("目录监控尚未启动")
	}
	folder := u.folders.Folders[item.Path]
	var config *FolderConfig
	for _, candidate := range u.folders.Config {
		if dashboardPathPrefix(item.Path, candidate.Path) && !candidate.isExcludedPath(item.Path) {
			config = candidate
			break
		}
	}
	if config == nil || config.ArchivePath == "" {
		return fmt.Errorf("原包归档配置已移除，请检查当前目录配置")
	}
	if folder == nil {
		if len(item.Files) == 0 {
			return fmt.Errorf("旧记录缺少待清理文件列表，已阻止重新解压")
		}
		folder = &Folder{config: config, status: DELETEFAILED, cleanupFiles: append([]string(nil), item.Files...)}
	}
	folder.config = config
	u.deleteAfterReached(item.Path, time.Now(), folder)
	if folder.status == DELETEFAILED {
		return fmt.Errorf("原包清理仍失败，请检查目录权限和日志")
	}
	u.clearFailedHistory(item.Key)
	return nil
}

func (u *Unpackerr) clearFailedHistory(key string) {
	if u.state == nil {
		return
	}
	u.state.mu.Lock()
	delete(u.state.Failed, key)
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("保存任务状态失败：%v", err)
	}
}

func (u *Unpackerr) retry115History(item ProcessedSource) error {
	if !u.CloudDrive2.N115Enabled || strings.TrimSpace(u.CloudDrive2.N115Cookie) == "" {
		return fmt.Errorf("请先启用并配置 115")
	}
	if item.CloudFile == nil || item.SourceCID == "" {
		parts := strings.Split(item.Key, "|")
		if len(parts) != 4 || parts[0] != "115" {
			return fmt.Errorf("旧记录缺少云端文件信息，请重新同步")
		}
		item.SourceCID = parts[1]
		item.CloudFile = &n115File{FID: parts[2], Name: item.Path, Size: item.Size}
	}
	configured := false
	for _, cid := range n115SourceCIDs(u.CloudDrive2) {
		configured = configured || cid == item.SourceCID
	}
	for _, mapping := range parse115DownloadMappings(u.CloudDrive2.N115DownloadMappings) {
		configured = configured || mapping.CID == item.SourceCID
	}
	if item.Stage == "cleanup" {
		configured = configured || strings.TrimSpace(u.CloudDrive2.N115FailureCID) == item.SourceCID
	}
	if !configured {
		return fmt.Errorf("原来源目录已从当前配置移除")
	}
	if _, loaded := u.n115Running.LoadOrStore(item.Key, struct{}{}); loaded {
		return fmt.Errorf("任务已在处理中")
	}
	u.cancelled.Delete(item.Key)
	u.update115Transfer(item.Key, item.Path, "等待重试", nil)
	go func() {
		defer u.n115Running.Delete(item.Key)
		u.n115Queue <- struct{}{}
		defer func() { <-u.n115Queue }()
		if u.taskSystemPaused.Load() || u.isIgnoredPath(item.Path) || u.taskCancelled(item.Key) {
			u.update115Transfer(item.Key, item.Path, "重试已取消", nil)
			return
		}
		mapping := n115FailureMapping(u.CloudDrive2, item.SourceCID)
		if item.Stage != "cleanup" && item.Stage != "move" && item.CloudFile.PickCode == "" {
			if err := u.refreshRetry115File(&item); err != nil {
				item.Error = err.Error()
				u.markFailed(item)
				u.update115Transfer(item.Key, item.Path, "云解压失败", func(task *CD2Transfer) { task.Error = err.Error() })
				return
			}
		}
		switch item.Stage {
		case "cleanup":
			u.finish115Source(item, *item.CloudFile)
		case "move":
			u.move115FailedSource(mapping, *item.CloudFile, item)
		default:
			u.run115CloudExtract(mapping, *item.CloudFile, item)
		}
	}()
	return nil
}
