package unpackerr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProcessingState is the small, persistent source-of-truth used to prevent a
// successfully processed archive from being extracted again after a restart.
// It deliberately remains a JSON file instead of introducing a database.
type ProcessingState struct {
	Path        string                     `json:"-"`
	Processed   map[string]ProcessedSource `json:"processed"`
	Pending     map[string]PendingCD2      `json:"pending_cd2"`
	Fallback115 map[string]Pending115      `json:"pending_115_fallback"`
	mu          sync.RWMutex               `json:"-"`
}

type ProcessedSource struct {
	Key         string    `json:"key"`
	Source      string    `json:"source"`
	Path        string    `json:"path"`
	Files       []string  `json:"files,omitempty"`
	Size        int64     `json:"size"`
	ModifiedNS  int64     `json:"modified_ns"`
	CachedAt    time.Time `json:"cached_at,omitempty"`
	CompletedAt time.Time `json:"completed_at"`
}

type PendingCD2 struct {
	Key           string          `json:"key"`
	Files         []string        `json:"files"`
	CachedPrimary string          `json:"cached_primary,omitempty"`
	Attempts      int             `json:"attempts"`
	NextAttempt   time.Time       `json:"next_attempt"`
	LastError     string          `json:"last_error,omitempty"`
	Version       ProcessedSource `json:"version,omitempty"`
	N115Fallback  string          `json:"115_fallback_key,omitempty"`
	N115SourceCID string          `json:"115_source_cid,omitempty"`
	N115FID       string          `json:"115_fid,omitempty"`
	N115FileName  string          `json:"115_file_name,omitempty"`
	N115TaskKey   string          `json:"115_task_key,omitempty"`
	N115Size      int64           `json:"115_size,omitempty"`
	N115MTime     int64           `json:"115_mtime,omitempty"`
}

// Pending115 tracks a cloud file moved to a CD2 fallback folder before its
// local extraction has succeeded. It is kept separately because the cache may
// not be visible until after a restart.
type Pending115 struct {
	Key         string    `json:"key"`
	TaskKey     string    `json:"task_key,omitempty"`
	SourceCID   string    `json:"source_cid"`
	FallbackCID string    `json:"fallback_cid,omitempty"`
	CD2Path     string    `json:"cd2_path,omitempty"`
	FID         string    `json:"fid"`
	FileName    string    `json:"file_name"`
	Kind        string    `json:"kind,omitempty"`
	Approval    bool      `json:"approval,omitempty"`
	Size        int64     `json:"size,omitempty"`
	MTime       int64     `json:"mtime,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (u *Unpackerr) loadProcessingState() error {
	base := filepath.Dir(u.ConfigFile)
	if base == "." || base == "" {
		base, _ = os.Getwd()
	}
	state := &ProcessingState{
		Path:        filepath.Join(base, "unpackflow-state.json"),
		Processed:   make(map[string]ProcessedSource),
		Pending:     make(map[string]PendingCD2),
		Fallback115: make(map[string]Pending115),
	}
	data, err := os.ReadFile(state.Path)
	if err == nil {
		if err := json.Unmarshal(data, state); err != nil {
			// Preserve a damaged file for diagnosis and start with an empty state.
			_ = os.Rename(state.Path, state.Path+".corrupt-"+time.Now().Format("20060102-150405"))
			state.Processed = make(map[string]ProcessedSource)
			state.Pending = make(map[string]PendingCD2)
			state.Fallback115 = make(map[string]Pending115)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("processing state: %w", err)
	}
	if state.Processed == nil {
		state.Processed = make(map[string]ProcessedSource)
	}
	if state.Pending == nil {
		state.Pending = make(map[string]PendingCD2)
	}
	if state.Fallback115 == nil {
		state.Fallback115 = make(map[string]Pending115)
	}
	state.Processed = compactProcessedSources(state.Processed)
	state.Path = filepath.Join(base, "unpackflow-state.json")
	u.state = state
	return nil
}

func compactProcessedSources(processed map[string]ProcessedSource) map[string]ProcessedSource {
	compacted := make(map[string]ProcessedSource, len(processed))
	identities := make(map[string]string, len(processed))
	for key, item := range processed {
		identity := processedIdentity(item)
		if previousKey, exists := identities[identity]; exists {
			if !item.CompletedAt.After(compacted[previousKey].CompletedAt) {
				continue
			}
			delete(compacted, previousKey)
		}
		compacted[key] = item
		identities[identity] = key
	}
	return compacted
}

// processedIdentity is intentionally cheap: same archive file name and same
// total byte size means the archive has already been handled. It avoids a
// second full read for hashing and avoids CloudDrive metadata-only mtime
// changes retriggering extraction. The source is not included so a copy of an
// archive arriving through CD2 and later through a local folder is still only
// processed once.
func processedIdentity(item ProcessedSource) string {
	return strings.ToLower(filepath.Base(filepath.Clean(item.Path))) + "|" + fmt.Sprintf("%d", item.Size)
}

func (u *Unpackerr) saveProcessingState() error {
	if u.state == nil {
		return nil
	}
	u.state.mu.RLock()
	data, err := json.MarshalIndent(struct {
		Processed   map[string]ProcessedSource `json:"processed"`
		Pending     map[string]PendingCD2      `json:"pending_cd2"`
		Fallback115 map[string]Pending115      `json:"pending_115_fallback"`
	}{u.state.Processed, u.state.Pending, u.state.Fallback115}, "", "  ")
	path := u.state.Path
	u.state.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// CD2 events can persist state concurrently. A shared .tmp path lets one
	// writer rename the file while another writer is still preparing it.
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".unpackflow-state-*.tmp")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	defer os.Remove(tmp)
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(append(data, '\n')); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sourceVersion(source, sourcePath string) (ProcessedSource, error) {
	clean := filepath.Clean(sourcePath)
	stat, err := os.Stat(clean)
	if err != nil {
		return ProcessedSource{}, err
	}
	key := fmt.Sprintf("%s|%d", strings.ToLower(filepath.Base(clean)), stat.Size())
	return ProcessedSource{Key: key, Source: source, Path: clean, Size: stat.Size(), ModifiedNS: stat.ModTime().UnixNano()}, nil
}

func sourceGroupVersion(source string, files []string) (ProcessedSource, error) {
	files = append([]string(nil), files...)
	sort.Strings(files)
	var size, modified int64
	for _, file := range files {
		stat, err := os.Stat(file)
		if err != nil {
			return ProcessedSource{}, err
		}
		size += stat.Size()
		if stat.ModTime().UnixNano() > modified {
			modified = stat.ModTime().UnixNano()
		}
	}
	primary := archivePrimary(files)
	return ProcessedSource{
		Key: fmt.Sprintf("%s|%d", strings.ToLower(filepath.Base(primary)), size), Source: source,
		Path: filepath.Clean(primary), Files: files, Size: size, ModifiedNS: modified,
	}, nil
}

func (u *Unpackerr) wasProcessed(version ProcessedSource) bool {
	if u.state == nil || version.Key == "" {
		return false
	}
	u.state.mu.RLock()
	_, ok := u.state.Processed[version.Key]
	if !ok {
		// A successful history entry owns this archive name + total size until
		// the user deletes it from the UI. Keep the source path and mtime only
		// for history display and diagnosis.
		for _, processed := range u.state.Processed {
			if processedIdentity(processed) == processedIdentity(version) {
				ok = true
				break
			}
		}
	}
	u.state.mu.RUnlock()
	return ok
}

func (u *Unpackerr) markProcessed(version ProcessedSource) {
	if u.state == nil || version.Key == "" {
		return
	}
	version.CompletedAt = time.Now()
	u.state.mu.Lock()
	for key, processed := range u.state.Processed {
		if processedIdentity(processed) == processedIdentity(version) {
			delete(u.state.Processed, key)
		}
	}
	u.state.Processed[version.Key] = version
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("保存处理记录失败: %v", err)
		return
	}
	u.Printf("历史记录已保存：%s", version.Path)
}

func (u *Unpackerr) processedHistory() []ProcessedSource {
	if u.state == nil {
		return nil
	}
	u.state.mu.RLock()
	items := make([]ProcessedSource, 0, len(u.state.Processed))
	for _, item := range u.state.Processed {
		items = append(items, item)
	}
	u.state.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].CompletedAt.After(items[j].CompletedAt) })
	return items
}

func (u *Unpackerr) deleteProcessed(key string) (ProcessedSource, bool) {
	if u.state == nil {
		return ProcessedSource{}, false
	}
	u.state.mu.Lock()
	item, ok := u.state.Processed[key]
	if ok {
		for processedKey, processed := range u.state.Processed {
			if processedIdentity(processed) == processedIdentity(item) {
				delete(u.state.Processed, processedKey)
			}
		}
	}
	u.state.mu.Unlock()
	if ok {
		if err := u.saveProcessingState(); err != nil {
			u.Errorf("删除处理记录失败: %v", err)
		}
	}
	return item, ok
}

func (u *Unpackerr) restoreProcessed(item ProcessedSource) {
	if u.state == nil || item.Key == "" {
		return
	}
	u.state.mu.Lock()
	u.state.Processed[item.Key] = item
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("恢复处理记录失败: %v", err)
	}
}

func (u *Unpackerr) savePendingCD2(pending PendingCD2) {
	if u.state == nil || pending.Key == "" {
		return
	}
	u.state.mu.Lock()
	u.state.Pending[pending.Key] = pending
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("保存 CD2 待处理任务失败: %v", err)
	}
}

func (u *Unpackerr) removePendingCD2(key string) {
	if u.state == nil {
		return
	}
	u.state.mu.Lock()
	delete(u.state.Pending, key)
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("清理 CD2 待处理任务失败: %v", err)
	}
}

func (u *Unpackerr) savePending115Fallback(items ...Pending115) {
	if u.state == nil {
		return
	}
	u.state.mu.Lock()
	for _, item := range items {
		if item.Key == "" || item.FID == "" || item.SourceCID == "" {
			continue
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now()
		}
		u.state.Fallback115[item.Key] = item
	}
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("保存 115 本地下载任务失败: %v", err)
	}
}

func (u *Unpackerr) pending115Fallback(key string) (Pending115, bool) {
	if u.state == nil || key == "" {
		return Pending115{}, false
	}
	u.state.mu.RLock()
	item, ok := u.state.Fallback115[key]
	u.state.mu.RUnlock()
	return item, ok
}

func (u *Unpackerr) removePending115Fallback(key string) {
	if u.state == nil || key == "" {
		return
	}
	u.state.mu.Lock()
	delete(u.state.Fallback115, key)
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("清理 115 本地下载任务失败: %v", err)
	}
}

func (u *Unpackerr) removePending115Task(taskKey string) {
	if u.state == nil || taskKey == "" {
		return
	}
	u.state.mu.Lock()
	for key, item := range u.state.Fallback115 {
		if item.TaskKey == taskKey || item.Key == taskKey {
			delete(u.state.Fallback115, key)
		}
	}
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("清理无效的 115 本地下载任务失败: %v", err)
	}
}

func (u *Unpackerr) removePending115FallbackForFile(sourceCID, fid string) {
	if u.state == nil || sourceCID == "" || fid == "" {
		return
	}
	u.state.mu.Lock()
	for key, item := range u.state.Fallback115 {
		if item.SourceCID == sourceCID && item.FID == fid {
			delete(u.state.Fallback115, key)
		}
	}
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("清理 115 本地下载任务失败: %v", err)
	}
}

func (u *Unpackerr) approvePending115Task(taskKey string) {
	if u.state == nil || taskKey == "" {
		return
	}
	u.state.mu.Lock()
	for key, item := range u.state.Fallback115 {
		if item.TaskKey == taskKey {
			item.Approval = false
			u.state.Fallback115[key] = item
		}
	}
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("保存 115 下载批准状态失败: %v", err)
	}
}

func (u *Unpackerr) pendingCD2() []PendingCD2 {
	if u.state == nil {
		return nil
	}
	u.state.mu.RLock()
	items := make([]PendingCD2, 0, len(u.state.Pending))
	for _, item := range u.state.Pending {
		item.Files = append([]string(nil), item.Files...)
		items = append(items, item)
	}
	u.state.mu.RUnlock()
	return items
}

func (u *Unpackerr) pendingCD2ForPath(path string) (PendingCD2, bool) {
	if u.state == nil {
		return PendingCD2{}, false
	}
	path = filepath.Clean(path)
	u.state.mu.RLock()
	defer u.state.mu.RUnlock()
	for _, item := range u.state.Pending {
		if filepath.Clean(item.CachedPrimary) == path {
			return item, true
		}
	}
	return PendingCD2{}, false
}

func (u *Unpackerr) hasPendingCD2Files(files []string) bool {
	_, ok := u.pendingCD2ForFiles(files)
	return ok
}

func (u *Unpackerr) pendingCD2ForFiles(files []string) (PendingCD2, bool) {
	if u.state == nil {
		return PendingCD2{}, false
	}
	wanted := make(map[string]struct{}, len(files))
	for _, file := range files {
		wanted[filepath.Clean(file)] = struct{}{}
	}
	u.state.mu.RLock()
	defer u.state.mu.RUnlock()
	for _, pending := range u.state.Pending {
		if len(pending.Files) != len(wanted) {
			continue
		}
		matched := true
		for _, file := range pending.Files {
			if _, ok := wanted[filepath.Clean(file)]; !ok {
				matched = false
				break
			}
		}
		if matched {
			pending.Files = append([]string(nil), pending.Files...)
			return pending, true
		}
	}
	return PendingCD2{}, false
}
