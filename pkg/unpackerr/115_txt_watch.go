package unpackerr

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/julienschmidt/httprouter"
)

const offlineTXTFolderName = "离线磁链"

func (u *Unpackerr) offlineTXTFolder() string {
	folder := u.localFolder()
	if folder == nil || strings.TrimSpace(folder.Path) == "" { return "" }
	// Keep TXT link intake beside the archive watch directory, never inside it.
	// This prevents the archive watcher and the TXT watcher from sharing inputs.
	return filepath.Join(filepath.Dir(filepath.Clean(folder.Path)), offlineTXTFolderName)
}

func (u *Unpackerr) n115OfflineTXTScanAPI(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	root := u.offlineTXTFolder()
	if root == "" { http.Error(w, "尚未配置本地监控文件夹", http.StatusBadRequest); return }
	if err := createOfflineTXTDirectories(root); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
	u.scan115OfflineTXTFolder(root)
	u.writeJSON(w, map[string]any{"success": true, "message": "TXT 磁链目录扫描完成"})
}

func (u *Unpackerr) start115OfflineTXTMonitor() {
	root := u.offlineTXTFolder()
	if root == "" { return }
	if folder := u.localFolder(); folder != nil {
		legacy := filepath.Join(folder.Path, offlineTXTFolderName)
		if filepath.Clean(legacy) != filepath.Clean(root) {
			if err := migrateOfflineTXTDirectory(legacy, root); err != nil {
				u.Errorf("迁移旧离线磁链目录失败：%v", err)
				return
			}
		}
	}
	if err := createOfflineTXTDirectories(root); err != nil {
		u.Errorf("创建离线磁链目录失败：%v", err)
		return
	}
	go u.scan115OfflineTXTFolder(root)
	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil { u.Errorf("启动离线磁链监听失败：%v", err); return }
		defer watcher.Close()
		if err := watcher.Add(root); err != nil { u.Errorf("监听离线磁链目录失败：%v", err); return }
		u.Printf("离线磁链监听已启用：%s", root)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok { return }
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) != 0 && isOfflineTXTFile(event.Name) {
					go u.process115OfflineTXTWhenStable(event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok { return }
				u.Errorf("离线磁链监听异常：%v", err)
			}
		}
	}()
	if interval := u.CloudDrive2.N115TXTScanInterval.Duration; interval > 0 {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				if !u.taskSystemPaused.Load() { u.scan115OfflineTXTFolder(root) }
			}
		}()
	}
}

func migrateOfflineTXTDirectory(oldRoot, newRoot string) error {
	if _, err := os.Stat(oldRoot); os.IsNotExist(err) { return nil } else if err != nil { return err }
	if _, err := os.Stat(newRoot); os.IsNotExist(err) {
		if err := os.Rename(oldRoot, newRoot); err == nil { return nil }
	}
	if err := createOfflineTXTDirectories(newRoot); err != nil { return err }
	return moveOfflineTXTEntries(oldRoot, newRoot)
}

func moveOfflineTXTEntries(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil { return err }
	for _, entry := range entries {
		from := filepath.Join(source, entry.Name())
		to := filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(to, 0o755); err != nil { return err }
			if err := moveOfflineTXTEntries(from, to); err != nil { return err }
			continue
		}
		if _, err := os.Stat(to); err == nil {
			ext := filepath.Ext(to)
			to = strings.TrimSuffix(to, ext) + "-" + time.Now().Format("20060102-150405.000") + ext
		}
		if err := os.Rename(from, to); err != nil { return err }
	}
	return os.Remove(source)
}

func createOfflineTXTDirectories(root string) error {
	for _, name := range []string{"", "已处理", "部分失败", "无效文件"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil { return err }
	}
	return nil
}

func isOfflineTXTFile(path string) bool {
	base := filepath.Base(path)
	return strings.EqualFold(filepath.Ext(base), ".txt") && !strings.HasPrefix(base, ".") && !strings.HasPrefix(base, "~")
}

func (u *Unpackerr) scan115OfflineTXTFolder(root string) {
	if !u.offlineTXTRunning.CompareAndSwap(false, true) { return }
	defer u.offlineTXTRunning.Store(false)
	entries, err := os.ReadDir(root)
	if err != nil { u.Errorf("扫描离线磁链目录失败：%v", err); return }
	for _, entry := range entries {
		if entry.Type().IsRegular() && isOfflineTXTFile(entry.Name()) {
			u.process115OfflineTXT(filepath.Join(root, entry.Name()))
		}
	}
}

func (u *Unpackerr) process115OfflineTXTWhenStable(path string) {
	time.Sleep(2 * time.Second)
	first, err := os.Stat(path)
	if err != nil || !first.Mode().IsRegular() { return }
	time.Sleep(time.Second)
	second, err := os.Stat(path)
	if err != nil || first.Size() != second.Size() { return }
	u.process115OfflineTXT(path)
}

func (u *Unpackerr) process115OfflineTXT(path string) {
	if _, loaded := u.offlineTXTFiles.LoadOrStore(path, struct{}{}); loaded { return }
	defer u.offlineTXTFiles.Delete(path)
	if u.taskSystemPaused.Load() || !u.CloudDrive2.N115Enabled || strings.TrimSpace(u.CloudDrive2.N115Cookie) == "" { return }
	content, err := os.ReadFile(path)
	if err != nil { u.finish115OfflineTXT(path, "无效文件", fmt.Sprintf("读取失败：%v", err)); return }
	parsed := parseOfflineLinks(string(content))
	links := make([]string, 0, len(parsed))
	for _, link := range parsed {
		lower := strings.ToLower(link)
		if strings.HasPrefix(lower, "magnet:?") || strings.HasPrefix(lower, "ed2k://") { links = append(links, link) }
	}
	if len(links) == 0 { u.finish115OfflineTXT(path, "无效文件", "未识别到磁链或 ED2K"); return }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := u.import115OfflineLinks(ctx, links, true)
	if err != nil { u.finish115OfflineTXT(path, "部分失败", err.Error()); return }
	destination := "已处理"
	if result.Failed > 0 { destination = "部分失败" }
	u.finish115OfflineTXT(path, destination, fmt.Sprintf("识别 %d，提交 %d，重复 %d，失败 %d", result.Recognized, result.Submitted, result.Duplicates, result.Failed))
}

func (u *Unpackerr) finish115OfflineTXT(source, folder, detail string) {
	root := filepath.Dir(source)
	target := filepath.Join(root, folder, filepath.Base(source))
	if _, err := os.Stat(target); err == nil {
		ext := filepath.Ext(target)
		target = strings.TrimSuffix(target, ext) + "-" + time.Now().Format("20060102-150405.000") + ext
	}
	if err := os.Rename(source, target); err != nil {
		u.Errorf("离线 TXT 归档失败：%s：%v", source, err)
		return
	}
	u.Systemf("离线 TXT %s：%s：%s", folder, filepath.Base(source), detail)
	icon, title := "✅", "TXT 离线导入完成"
	if folder != "已处理" { icon, title = "⚠️", "TXT 离线导入需要处理" }
	u.notifyEvent(notifyOffline, icon, title, "115离线", filepath.Base(source)+"："+detail)
}
