package unpackerr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type UIStore struct {
	Path         string         `json:"-"`
	Passwords    []string       `json:"passwords"`
	Notification UINotification `json:"notification"`
	Overrides    UIOverrides    `json:"settings"`
	mu           sync.RWMutex
}
type UINotification struct {
	Enabled bool                  `json:"enabled"`
	URL     string                `json:"url"`
	Events  *UINotificationEvents `json:"events,omitempty"`
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
	return settings
}

type UIOverrides struct {
	Workers           uint     `json:"workers,omitempty"`
	LocalSourceAction string   `json:"local_source_action,omitempty"`
	LocalArchiveDir   string   `json:"local_archive_dir,omitempty"`
	FolderInterval    string   `json:"folder_interval,omitempty"`
	CD2Enabled        *bool    `json:"cd2_enabled,omitempty"`
	CD2URL            string   `json:"cd2_url,omitempty"`
	CD2Token          string   `json:"cd2_token,omitempty"`
	RefreshInterval   string   `json:"refresh_interval,omitempty"`
	RefreshPath       string   `json:"refresh_path,omitempty"`
	WatchPath         string   `json:"watch_path,omitempty"`
	PathOverrides     []string `json:"path_overrides,omitempty"`
	CacheDir          string   `json:"cache_dir,omitempty"`
	CacheExtractPath  string   `json:"cache_extract_path,omitempty"`
	KeepCache         *bool    `json:"keep_cache,omitempty"`
	DeleteSource      *bool    `json:"delete_source,omitempty"`
	CacheDeleteDelay  string   `json:"cache_delete_delay,omitempty"`
	CopyTimeout       string   `json:"copy_timeout,omitempty"`
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
	if store.Overrides.RefreshPath != "" {
		u.CloudDrive2.RefreshPath = store.Overrides.RefreshPath
	}
	if store.Overrides.WatchPath != "" {
		u.CloudDrive2.WatchPath = store.Overrides.WatchPath
	}
	if store.Overrides.RefreshInterval != "" {
		if d, e := time.ParseDuration(store.Overrides.RefreshInterval); e == nil {
			u.CloudDrive2.RefreshInterval.Duration = d
		}
	}
	if store.Overrides.PathOverrides != nil {
		u.CloudDrive2.PathOverrides = append([]string(nil), store.Overrides.PathOverrides...)
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
	if store.Overrides.DeleteSource != nil {
		u.CloudDrive2.DeleteSource = *store.Overrides.DeleteSource
	}
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
	u.uiStore = store
	return nil
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
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
	enabled, keepCache, deleteSource := u.CloudDrive2.Enabled, u.CloudDrive2.KeepCache, u.CloudDrive2.DeleteSource
	settings := UIOverrides{
		Workers:          u.Parallel,
		FolderInterval:   u.Folder.Interval.Duration.String(),
		CD2Enabled:       &enabled,
		CD2URL:           u.CloudDrive2.URL,
		RefreshInterval:  u.CloudDrive2.RefreshInterval.Duration.String(),
		RefreshPath:      u.CloudDrive2.RefreshPath,
		WatchPath:        u.CloudDrive2.WatchPath,
		PathOverrides:    append([]string{}, u.CloudDrive2.PathOverrides...),
		CacheDir:         u.CloudDrive2.CacheDir,
		CacheExtractPath: u.CloudDrive2.CacheExtractPath,
		KeepCache:        &keepCache,
		DeleteSource:     &deleteSource,
		CacheDeleteDelay: u.CloudDrive2.CacheDeleteDelay.Duration.String(),
		CopyTimeout:      u.CloudDrive2.CopyTimeout.Duration.String(),
	}
	if folder := u.localFolder(); folder != nil {
		settings.LocalSourceAction = localSourceAction(folder)
		settings.LocalArchiveDir = folder.ArchivePath
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
	if overrides.DeleteSource != nil {
		settings.DeleteSource = overrides.DeleteSource
	}
	if overrides.CacheDeleteDelay != "" {
		settings.CacheDeleteDelay = overrides.CacheDeleteDelay
	}
	if overrides.CopyTimeout != "" {
		settings.CopyTimeout = overrides.CopyTimeout
	}
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
	s = normalizeNotification(s)
	u.uiStore.mu.Lock()
	u.uiStore.Notification = s
	u.uiStore.mu.Unlock()
	return u.saveUIStore()
}
func (u *Unpackerr) saveUIOverrides(s UIOverrides) error {
	u.uiStore.mu.Lock()
	// The API intentionally never returns the CD2 token to the browser. An empty
	// token submitted while editing another setting therefore means "keep the
	// existing token", not "erase it".
	if strings.TrimSpace(s.CD2Token) == "" {
		s.CD2Token = u.uiStore.Overrides.CD2Token
	}
	u.uiStore.Overrides = s
	u.uiStore.mu.Unlock()
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
	u.sendNotification(s, icon, title, source, task)
}

func (u *Unpackerr) sendNotification(s UINotification, icon, title, source, task string) {
	message := formatNotificationMessage(icon, title, source, task)
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
func sortedPasswords(p []string) []string {
	r := append([]string{}, p...)
	sort.Strings(r)
	return r
}
