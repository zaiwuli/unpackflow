package unpackerr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/Unpackerr/unpackerr/pkg/clouddrive"
	"golift.io/cnfg"
)

type UIStore struct {
	Path         string         `json:"-"`
	Passwords    []string       `json:"passwords"`
	Notification UINotification `json:"notification"`
	Overrides    UIOverrides    `json:"settings"`
	mu           sync.RWMutex
}
type UINotification struct {
	Enabled          bool                   `json:"enabled"`
	URL              string                 `json:"url"`
	Provider         string                 `json:"provider,omitempty"`
	APIKey           string                 `json:"api_key,omitempty"`
	Events           *UINotificationEvents  `json:"events,omitempty"`
	Templates        []NotificationTemplate `json:"templates,omitempty"`
	ActiveTemplateID string                 `json:"active_template_id,omitempty"`
}
type NotificationTemplate struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Remark  string `json:"remark,omitempty"`
	Content string `json:"content"`
}

const defaultNotificationTemplateID = "default"
const (
	notificationProviderMP = "mp"
	notificationProviderMS = "ms"
)

func defaultNotificationTemplate() NotificationTemplate {
	return NotificationTemplate{ID: notificationProviderMP, Name: "MP 模板通知", Remark: "GET 文本通知", Content: "{{icon}} UnpackFlow {{title}}\n{{separator}}\n⏱️ 时间: {{time}}\n📦 来源: {{source}}\n📄 任务: {{task}}"}
}

func notificationTemplates() []NotificationTemplate {
	return []NotificationTemplate{
		defaultNotificationTemplate(),
		{ID: notificationProviderMS, Name: "MS 模板通知", Remark: "POST JSON 通知", Content: "{{icon}} {{title}}\n{{separator}}\n⏱️ 时间：{{time}}\n📦 来源：{{source}}\n📄 任务：{{task}}"},
	}
}

type UINotificationEvents struct {
	Discovery bool `json:"discovery"`
	Cache     bool `json:"cache"`
	Extract   bool `json:"extract"`
	Complete  bool `json:"complete"`
	Cleanup   bool `json:"cleanup"`
}

type notificationStage string

const (
	notifyDiscovery notificationStage = "discovery"
	notifyCache     notificationStage = "cache"
	notifyExtract   notificationStage = "extract"
	notifyComplete  notificationStage = "complete"
	notifyCleanup   notificationStage = "cleanup"
)

func defaultNotificationEvents() *UINotificationEvents {
	return &UINotificationEvents{Discovery: true, Cache: true, Extract: true, Complete: true, Cleanup: true}
}

func normalizeNotification(settings UINotification) UINotification {
	if settings.Events == nil {
		settings.Events = defaultNotificationEvents()
	}
	if settings.Provider != notificationProviderMS {
		settings.Provider = notificationProviderMP
	}
	if settings.Provider == notificationProviderMS {
		settings.URL = normalizeMSNotificationURL(settings.URL)
	}
	settings.Templates = notificationTemplates()
	settings.ActiveTemplateID = settings.Provider
	return settings
}

// normalizeMSNotificationURL accepts either the MS service root
// (http://host:8888/) or the full openSend endpoint and stores the latter.
func normalizeMSNotificationURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	const endpoint = "/api/v1/message/openSend"
	cleanPath := strings.TrimRight(parsed.Path, "/")
	if cleanPath == "" || cleanPath == "/" || strings.TrimRight(cleanPath, "/") == endpoint {
		parsed.Path = endpoint
	} else if !strings.HasSuffix(cleanPath, endpoint) {
		parsed.Path = cleanPath + endpoint
	}
	return parsed.String()
}

type UIOverrides struct {
	SchemaVersion       int                `json:"schema_version"`
	Workers             uint               `json:"workers,omitempty"`
	LocalSourceAction   string             `json:"local_source_action,omitempty"`
	LocalArchiveDir     string             `json:"local_archive_dir,omitempty"`
	LocalSourceDelay    string             `json:"local_source_delay,omitempty"`
	FolderInterval      string             `json:"folder_interval,omitempty"`
	CD2Enabled          *bool              `json:"cd2_enabled,omitempty"`
	CD2URL              string             `json:"cd2_url,omitempty"`
	CD2Token            string             `json:"cd2_token,omitempty"`
	RefreshInterval     string             `json:"refresh_interval,omitempty"`
	RefreshPath         string             `json:"refresh_path"`
	WatchPath           string             `json:"watch_path"`
	ManualWatchPaths    []string           `json:"manual_watch_paths"`
	PathOverrides       []string           `json:"path_overrides"`
	CacheDir            string             `json:"cache_dir,omitempty"`
	CacheExtractPath    string             `json:"cache_extract_path,omitempty"`
	KeepCache           *bool              `json:"keep_cache,omitempty"`
	DeleteSource        *bool              `json:"delete_source,omitempty"`
	CacheDeleteDelay    string             `json:"cache_delete_delay,omitempty"`
	CopyTimeout         string             `json:"copy_timeout,omitempty"`
	DownloadsPaused     *bool              `json:"downloads_paused,omitempty"`
	TaskSystemPaused    *bool              `json:"task_system_paused,omitempty"`
	CD2FallbackEnabled  *bool              `json:"cd2_fallback_enabled,omitempty"`
	CD2FallbackInterval string             `json:"cd2_fallback_interval,omitempty"`
	N115Enabled         *bool              `json:"115_enabled,omitempty"`
	N115EventEnabled    *bool              `json:"115_event_enabled,omitempty"`
	N115Cookie          string             `json:"115_cookie,omitempty"`
	N115CookieRemark    string             `json:"115_cookie_remark,omitempty"`
	N115EventInterval   string             `json:"115_event_interval,omitempty"`
	N115Mappings        []string           `json:"115_mappings"`
	N115SourceCIDs      []string           `json:"115_source_cids"`
	N115CIDRemarks      map[string]string  `json:"115_cid_remarks"`
	N115FailureCID      string             `json:"115_failure_cid"`
	N115FailureCD2Path  string             `json:"115_failure_cd2_path"`
	N115Downloads       []string           `json:"115_download_mappings"`
	N115SuccessAction   string             `json:"115_success_action,omitempty"`
	N115ArchiveCID      string             `json:"115_archive_cid"`
	N115AutoFallback    *bool              `json:"115_auto_fallback,omitempty"`
	N115RetryCount      uint               `json:"115_retry_count,omitempty"`
	N115RetryDelay      string             `json:"115_retry_delay,omitempty"`
	N115Sources         []N115SourceRule   `json:"115_sources"`
	N115Failure         *N115FailureRule   `json:"115_failure"`
	N115DownloadRules   []N115DownloadRule `json:"115_download_rules"`
	N115Archive         *N115FolderRule    `json:"115_archive"`
}

type N115SourceRule struct {
	ID         string `json:"id"`
	CID        string `json:"cid"`
	ExtractCID string `json:"extract_cid,omitempty"`
	Remark     string `json:"remark,omitempty"`
}

type N115FolderRule struct {
	CID    string `json:"cid"`
	Remark string `json:"remark,omitempty"`
}

type N115FailureRule struct {
	ID      string `json:"id"`
	CID     string `json:"cid"`
	Remark  string `json:"remark,omitempty"`
	CD2Path string `json:"cd2_path"`
	Mode    string `json:"mode"`
}

type N115DownloadRule struct {
	ID      string `json:"id"`
	CID     string `json:"cid"`
	Remark  string `json:"remark,omitempty"`
	CD2Path string `json:"cd2_path"`
	Mode    string `json:"mode"`
}

func (u *Unpackerr) loadUIStore() error {
	base := filepath.Dir(u.ConfigFile)
	if base == "." || base == "" {
		base, _ = os.Getwd()
	}
	store := &UIStore{Path: filepath.Join(base, "unpackflow-ui.json")}
	data, err := os.ReadFile(store.Path)
	if err == nil {
		if err = json.Unmarshal(data, store); err != nil {
			return fmt.Errorf("ui state: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("ui state: %w", err)
	}
	store.Path = filepath.Join(base, "unpackflow-ui.json")
	modernCloudSettings := store.Overrides.SchemaVersion >= 2 || store.Overrides.N115Sources != nil || store.Overrides.N115DownloadRules != nil || store.Overrides.N115Failure != nil || store.Overrides.N115Archive != nil
	migrateLegacyDataPaths(&store.Overrides)
	normalizeStructured115Settings(&store.Overrides)
	if len(store.Passwords) == 0 && len(u.Passwords) > 0 {
		store.Passwords = append([]string(nil), u.Passwords...)
	}
	store.Notification = normalizeNotification(store.Notification)
	u.Passwords = append([]string(nil), store.Passwords...)
	if store.Overrides.Workers > 0 {
		u.Parallel = store.Overrides.Workers
	}
	u.applyLocalUIOverrides(store.Overrides)
	if store.Overrides.CD2Enabled != nil {
		u.CloudDrive2.Enabled = *store.Overrides.CD2Enabled
	}
	if store.Overrides.CD2URL != "" {
		u.CloudDrive2.URL = store.Overrides.CD2URL
	}
	if store.Overrides.CD2Token != "" {
		u.CloudDrive2.Token = store.Overrides.CD2Token
	}
	if modernCloudSettings {
		u.CloudDrive2.RefreshPath = strings.TrimSpace(store.Overrides.RefreshPath)
		u.CloudDrive2.WatchPath = strings.TrimSpace(store.Overrides.WatchPath)
		u.CloudDrive2.ManualWatchPaths = append([]string(nil), store.Overrides.ManualWatchPaths...)
		u.CloudDrive2.PathOverrides = append([]string(nil), store.Overrides.PathOverrides...)
	} else {
		if store.Overrides.RefreshPath != "" {
			u.CloudDrive2.RefreshPath = store.Overrides.RefreshPath
		}
		if store.Overrides.WatchPath != "" {
			u.CloudDrive2.WatchPath = store.Overrides.WatchPath
		}
		if store.Overrides.ManualWatchPaths != nil {
			u.CloudDrive2.ManualWatchPaths = append([]string(nil), store.Overrides.ManualWatchPaths...)
		} else if store.Overrides.WatchPath != "" {
			// Existing configurations used one CD2 watch path. Keep it once when
			// loading a genuinely old settings file.
			u.CloudDrive2.ManualWatchPaths = []string{store.Overrides.WatchPath}
		}
		if store.Overrides.PathOverrides != nil {
			u.CloudDrive2.PathOverrides = append([]string(nil), store.Overrides.PathOverrides...)
		}
	}
	if store.Overrides.RefreshInterval != "" {
		if d, e := time.ParseDuration(store.Overrides.RefreshInterval); e == nil {
			u.CloudDrive2.RefreshInterval.Duration = d
		}
	}
	if store.Overrides.CacheDir != "" {
		u.CloudDrive2.CacheDir = store.Overrides.CacheDir
	}
	if store.Overrides.CacheExtractPath != "" {
		u.CloudDrive2.CacheExtractPath = store.Overrides.CacheExtractPath
	}
	if store.Overrides.KeepCache != nil {
		u.CloudDrive2.KeepCache = *store.Overrides.KeepCache
	}
	// CD2 cache files no longer delete mounted originals through UI settings.
	u.CloudDrive2.DeleteSource = false
	if store.Overrides.CacheDeleteDelay != "" {
		if d, e := time.ParseDuration(store.Overrides.CacheDeleteDelay); e == nil {
			u.CloudDrive2.CacheDeleteDelay.Duration = d
		}
	}
	if store.Overrides.CopyTimeout != "" {
		if d, e := time.ParseDuration(store.Overrides.CopyTimeout); e == nil {
			u.CloudDrive2.CopyTimeout.Duration = d
		}
	}
	if store.Overrides.DownloadsPaused != nil {
		u.downloadsPaused.Store(*store.Overrides.DownloadsPaused)
	}
	if store.Overrides.TaskSystemPaused != nil {
		u.taskSystemPaused.Store(*store.Overrides.TaskSystemPaused)
	} else if store.Overrides.DownloadsPaused != nil {
		u.taskSystemPaused.Store(*store.Overrides.DownloadsPaused)
	}
	if store.Overrides.CD2FallbackEnabled != nil {
		u.CloudDrive2.FallbackScanEnabled = *store.Overrides.CD2FallbackEnabled
	}
	if store.Overrides.CD2FallbackInterval != "" {
		if d, e := time.ParseDuration(store.Overrides.CD2FallbackInterval); e == nil {
			u.CloudDrive2.FallbackScanInterval.Duration = d
		}
	}
	if store.Overrides.N115Enabled != nil {
		u.CloudDrive2.N115Enabled = *store.Overrides.N115Enabled
	}
	if store.Overrides.N115EventEnabled != nil {
		u.CloudDrive2.N115EventEnabled = *store.Overrides.N115EventEnabled
	}
	if store.Overrides.N115Cookie != "" {
		u.CloudDrive2.N115Cookie = store.Overrides.N115Cookie
	}
	if store.Overrides.N115EventInterval != "" {
		if d, e := time.ParseDuration(store.Overrides.N115EventInterval); e == nil {
			u.CloudDrive2.N115EventInterval.Duration = d
		}
	}
	if modernCloudSettings {
		u.CloudDrive2.N115CookieRemark = strings.TrimSpace(store.Overrides.N115CookieRemark)
	} else if store.Overrides.N115CookieRemark != "" {
		u.CloudDrive2.N115CookieRemark = store.Overrides.N115CookieRemark
	}
	if modernCloudSettings {
		u.CloudDrive2.N115Mappings = append([]string(nil), store.Overrides.N115Mappings...)
		u.CloudDrive2.N115SourceCIDs = clean115CIDs(store.Overrides.N115SourceCIDs)
		u.CloudDrive2.N115FailureCID = strings.TrimSpace(store.Overrides.N115FailureCID)
		u.CloudDrive2.N115FailureCD2Path = strings.TrimSpace(store.Overrides.N115FailureCD2Path)
		u.CloudDrive2.N115DownloadMappings = append([]string(nil), store.Overrides.N115Downloads...)
	} else if store.Overrides.N115Mappings != nil {
		u.CloudDrive2.N115Mappings = append([]string(nil), store.Overrides.N115Mappings...)
		if store.Overrides.N115SourceCIDs != nil {
			u.CloudDrive2.N115SourceCIDs = clean115CIDs(store.Overrides.N115SourceCIDs)
		}
		if store.Overrides.N115SourceCIDs != nil || store.Overrides.N115Downloads != nil {
			u.CloudDrive2.N115FailureCID = strings.TrimSpace(store.Overrides.N115FailureCID)
			u.CloudDrive2.N115FailureCD2Path = strings.TrimSpace(store.Overrides.N115FailureCD2Path)
		}
		if store.Overrides.N115Downloads != nil {
			u.CloudDrive2.N115DownloadMappings = append([]string(nil), store.Overrides.N115Downloads...)
		}
	}
	if !modernCloudSettings {
		migrate115CloudSettings(&u.CloudDrive2)
	}
	if modernCloudSettings {
		u.CloudDrive2.N115SuccessAction = strings.TrimSpace(store.Overrides.N115SuccessAction)
		if u.CloudDrive2.N115SuccessAction == "" {
			u.CloudDrive2.N115SuccessAction = "keep"
		}
		u.CloudDrive2.N115ArchiveCID = strings.TrimSpace(store.Overrides.N115ArchiveCID)
	} else if store.Overrides.N115SuccessAction != "" {
		u.CloudDrive2.N115SuccessAction = store.Overrides.N115SuccessAction
		if store.Overrides.N115ArchiveCID != "" {
			u.CloudDrive2.N115ArchiveCID = store.Overrides.N115ArchiveCID
		}
	}
	if store.Overrides.N115AutoFallback != nil {
		u.CloudDrive2.N115AutoFallback = *store.Overrides.N115AutoFallback
	}
	if store.Overrides.N115RetryCount > 0 {
		u.CloudDrive2.N115RetryCount = store.Overrides.N115RetryCount
	}
	if store.Overrides.N115RetryDelay != "" {
		if d, e := time.ParseDuration(store.Overrides.N115RetryDelay); e == nil && d > 0 {
			u.CloudDrive2.N115RetryDelay.Duration = d
		}
	}
	u.uiStore = store
	if modernCloudSettings && store.Overrides.SchemaVersion < 2 {
		store.Overrides.SchemaVersion = 2
		if err := u.saveUIStore(); err != nil {
			u.Errorf("升级云端设置格式失败：%v", err)
		}
	}
	return nil
}

// migrateLegacyDataPaths keeps existing UI settings usable after changing from
// separate /downloads, /output and /cache mounts to the single /data mount.
func migrateLegacyDataPaths(settings *UIOverrides) {
	dataRoot := strings.TrimSpace(os.Getenv("UN_DATA_DIR"))
	if dataRoot == "" || settings == nil {
		return
	}
	if settings.CacheDir == "/cache" {
		settings.CacheDir = filepath.Join(dataRoot, "缓存目录")
	}
	if settings.CacheExtractPath == "/output" {
		settings.CacheExtractPath = filepath.Join(dataRoot, "解压目录")
	}
	if settings.LocalArchiveDir == "/archive" {
		settings.LocalArchiveDir = filepath.Join(dataRoot, "归档目录")
	}
}
func (u *Unpackerr) saveUIStore() error {
	if u.uiStore == nil {
		return nil
	}
	u.uiStore.mu.RLock()
	data, err := json.MarshalIndent(struct {
		Passwords    []string       `json:"passwords"`
		Notification UINotification `json:"notification"`
		Overrides    UIOverrides    `json:"settings"`
	}{u.uiStore.Passwords, u.uiStore.Notification, u.uiStore.Overrides}, "", "  ")
	path := u.uiStore.Path
	u.uiStore.mu.RUnlock()
	if err != nil {
		return err
	}
	// Passwords, notifications and settings may persist concurrently. A fixed
	// temporary filename can be renamed or removed by the other writer before
	// this writer finishes, causing intermittent save failures on a NAS volume.
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".unpackflow-ui-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时设置文件: %w", err)
	}
	tmp := tmpFile.Name()
	defer os.Remove(tmp)
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("设置临时文件权限: %w", err)
	}
	if _, err := tmpFile.Write(append(data, '\n')); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("写入设置文件: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("同步设置文件: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("关闭设置文件: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换设置文件: %w", err)
	}
	return nil
}
func (u *Unpackerr) uiPasswords() []string {
	if u.uiStore == nil {
		return append([]string(nil), u.Passwords...)
	}
	u.uiStore.mu.RLock()
	defer u.uiStore.mu.RUnlock()
	return append([]string(nil), u.uiStore.Passwords...)
}
func (u *Unpackerr) addUIPassword(password string) error {
	password = strings.TrimSpace(password)
	if password == "" {
		return fmt.Errorf("密码不能为空")
	}
	if u.uiStore == nil {
		return fmt.Errorf("UI 存储未初始化")
	}
	u.uiStore.mu.Lock()
	for _, p := range u.uiStore.Passwords {
		if p == password {
			u.uiStore.mu.Unlock()
			return nil
		}
	}
	u.uiStore.Passwords = append(u.uiStore.Passwords, password)
	u.Passwords = append([]string(nil), u.uiStore.Passwords...)
	u.uiStore.mu.Unlock()
	return u.saveUIStore()
}
func (u *Unpackerr) removeUIPassword(index int) error {
	if u.uiStore == nil {
		return fmt.Errorf("UI 存储未初始化")
	}
	u.uiStore.mu.Lock()
	sorted := sortedPasswords(u.uiStore.Passwords)
	if index < 0 || index >= len(sorted) {
		u.uiStore.mu.Unlock()
		return fmt.Errorf("密码不存在")
	}
	target := sorted[index]
	for originalIndex, password := range u.uiStore.Passwords {
		if password == target {
			u.uiStore.Passwords = append(u.uiStore.Passwords[:originalIndex], u.uiStore.Passwords[originalIndex+1:]...)
			break
		}
	}
	u.Passwords = append([]string(nil), u.uiStore.Passwords...)
	u.uiStore.mu.Unlock()
	return u.saveUIStore()
}
func (u *Unpackerr) notificationSettings() UINotification {
	if u.uiStore == nil {
		return UINotification{}
	}
	u.uiStore.mu.RLock()
	defer u.uiStore.mu.RUnlock()
	return normalizeNotification(u.uiStore.Notification)
}
func (u *Unpackerr) uiSettings() UIOverrides {
	enabled, keepCache := u.CloudDrive2.Enabled, u.CloudDrive2.KeepCache
	settings := UIOverrides{
		SchemaVersion:  2,
		Workers:        u.Parallel,
		FolderInterval: u.Folder.Interval.Duration.String(),
		CD2Enabled:     &enabled,
		CD2URL:         u.CloudDrive2.URL,
		CD2Token: func() string {
			if strings.TrimSpace(u.CloudDrive2.Token) != "" {
				return "********"
			}
			return ""
		}(),
		RefreshInterval:     u.CloudDrive2.RefreshInterval.Duration.String(),
		RefreshPath:         u.CloudDrive2.RefreshPath,
		WatchPath:           u.CloudDrive2.WatchPath,
		ManualWatchPaths:    append([]string(nil), u.CloudDrive2.ManualWatchPaths...),
		PathOverrides:       append([]string{}, u.CloudDrive2.PathOverrides...),
		CacheDir:            u.CloudDrive2.CacheDir,
		CacheExtractPath:    u.CloudDrive2.CacheExtractPath,
		KeepCache:           &keepCache,
		CacheDeleteDelay:    u.CloudDrive2.CacheDeleteDelay.Duration.String(),
		CopyTimeout:         u.CloudDrive2.CopyTimeout.Duration.String(),
		DownloadsPaused:     func() *bool { value := u.downloadsPaused.Load(); return &value }(),
		TaskSystemPaused:    func() *bool { value := u.taskSystemPaused.Load(); return &value }(),
		CD2FallbackEnabled:  func() *bool { v := u.CloudDrive2.FallbackScanEnabled; return &v }(),
		CD2FallbackInterval: u.CloudDrive2.FallbackScanInterval.Duration.String(),
		N115Enabled:         func() *bool { v := u.CloudDrive2.N115Enabled; return &v }(),
		N115EventEnabled:    func() *bool { v := u.CloudDrive2.N115EventEnabled; return &v }(),
		N115CookieRemark:    u.CloudDrive2.N115CookieRemark,
		N115EventInterval:   u.CloudDrive2.N115EventInterval.Duration.String(),
		N115Mappings:        append([]string(nil), u.CloudDrive2.N115Mappings...),
		N115SourceCIDs:      append([]string(nil), u.CloudDrive2.N115SourceCIDs...),
		N115CIDRemarks:      u.n115CIDRemarks(),
		N115FailureCID:      u.CloudDrive2.N115FailureCID,
		N115FailureCD2Path:  u.CloudDrive2.N115FailureCD2Path,
		N115Downloads:       append([]string(nil), u.CloudDrive2.N115DownloadMappings...),
		N115SuccessAction:   u.CloudDrive2.N115SuccessAction,
		N115ArchiveCID:      u.CloudDrive2.N115ArchiveCID,
		N115AutoFallback:    func() *bool { v := u.CloudDrive2.N115AutoFallback; return &v }(),
		N115RetryCount:      u.CloudDrive2.N115RetryCount,
		N115RetryDelay:      u.CloudDrive2.N115RetryDelay.Duration.String(),
	}
	if folder := u.localFolder(); folder != nil {
		settings.LocalSourceAction = localSourceAction(folder)
		settings.LocalArchiveDir = folder.ArchivePath
		if folder.DeleteAfter != nil {
			settings.LocalSourceDelay = folder.DeleteAfter.Duration.String()
		}
	}
	if u.uiStore == nil {
		return settings
	}
	u.uiStore.mu.RLock()
	overrides := u.uiStore.Overrides
	u.uiStore.mu.RUnlock()
	if overrides.Workers > 0 {
		settings.Workers = overrides.Workers
	}
	if overrides.LocalSourceAction != "" {
		settings.LocalSourceAction = overrides.LocalSourceAction
	}
	if overrides.LocalArchiveDir != "" {
		settings.LocalArchiveDir = overrides.LocalArchiveDir
	}
	if overrides.LocalSourceDelay != "" {
		settings.LocalSourceDelay = overrides.LocalSourceDelay
	}
	if overrides.FolderInterval != "" {
		settings.FolderInterval = overrides.FolderInterval
	}
	if overrides.CD2Enabled != nil {
		settings.CD2Enabled = overrides.CD2Enabled
	}
	if overrides.CD2URL != "" {
		settings.CD2URL = overrides.CD2URL
	}
	if overrides.RefreshInterval != "" {
		settings.RefreshInterval = overrides.RefreshInterval
	}
	if overrides.RefreshPath != "" {
		settings.RefreshPath = overrides.RefreshPath
	}
	if overrides.WatchPath != "" {
		settings.WatchPath = overrides.WatchPath
	}
	if overrides.ManualWatchPaths != nil {
		settings.ManualWatchPaths = append([]string(nil), overrides.ManualWatchPaths...)
	}
	if overrides.PathOverrides != nil {
		settings.PathOverrides = append([]string{}, overrides.PathOverrides...)
	}
	if overrides.CacheDir != "" {
		settings.CacheDir = overrides.CacheDir
	}
	if overrides.CacheExtractPath != "" {
		settings.CacheExtractPath = overrides.CacheExtractPath
	}
	if overrides.KeepCache != nil {
		settings.KeepCache = overrides.KeepCache
	}
	if overrides.CacheDeleteDelay != "" {
		settings.CacheDeleteDelay = overrides.CacheDeleteDelay
	}
	if overrides.CopyTimeout != "" {
		settings.CopyTimeout = overrides.CopyTimeout
	}
	if overrides.DownloadsPaused != nil {
		settings.DownloadsPaused = overrides.DownloadsPaused
	}
	if overrides.TaskSystemPaused != nil {
		settings.TaskSystemPaused = overrides.TaskSystemPaused
	}
	if overrides.CD2FallbackEnabled != nil {
		settings.CD2FallbackEnabled = overrides.CD2FallbackEnabled
	}
	if overrides.CD2FallbackInterval != "" {
		settings.CD2FallbackInterval = overrides.CD2FallbackInterval
	}
	if overrides.N115Enabled != nil {
		settings.N115Enabled = overrides.N115Enabled
	}
	if overrides.N115EventEnabled != nil {
		settings.N115EventEnabled = overrides.N115EventEnabled
	}
	if overrides.N115CookieRemark != "" {
		settings.N115CookieRemark = overrides.N115CookieRemark
	}
	if overrides.N115EventInterval != "" {
		settings.N115EventInterval = overrides.N115EventInterval
	}
	if overrides.N115Mappings != nil {
		settings.N115Mappings = append([]string(nil), overrides.N115Mappings...)
	}
	if overrides.N115SourceCIDs != nil {
		settings.N115SourceCIDs = append([]string(nil), overrides.N115SourceCIDs...)
	}
	if overrides.N115CIDRemarks != nil {
		settings.N115CIDRemarks = cloneN115CIDRemarks(overrides.N115CIDRemarks)
	}
	if overrides.N115FailureCID != "" {
		settings.N115FailureCID = overrides.N115FailureCID
	}
	if overrides.N115FailureCD2Path != "" {
		settings.N115FailureCD2Path = overrides.N115FailureCD2Path
	}
	if overrides.N115Downloads != nil {
		settings.N115Downloads = append([]string(nil), overrides.N115Downloads...)
	}
	if overrides.N115SuccessAction != "" {
		settings.N115SuccessAction = overrides.N115SuccessAction
	}
	if overrides.N115ArchiveCID != "" {
		settings.N115ArchiveCID = overrides.N115ArchiveCID
	}
	if overrides.N115AutoFallback != nil {
		settings.N115AutoFallback = overrides.N115AutoFallback
	}
	if overrides.N115RetryCount > 0 {
		settings.N115RetryCount = overrides.N115RetryCount
	}
	if overrides.N115RetryDelay != "" {
		settings.N115RetryDelay = overrides.N115RetryDelay
	}
	if overrides.N115Sources != nil {
		settings.N115Sources = append([]N115SourceRule(nil), overrides.N115Sources...)
	}
	if overrides.N115Failure != nil {
		copy := *overrides.N115Failure
		settings.N115Failure = &copy
	}
	if overrides.N115DownloadRules != nil {
		settings.N115DownloadRules = append([]N115DownloadRule(nil), overrides.N115DownloadRules...)
	}
	if overrides.N115Archive != nil {
		copy := *overrides.N115Archive
		settings.N115Archive = &copy
	}
	if overrides.N115Cookie != "" {
		settings.N115Cookie = "********"
	}
	normalizeStructured115Settings(&settings)
	return settings
}

func (u *Unpackerr) localFolder() *FolderConfig {
	for _, folder := range u.Folders {
		if folder != nil && !folder.ExternalOnly {
			return folder
		}
	}
	return nil
}

func localSourceAction(folder *FolderConfig) string {
	if folder == nil {
		return "keep"
	}
	if strings.TrimSpace(folder.ArchivePath) != "" {
		return "archive"
	}
	if folder.DeleteOrig {
		return "delete"
	}
	return "keep"
}

func (u *Unpackerr) applyLocalUIOverrides(overrides UIOverrides) {
	if overrides.FolderInterval != "" {
		if duration, err := time.ParseDuration(overrides.FolderInterval); err == nil && duration >= 0 {
			u.Folder.Interval.Duration = duration
		}
	}
	folder := u.localFolder()
	if folder == nil {
		return
	}
	if overrides.LocalSourceDelay != "" {
		if duration, err := time.ParseDuration(overrides.LocalSourceDelay); err == nil && duration >= 0 {
			folder.DeleteAfter = &cnfg.Duration{Duration: duration}
		}
	}
	action := strings.ToLower(strings.TrimSpace(overrides.LocalSourceAction))
	switch action {
	case "delete":
		folder.DeleteOrig = true
		folder.ArchivePath = ""
	case "archive":
		folder.DeleteOrig = false
		folder.ArchivePath = strings.TrimSpace(overrides.LocalArchiveDir)
		if folder.ArchivePath == "" {
			folder.ArchivePath = strings.TrimSpace(os.Getenv("UN_LOCAL_ARCHIVE_DIR"))
		}
	case "keep":
		folder.DeleteOrig = false
		folder.ArchivePath = ""
	}
}
func (u *Unpackerr) saveNotification(s UINotification) error {
	if u.uiStore == nil {
		return fmt.Errorf("UI 存储未初始化")
	}
	u.uiStore.mu.RLock()
	current := u.uiStore.Notification
	u.uiStore.mu.RUnlock()
	if s.Templates == nil {
		s.Templates = current.Templates
	}
	if s.Provider == "" {
		s.Provider = current.Provider
	}
	if s.APIKey == "" {
		s.APIKey = current.APIKey
	}
	s = normalizeNotification(s)
	u.uiStore.mu.Lock()
	u.uiStore.Notification = s
	u.uiStore.mu.Unlock()
	return u.saveUIStore()
}
func (u *Unpackerr) saveUIOverrides(s UIOverrides) error {
	u.uiStore.mu.Lock()
	s.SchemaVersion = 2
	// The API intentionally never returns the CD2 token to the browser. An empty
	// token submitted while editing another setting therefore means "keep the
	// existing token", not "erase it".
	if strings.TrimSpace(s.CD2Token) == "" || strings.TrimSpace(s.CD2Token) == "********" {
		s.CD2Token = u.uiStore.Overrides.CD2Token
	}
	if strings.TrimSpace(s.N115Cookie) == "" {
		s.N115Cookie = u.uiStore.Overrides.N115Cookie
	}
	normalizeStructured115Settings(&s)
	s.N115SourceCIDs = clean115CIDs(s.N115SourceCIDs)
	s.N115CIDRemarks = cleanN115CIDRemarks(s.N115CIDRemarks)
	s.N115FailureCID = strings.TrimSpace(s.N115FailureCID)
	s.N115FailureCD2Path = strings.TrimSpace(s.N115FailureCD2Path)
	u.uiStore.Overrides = s
	u.uiStore.mu.Unlock()
	if err := u.saveUIStore(); err != nil {
		return err
	}
	u.applyCloudDriveUIOverrides(s)
	return nil
}

func normalizeStructured115Settings(s *UIOverrides) {
	if s == nil {
		return
	}
	// A nil structured slice means an old configuration that still needs
	// migration. A non-nil empty slice means the user intentionally deleted
	// every rule and must not be repopulated from legacy fields.
	if s.N115Sources == nil {
		for _, cid := range clean115CIDs(s.N115SourceCIDs) {
			s.N115Sources = append(s.N115Sources, N115SourceRule{ID: "source:" + cid, CID: cid, Remark: s.N115CIDRemarks[cid]})
		}
	}
	s.N115SourceCIDs = s.N115SourceCIDs[:0]
	for index := range s.N115Sources {
		rule := &s.N115Sources[index]
		rule.CID = strings.TrimSpace(rule.CID)
		rule.Remark = strings.TrimSpace(rule.Remark)
		if rule.ID == "" {
			rule.ID = "source:" + rule.CID
		}
		if rule.CID != "" {
			s.N115SourceCIDs = append(s.N115SourceCIDs, rule.CID)
		}
	}
	if s.N115Archive == nil && strings.TrimSpace(s.N115ArchiveCID) != "" {
		cid := strings.TrimSpace(s.N115ArchiveCID)
		s.N115Archive = &N115FolderRule{CID: cid, Remark: s.N115CIDRemarks[cid]}
	}
	if s.N115Archive != nil {
		s.N115Archive.CID = strings.TrimSpace(s.N115Archive.CID)
		s.N115Archive.Remark = strings.TrimSpace(s.N115Archive.Remark)
		s.N115ArchiveCID = s.N115Archive.CID
	}
	if s.N115Failure == nil && (s.N115FailureCID != "" || s.N115FailureCD2Path != "") {
		cid := strings.TrimSpace(s.N115FailureCID)
		mode := "approval"
		if s.N115AutoFallback != nil && *s.N115AutoFallback {
			mode = "auto"
		}
		s.N115Failure = &N115FailureRule{ID: "cloud-failure", CID: cid, Remark: s.N115CIDRemarks[cid], CD2Path: normalizeCloudDrivePath(s.N115FailureCD2Path), Mode: mode}
	}
	if s.N115Failure != nil {
		s.N115Failure.CID = strings.TrimSpace(s.N115Failure.CID)
		s.N115Failure.Remark = strings.TrimSpace(s.N115Failure.Remark)
		s.N115Failure.CD2Path = normalizeCloudDrivePath(s.N115Failure.CD2Path)
		if s.N115Failure.ID == "" {
			s.N115Failure.ID = "cloud-failure"
		}
		if s.N115Failure.Mode != "auto" {
			s.N115Failure.Mode = "approval"
		}
		s.N115FailureCID = s.N115Failure.CID
		s.N115FailureCD2Path = s.N115Failure.CD2Path
		auto := s.N115Failure.Mode == "auto"
		s.N115AutoFallback = &auto
	}
	if s.N115DownloadRules == nil {
		for _, mapping := range parse115DownloadMappings(s.N115Downloads) {
			mode := "auto"
			if mapping.Approval {
				mode = "approval"
			}
			s.N115DownloadRules = append(s.N115DownloadRules, N115DownloadRule{ID: "download:" + mapping.CID, CID: mapping.CID, Remark: s.N115CIDRemarks[mapping.CID], CD2Path: mapping.CD2Path, Mode: mode})
		}
	}
	s.N115Downloads = s.N115Downloads[:0]
	for index := range s.N115DownloadRules {
		rule := &s.N115DownloadRules[index]
		rule.CID = strings.TrimSpace(rule.CID)
		rule.Remark = strings.TrimSpace(rule.Remark)
		rule.CD2Path = normalizeCloudDrivePath(rule.CD2Path)
		if rule.ID == "" {
			rule.ID = "download:" + rule.CID
		}
		if rule.Mode != "approval" {
			rule.Mode = "auto"
		}
		if rule.CID != "" && rule.CD2Path != "" {
			s.N115Downloads = append(s.N115Downloads, rule.CID+" => "+rule.CD2Path+" => "+rule.Mode)
		}
	}
	remarks := make(map[string]string)
	for _, rule := range s.N115Sources {
		if rule.CID != "" && rule.Remark != "" {
			remarks[rule.CID] = rule.Remark
		}
	}
	if s.N115Archive != nil && s.N115Archive.CID != "" && s.N115Archive.Remark != "" {
		remarks[s.N115Archive.CID] = s.N115Archive.Remark
	}
	if s.N115Failure != nil && s.N115Failure.CID != "" && s.N115Failure.Remark != "" {
		remarks[s.N115Failure.CID] = s.N115Failure.Remark
	}
	for _, rule := range s.N115DownloadRules {
		if rule.CID != "" && rule.Remark != "" {
			remarks[rule.CID] = rule.Remark
		}
	}
	s.N115CIDRemarks = remarks
}

// applyCloudDriveUIOverrides keeps path-based cloud processing in sync with
// the settings page. Connection endpoint or token changes still take effect
// after restart, but folder mappings and 115 behavior apply immediately.
func (u *Unpackerr) applyCloudDriveUIOverrides(s UIOverrides) {
	if s.RefreshInterval != "" {
		if d, err := time.ParseDuration(s.RefreshInterval); err == nil {
			u.CloudDrive2.RefreshInterval.Duration = d
		}
	}
	u.CloudDrive2.RefreshPath = strings.TrimSpace(s.RefreshPath)
	u.CloudDrive2.WatchPath = strings.TrimSpace(s.WatchPath)
	u.CloudDrive2.ManualWatchPaths = append([]string(nil), s.ManualWatchPaths...)
	u.CloudDrive2.PathOverrides = append([]string(nil), s.PathOverrides...)
	if s.CD2FallbackEnabled != nil {
		u.CloudDrive2.FallbackScanEnabled = *s.CD2FallbackEnabled
	}
	if s.CD2FallbackInterval != "" {
		if d, err := time.ParseDuration(s.CD2FallbackInterval); err == nil {
			u.CloudDrive2.FallbackScanInterval.Duration = d
		}
	}
	if s.N115Enabled != nil {
		u.CloudDrive2.N115Enabled = *s.N115Enabled
	}
	if s.N115EventEnabled != nil {
		u.CloudDrive2.N115EventEnabled = *s.N115EventEnabled
	}
	if s.N115Cookie != "" {
		u.CloudDrive2.N115Cookie = s.N115Cookie
	}
	u.CloudDrive2.N115CookieRemark = s.N115CookieRemark
	if s.N115EventInterval != "" {
		if d, err := time.ParseDuration(s.N115EventInterval); err == nil {
			u.CloudDrive2.N115EventInterval.Duration = d
		}
	}
	u.CloudDrive2.N115Mappings = append([]string(nil), s.N115Mappings...)
	u.CloudDrive2.N115SourceCIDs = clean115CIDs(s.N115SourceCIDs)
	u.CloudDrive2.N115ExtractCIDs = make(map[string]string)
	for _, rule := range s.N115Sources {
		if strings.TrimSpace(rule.CID) != "" && strings.TrimSpace(rule.ExtractCID) != "" {
			u.CloudDrive2.N115ExtractCIDs[strings.TrimSpace(rule.CID)] = strings.TrimSpace(rule.ExtractCID)
		}
	}
	u.CloudDrive2.N115FailureCID = strings.TrimSpace(s.N115FailureCID)
	u.CloudDrive2.N115FailureCD2Path = normalizeCloudDrivePath(s.N115FailureCD2Path)
	u.CloudDrive2.N115DownloadMappings = append([]string(nil), s.N115Downloads...)
	u.CloudDrive2.N115SuccessAction = s.N115SuccessAction
	u.CloudDrive2.N115ArchiveCID = strings.TrimSpace(s.N115ArchiveCID)
	if s.N115AutoFallback != nil {
		u.CloudDrive2.N115AutoFallback = *s.N115AutoFallback
	}
	if s.N115RetryCount > 0 {
		u.CloudDrive2.N115RetryCount = s.N115RetryCount
	}
	if s.N115RetryDelay != "" {
		if d, err := time.ParseDuration(s.N115RetryDelay); err == nil {
			u.CloudDrive2.N115RetryDelay.Duration = d
		}
	}
	u.refreshPending115ConfiguredPaths()
}

func (u *Unpackerr) refreshPending115ConfiguredPaths() {
	if u.state == nil {
		return
	}
	downloads := parse115DownloadMappings(u.CloudDrive2.N115DownloadMappings)
	u.state.mu.Lock()
	changed := false
	for key, pending := range u.state.Fallback115 {
		pathValue := ""
		if pending.Kind == "cloud_failure" {
			pathValue = normalizeCloudDrivePath(u.CloudDrive2.N115FailureCD2Path)
		} else {
			for _, mapping := range downloads {
				if mapping.CID == pending.SourceCID || mapping.CID == pending.FallbackCID {
					pathValue = mapping.CD2Path
					break
				}
			}
		}
		if pathValue == "" {
			delete(u.state.Fallback115, key)
			u.cd2Tasks.Delete(pending.TaskKey)
			changed = true
			continue
		}
		if pending.CD2Path != pathValue {
			delete(u.state.Fallback115, key)
			pending.CD2Path = pathValue
			remoteFile := path.Join(pathValue, pending.FileName)
			mapped := clouddrive.MapCloudPathWithOverrides(remoteFile, nil, u.CloudDrive2.PathOverrides)
			for _, newKey := range n115FallbackKeys(mapped, remoteFile) {
				pending.Key = newKey
				u.state.Fallback115[newKey] = pending
			}
			changed = true
		}
	}
	for key, pending := range u.state.Pending {
		if pending.N115TaskKey == "" {
			continue
		}
		if _, ok := u.configured115LocalPath(pending.N115SourceCID); !ok {
			delete(u.state.Pending, key)
			u.cd2Tasks.Delete(pending.N115TaskKey)
			changed = true
		}
	}
	u.state.mu.Unlock()
	if changed {
		if err := u.saveProcessingState(); err != nil {
			u.Errorf("更新 115 待处理任务路径失败：%v", err)
		}
	}
}

func (u *Unpackerr) configured115LocalPath(cid string) (string, bool) {
	cid = strings.TrimSpace(cid)
	if cid != "" && cid == strings.TrimSpace(u.CloudDrive2.N115FailureCID) {
		value := normalizeCloudDrivePath(u.CloudDrive2.N115FailureCD2Path)
		return value, value != ""
	}
	for _, mapping := range parse115DownloadMappings(u.CloudDrive2.N115DownloadMappings) {
		if mapping.CID == cid {
			return mapping.CD2Path, true
		}
	}
	return "", false
}

func cleanN115CIDRemarks(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for cid, remark := range values {
		cid, remark = strings.TrimSpace(cid), strings.TrimSpace(remark)
		if cid != "" && remark != "" {
			result[cid] = remark
		}
	}
	return result
}

func cloneN115CIDRemarks(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for cid, remark := range values {
		result[cid] = remark
	}
	return result
}

func (u *Unpackerr) n115CIDRemarks() map[string]string {
	if u.uiStore == nil {
		return nil
	}
	u.uiStore.mu.RLock()
	defer u.uiStore.mu.RUnlock()
	return cloneN115CIDRemarks(u.uiStore.Overrides.N115CIDRemarks)
}

func (u *Unpackerr) setDownloadsPaused(paused bool) error {
	u.downloadsPaused.Store(paused)
	if u.uiStore == nil {
		return nil
	}
	u.uiStore.mu.Lock()
	u.uiStore.Overrides.DownloadsPaused = &paused
	u.uiStore.mu.Unlock()
	return u.saveUIStore()
}

func (u *Unpackerr) setTaskSystemPaused(paused bool) error {
	u.taskSystemPaused.Store(paused)
	if u.uiStore == nil {
		return nil
	}
	u.uiStore.mu.Lock()
	u.uiStore.Overrides.TaskSystemPaused = &paused
	u.uiStore.Overrides.DownloadsPaused = &paused
	u.uiStore.mu.Unlock()
	u.downloadsPaused.Store(paused)
	return u.saveUIStore()
}
func (u *Unpackerr) notifyUI(status ExtractStatus, item *Extract) {
	if item == nil {
		return
	}
	icon, title, stage := "⚪", "任务状态", notifyComplete
	switch status {
	case QUEUED:
		icon, title, stage = "📦", "发现压缩包", notifyDiscovery
	case EXTRACTING:
		icon, title, stage = "⏱️", "开始解压", notifyExtract
	case EXTRACTED:
		icon, title, stage = "✅", "解压完成", notifyComplete
	case EXTRACTFAILED:
		icon, title, stage = "❌", "解压失败", notifyComplete
	case DELETED:
		icon, title, stage = "🧹", "任务清理完成", notifyCleanup
	}
	u.notifyEvent(stage, icon, title, sourceName(item.App), item.Path)
}

func (u *Unpackerr) notifyEvent(stage notificationStage, icon, title, source, task string) {
	s := u.notificationSettings()
	if !s.Enabled || s.URL == "" {
		u.Debugf("通知未发送：通知功能未启用或通知地址为空（%s）", title)
		return
	}
	if !notificationStageEnabled(s, stage) {
		u.Debugf("通知未发送：已关闭“%s”阶段通知（%s）", notificationStageName(stage), title)
		return
	}
	// 115, CD2 and the cache watcher may report the same archive using different
	// paths and source labels. Notifications are task-lifecycle events, so the
	// identity intentionally uses the normalized archive name and stage rather
	// than the reporting channel.
	identity := notificationTaskIdentity(task)
	key := string(stage) + "|" + identity
	now := time.Now()
	u.noticeMu.Lock()
	if last, exists := u.noticeRecent[key]; exists && now.Sub(last) < 24*time.Hour {
		u.noticeMu.Unlock()
		return
	}
	if u.noticeRecent == nil {
		u.noticeRecent = make(map[string]time.Time)
	}
	for previous, at := range u.noticeRecent {
		if now.Sub(at) >= 30*time.Second {
			delete(u.noticeRecent, previous)
		}
	}
	u.noticeRecent[key] = now
	u.noticeMu.Unlock()
	if u.state != nil {
		u.state.mu.Lock()
		if u.state.Notifications == nil {
			u.state.Notifications = make(map[string]time.Time)
		}
		if last, exists := u.state.Notifications[key]; exists && now.Sub(last) < 24*time.Hour {
			u.state.mu.Unlock()
			return
		}
		u.state.Notifications[key] = now
		u.state.mu.Unlock()
		if err := u.saveProcessingState(); err != nil {
			u.Debugf("保存通知去重状态失败：%v", err)
		}
	}
	u.sendNotification(s, icon, title, source, task)
}

func notificationTaskIdentity(task string) string {
	clean := filepath.ToSlash(strings.TrimSpace(task))
	clean = strings.TrimSuffix(clean, "/")
	name := path.Base(clean)
	if name == "." || name == "" || name == "/" {
		name = clean
	}
	return strings.ToLower(name)
}

func (u *Unpackerr) sendNotification(s UINotification, icon, title, source, task string) {
	if s.Provider == notificationProviderMS {
		u.sendMSNotification(s, icon, title, source, task)
		return
	}
	message := renderNotificationTemplate(s, icon, title, source, task)
	go func() {
		parsed, err := url.Parse(s.URL)
		if err != nil {
			u.Errorf("通知地址无效：%v", err)
			return
		}
		query := parsed.Query()
		query.Set("text", message)
		parsed.RawQuery = query.Encode()
		client := &http.Client{Timeout: 10 * time.Second}
		var lastError error
		for attempt := 1; attempt <= 3; attempt++ {
			req, requestErr := http.NewRequestWithContext(context.Background(), http.MethodGet, parsed.String(), nil)
			if requestErr != nil {
				u.Errorf("创建通知请求失败：%v", requestErr)
				return
			}
			res, requestErr := client.Do(req)
			if requestErr == nil {
				_ = res.Body.Close()
				if res.StatusCode >= 200 && res.StatusCode < 300 {
					u.Printf("通知已发送：%s（%s）", title, source)
					return
				}
				lastError = fmt.Errorf("HTTP %s", res.Status)
			} else {
				lastError = requestErr
			}
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * time.Second)
			}
		}
		u.Errorf("发送通知失败（已重试 3 次）：%s：%v", title, lastError)
	}()
}

func (u *Unpackerr) sendMSNotification(s UINotification, icon, title, source, task string) {
	s.URL = normalizeMSNotificationURL(s.URL)
	message := renderNotificationTemplate(s, icon, title, source, task)
	go func() {
		payload, err := json.Marshal(struct {
			Title    string `json:"title"`
			Content  string `json:"content"`
			ImageURL string `json:"imageUrl"`
			Proxy    bool   `json:"proxy"`
		}{Title: title, Content: message, ImageURL: "", Proxy: false})
		if err != nil {
			u.Errorf("通知内容生成失败：%v", err)
			return
		}
		client := &http.Client{Timeout: 10 * time.Second}
		var lastError error
		for attempt := 1; attempt <= 3; attempt++ {
			req, requestErr := http.NewRequestWithContext(context.Background(), http.MethodPost, s.URL, bytes.NewReader(payload))
			if requestErr != nil {
				u.Errorf("创建通知请求失败：%v", requestErr)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if s.APIKey != "" {
				req.Header.Set("apiKey", s.APIKey)
			}
			res, requestErr := client.Do(req)
			if requestErr == nil {
				_ = res.Body.Close()
				if res.StatusCode >= 200 && res.StatusCode < 300 {
					u.Printf("MS 通知已发送：%s（%s）", title, source)
					return
				}
				lastError = fmt.Errorf("HTTP %s", res.Status)
			} else {
				lastError = requestErr
			}
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * time.Second)
			}
		}
		u.Errorf("MS 通知发送失败（已重试 3 次）：%s：%v", title, lastError)
	}()
}

func notificationStageEnabled(settings UINotification, stage notificationStage) bool {
	events := normalizeNotification(settings).Events
	switch stage {
	case notifyDiscovery:
		return events.Discovery
	case notifyCache:
		return events.Cache
	case notifyExtract:
		return events.Extract
	case notifyComplete:
		return events.Complete
	case notifyCleanup:
		return events.Cleanup
	default:
		return false
	}
}

func notificationStageName(stage notificationStage) string {
	switch stage {
	case notifyDiscovery:
		return "发现"
	case notifyCache:
		return "缓存"
	case notifyExtract:
		return "解压"
	case notifyComplete:
		return "完成"
	case notifyCleanup:
		return "清理"
	default:
		return "未知"
	}
}
func formatUINotification(status ExtractStatus, item *Extract) string {
	icon, title := "\u26aa", "\u4efb\u52a1\u72b6\u6001"
	switch status {
	case QUEUED:
		icon, title = "\U0001f4e6", "\u53d1\u73b0\u538b\u7f29\u5305"
	case EXTRACTING:
		icon, title = "\u23f1\ufe0f", "\u5f00\u59cb\u89e3\u538b"
	case EXTRACTED:
		icon, title = "\u2705", "\u89e3\u538b\u5b8c\u6210"
	case EXTRACTFAILED:
		icon, title = "\u274c", "\u89e3\u538b\u5931\u8d25"
	case DELETED:
		icon, title = "\U0001f9f9", "\u4efb\u52a1\u6e05\u7406\u5b8c\u6210"
	}
	return formatNotificationMessage(icon, title, sourceName(item.App), item.Path)
}

func formatNotificationMessage(icon, title, source, task string) string {
	return fmt.Sprintf("%s UnpackFlow %s\n--------------------\n⏱️ 时间: %s\n📁 来源: %s\n🆔 任务: %s", icon, title, time.Now().Format("2006-01-02 15:04:05"), source, task)
}
func renderNotificationTemplate(settings UINotification, icon, title, source, task string) string {
	settings = normalizeNotification(settings)
	selected := defaultNotificationTemplate()
	for _, item := range settings.Templates {
		if item.ID == settings.ActiveTemplateID {
			selected = item
			break
		}
	}
	values := map[string]string{"icon": icon, "title": title, "source": source, "task": task, "time": time.Now().Format("2006-01-02 15:04:05"), "separator": "--------------------"}
	funcs := template.FuncMap{}
	for key, value := range values {
		captured := value
		funcs[key] = func() string { return captured }
	}
	tmpl, err := template.New(selected.ID).Funcs(funcs).Option("missingkey=error").Parse(selected.Content)
	if err != nil {
		return formatNotificationMessage(icon, title, source, task)
	}
	var out strings.Builder
	if err = tmpl.Execute(&out, values); err != nil {
		return formatNotificationMessage(icon, title, source, task)
	}
	return out.String()
}

func sortedPasswords(p []string) []string {
	r := append([]string{}, p...)
	sort.Strings(r)
	return r
}
