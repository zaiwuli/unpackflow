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
	Path      string                     `json:"-"`
	Processed map[string]ProcessedSource `json:"processed"`
	Pending   map[string]PendingCD2      `json:"pending_cd2"`
	mu        sync.RWMutex               `json:"-"`
}

type ProcessedSource struct {
	Key         string    `json:"key"`
	Source      string    `json:"source"`
	Path        string    `json:"path"`
	Files       []string  `json:"files,omitempty"`
	Size        int64     `json:"size"`
	ModifiedNS  int64     `json:"modified_ns"`
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
}

func (u *Unpackerr) loadProcessingState() error {
	base := filepath.Dir(u.ConfigFile)
	if base == "." || base == "" {
		base, _ = os.Getwd()
	}
	state := &ProcessingState{
		Path:      filepath.Join(base, "unpackflow-state.json"),
		Processed: make(map[string]ProcessedSource),
		Pending:   make(map[string]PendingCD2),
	}
	data, err := os.ReadFile(state.Path)
	if err == nil {
		if err := json.Unmarshal(data, state); err != nil {
			// Preserve a damaged file for diagnosis and start with an empty state.
			_ = os.Rename(state.Path, state.Path+".corrupt-"+time.Now().Format("20060102-150405"))
			state.Processed = make(map[string]ProcessedSource)
			state.Pending = make(map[string]PendingCD2)
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
	state.Path = filepath.Join(base, "unpackflow-state.json")
	u.state = state
	return nil
}

func (u *Unpackerr) saveProcessingState() error {
	if u.state == nil {
		return nil
	}
	u.state.mu.RLock()
	data, err := json.MarshalIndent(struct {
		Processed map[string]ProcessedSource `json:"processed"`
		Pending   map[string]PendingCD2      `json:"pending_cd2"`
	}{u.state.Processed, u.state.Pending}, "", "  ")
	path := u.state.Path
	u.state.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
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
	key := fmt.Sprintf("%s|%s|%d|%d", source, clean, stat.Size(), stat.ModTime().UnixNano())
	return ProcessedSource{Key: key, Source: source, Path: clean, Size: stat.Size(), ModifiedNS: stat.ModTime().UnixNano()}, nil
}

func sourceGroupVersion(source string, files []string) (ProcessedSource, error) {
	files = append([]string(nil), files...)
	sort.Strings(files)
	var size, modified int64
	parts := make([]string, 0, len(files))
	for _, file := range files {
		stat, err := os.Stat(file)
		if err != nil {
			return ProcessedSource{}, err
		}
		size += stat.Size()
		if stat.ModTime().UnixNano() > modified {
			modified = stat.ModTime().UnixNano()
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", filepath.Clean(file), stat.Size(), stat.ModTime().UnixNano()))
	}
	primary := archivePrimary(files)
	return ProcessedSource{
		Key: fmt.Sprintf("%s|%s", source, strings.Join(parts, "|")), Source: source,
		Path: filepath.Clean(primary), Files: files, Size: size, ModifiedNS: modified,
	}, nil
}

func (u *Unpackerr) wasProcessed(version ProcessedSource) bool {
	if u.state == nil || version.Key == "" {
		return false
	}
	u.state.mu.RLock()
	_, ok := u.state.Processed[version.Key]
	u.state.mu.RUnlock()
	return ok
}

func (u *Unpackerr) markProcessed(version ProcessedSource) {
	if u.state == nil || version.Key == "" {
		return
	}
	version.CompletedAt = time.Now()
	u.state.mu.Lock()
	u.state.Processed[version.Key] = version
	u.state.mu.Unlock()
	if err := u.saveProcessingState(); err != nil {
		u.Errorf("保存处理记录失败: %v", err)
	}
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
	delete(u.state.Processed, key)
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
	if u.state == nil {
		return false
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
			return true
		}
	}
	return false
}
