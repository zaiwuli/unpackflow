package unpackerr

import (
	"context"
	"fmt"
	"path/filepath"
)

// All UI mutations of extraction maps run on the owner loop.
func (u *Unpackerr) handleTaskMutation(key, action string) error {
	resolved := ProcessedSource{Path: key}
	if u.state != nil {
		u.state.mu.RLock()
		resolved = u.resolveIgnoredTaskLocked(key)
		u.state.mu.RUnlock()
	}
	if action == "unignore" {
		if err := u.setIgnoredPath(key, false); err != nil {
			return err
		}
		u.cancelled.Delete(key)
		u.cancelled.Delete(resolved.Path)
		u.cd2Tasks.Range(func(taskKey, value any) bool {
			if task, ok := value.(*CD2Transfer); ok && task != nil && filepath.Base(task.Path) == filepath.Base(resolved.Path) {
				u.cancelled.Delete(taskKey)
				u.cancelled.Delete(task.CachedPath)
			}
			return true
		})
		return nil
	}
	aliases := u.dashboardTaskAliases()
	canonical := dashboardCanonicalTaskKey(key, aliases)
	keys := map[string]bool{key: true}
	u.cd2Tasks.Range(func(taskKey, value any) bool {
		if task, ok := value.(*CD2Transfer); ok && task != nil && dashboardCanonicalTaskKey(task.Key, aliases) == canonical {
			if task.Version.Key != "" {
				resolved = task.Version
			}
			keys[task.Key] = true
			if task.CachedPath != "" {
				keys[task.CachedPath] = true
			}
		}
		return true
	})
	for path := range u.Map {
		if dashboardCanonicalTaskKey(path, aliases) == canonical {
			keys[path] = true
		}
	}
	for path := range keys {
		if item := u.Map[path]; item != nil && (item.Status == EXTRACTED || item.Status == DELETING) {
			return fmt.Errorf("任务已解压，不能取消原包清理阶段")
		}
	}
	for path := range keys {
		if pending, ok := u.pendingCD2ForPath(path); ok && pending.Version.Key != "" {
			resolved = pending.Version
			resolved.CachedPath = pending.CachedPrimary
		}
	}
	if action == "ignore" {
		if err := u.setIgnoredPath(key, true); err != nil {
			return err
		}
	}
	for path := range keys {
		u.cancelled.Store(path, struct{}{})
		if cancel, ok := u.cd2Cancel.Load(path); ok {
			cancel.(context.CancelFunc)()
		}
		if item := u.Map[path]; item != nil && item.Status != EXTRACTING && item.Status != QUEUED {
			delete(u.Map, path)
		}
		if u.folders != nil {
			if folder := u.folders.Folders[path]; folder != nil && folder.status != EXTRACTING && folder.status != QUEUED {
				delete(u.folders.Folders, path)
				u.folders.Remove(path)
			}
		}
		if value, ok := u.cd2Tasks.Load(path); ok {
			if task, ok := value.(*CD2Transfer); ok && task != nil {
				state := "已取消"
				_, copying := u.cd2Copy.Load(path)
				_, cloud := u.n115Running.Load(path)
				if copying || cloud {
					state = "正在取消"
				}
				u.updateCD2Transfer(path, task.Path, state, nil)
			}
		}
	}
	if action == "cancel" {
		if version, err := sourceVersion("local", resolved.Path); err == nil && resolved.Key == "" {
			resolved = version
		}
		if pending, ok := u.pendingCD2ForPath(key); ok && pending.Version.Key != "" {
			resolved = pending.Version
			resolved.CachedPath = pending.CachedPrimary
		}
		if resolved.Key == "" {
			resolved.Key = key
		}
		resolved.Stage, resolved.Error = "cancelled", "用户取消"
		u.markFailed(resolved)
	}
	u.removePending115Task(key)
	return nil
}

func (u *Unpackerr) taskCancelled(key string) bool {
	_, cancelled := u.cancelled.Load(key)
	return cancelled
}
