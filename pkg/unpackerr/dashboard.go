package unpackerr

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
	"golift.io/starr"
)

//go:embed webui/index.html
var dashboardHTML []byte

//go:embed webui/app.css
var dashboardCSS []byte

//go:embed webui/app.js
var dashboardJS []byte

//go:embed webui/icon.svg
var dashboardIcon []byte

type DashboardSnapshot struct {
	UpdatedAt    string              `json:"updated_at"`
	Totals       DashboardTotals     `json:"totals"`
	Tasks        []DashboardTask     `json:"tasks"`
	History      []DashboardHistory  `json:"history"`
	Folders      []DashboardFolder   `json:"folders"`
	CloudDrive   DashboardCloudDrive `json:"clouddrive2"`
	Passwords    []string            `json:"passwords"`
	Notification UINotification      `json:"notification"`
	Settings     UIOverrides         `json:"settings"`
	Logs         []DashboardLog      `json:"logs"`
	Transfers    []CD2Transfer       `json:"transfers"`
	Paused       bool                `json:"paused"`
}

type DashboardLog struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

type DashboardHistory struct {
	Key         string `json:"key"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	CachedAt    string `json:"cached_at,omitempty"`
	CompletedAt string `json:"completed_at"`
}

type historyAction struct {
	Key    string
	Action string
	result chan error
}

type DashboardTotals struct {
	Active   int  `json:"active"`
	Finished uint `json:"finished"`
	Retries  uint `json:"retries"`
	Workers  uint `json:"workers"`
}

type DashboardTask struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	Updated     string `json:"updated"`
	Retries     uint   `json:"retries"`
	Progress    string `json:"progress,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
	Total       int64  `json:"total,omitempty"`
	Speed       int64  `json:"speed,omitempty"`
	ETASeconds  int64  `json:"eta_seconds,omitempty"`
	Error       string `json:"error,omitempty"`
	CanFallback bool   `json:"can_fallback,omitempty"`
}

type DashboardFolder struct {
	Path        string `json:"path"`
	ExtractPath string `json:"extract_path"`
	Tracked     int    `json:"tracked"`
}

type DashboardCloudDrive struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

func (u *Unpackerr) dashboardSnapshot() DashboardSnapshot {
	snapshot := DashboardSnapshot{
		UpdatedAt: time.Now().Format(time.RFC3339),
		Tasks:     []DashboardTask{},
		History:   []DashboardHistory{},
		Folders:   []DashboardFolder{},
		Totals: DashboardTotals{
			Finished: u.Finished,
			Retries:  u.Retries,
			Workers:  u.Parallel,
		},
		CloudDrive:   DashboardCloudDrive{Enabled: u.CloudDrive2.Enabled, URL: u.CloudDrive2.URL},
		Passwords:    sortedPasswords(u.uiPasswords()),
		Notification: u.notificationSettings(),
		Settings:     u.uiSettings(),
		Logs:         u.Logger.dashboardLogs(),
		Transfers:    u.dashboardTransfers(),
		Paused:       u.taskSystemPaused.Load(),
	}
	for name, item := range u.Map {
		source := sourceName(item.App)
		if _, ok := u.cd2Cache.Load(filepath.Clean(name)); ok || dashboardPathPrefix(name, u.CloudDrive2.CacheDir) {
			source = "CloudDrive2"
		}
		task := DashboardTask{
			Key:     name,
			Name:    name,
			Source:  source,
			Status:  statusName(item.Status),
			Updated: item.Updated.Format("2006-01-02 15:04:05"),
			Retries: item.Retries,
		}
		if item.XProg != nil {
			if progress := item.XProg.String(); progress != "no progress yet" {
				task.Progress = progress
			}
		}
		if item.Status == EXTRACTING && task.Progress == "" {
			task.Progress = "正在解压，解压工具暂未返回百分比"
		}
		if _, cancelled := u.cancelled.Load(name); cancelled {
			task.Status = "已取消"
		}
		if item.Status == QUEUED || item.Status == EXTRACTING || item.Status == WAITING {
			snapshot.Totals.Active++
		}
		snapshot.Tasks = append(snapshot.Tasks, task)
	}
	for _, transfer := range snapshot.Transfers {
		status := transfer.State
		if _, cancelled := u.cancelled.Load(transfer.Key); cancelled {
			status = "已取消"
		}
		source := transfer.Source
		if source == "" {
			source = "CloudDrive2"
		}
		snapshot.Tasks = append(snapshot.Tasks, DashboardTask{
			Key:         transfer.Key,
			Name:        filepath.Base(transfer.Path),
			Source:      source,
			Status:      status,
			Updated:     transfer.UpdatedAt.Format("2006-01-02 15:04:05"),
			Bytes:       transfer.Bytes,
			Total:       transfer.Total,
			Speed:       transfer.Speed,
			ETASeconds:  transfer.ETA,
			Error:       transfer.Error,
			CanFallback: transfer.CanFallback,
		})
		if dashboardTaskIsActive(status) {
			snapshot.Totals.Active++
		}
	}
	sort.Slice(snapshot.Tasks, func(i, j int) bool { return snapshot.Tasks[i].Updated > snapshot.Tasks[j].Updated })
	for _, item := range u.processedHistory() {
		source := "本地目录"
		if item.Source == "cd2" {
			source = "CloudDrive2"
		} else if item.Source == "115" {
			source = "115 云端"
		}
		snapshot.History = append(snapshot.History, DashboardHistory{
			Key: item.Key, Path: item.Path, Source: source,
			CachedAt:    formatDashboardTime(item.CachedAt),
			CompletedAt: item.CompletedAt.Format("2006-01-02 15:04:05"),
		})
	}
	for _, folder := range u.Folders {
		tracked := 0
		if u.folders != nil {
			for name := range u.folders.Folders {
				if dashboardPathPrefix(name, folder.Path) {
					tracked++
				}
			}
		}
		snapshot.Folders = append(snapshot.Folders, DashboardFolder{
			Path: folder.Path, ExtractPath: folder.ExtractPath, Tracked: tracked,
		})
	}
	return snapshot
}

func dashboardTaskIsActive(status string) bool {
	return strings.Contains(status, "解压中") || strings.Contains(status, "复制") || strings.Contains(status, "缓存") || strings.Contains(status, "刷新")
}

func formatDashboardTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02 15:04:05")
}

func (u *Unpackerr) dashboardTransfers() []CD2Transfer {
	items := make([]CD2Transfer, 0)
	seen := make(map[string]struct{})
	u.cd2Tasks.Range(func(_, value any) bool {
		if transfer, ok := value.(*CD2Transfer); ok && transfer != nil {
			items = append(items, *transfer)
			seen[transfer.Key] = struct{}{}
		}
		return true
	})
	// Pending cloud/local download tasks survive a restart. Rebuild lightweight
	// rows for every state so copying and waiting tasks are visible immediately.
	if u.state != nil {
		u.state.mu.RLock()
		for _, item := range u.state.Fallback115 {
			if item.TaskKey == "" {
				continue
			}
			if _, exists := seen[item.TaskKey]; exists {
				continue
			}
			state := "等待 CD2 文件可见"
			source := item.RouteLabel
			if source == "" {
				source = "日常本地下载"
			}
			if item.Kind == "cloud_failure" {
				source = item.RouteLabel
				if source == "" {
					source = "云解压失败转本地"
				}
			}
			if item.Approval {
				state = "等待批准本地下载"
				if item.Kind == "cloud_failure" {
					state = "云解压失败，等待批准本地下载"
				}
			}
			items = append(items, CD2Transfer{Key: item.TaskKey, Path: item.FileName, Source: source, State: state, StartedAt: item.CreatedAt, UpdatedAt: item.CreatedAt, CanFallback: item.Approval})
			seen[item.TaskKey] = struct{}{}
		}
		for _, item := range u.state.Pending {
			key := item.Key
			if key == "" {
				key = item.CachedPrimary
			}
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			name := filepath.Base(key)
			if len(item.Files) > 0 {
				name = filepath.Base(item.Files[0])
			}
			state := "等待复制"
			if item.CachedPrimary != "" {
				state = "已缓存，等待本地解压"
			} else if item.LastError != "" {
				state = "复制失败，等待重试"
			}
			items = append(items, CD2Transfer{Key: key, Path: name, Source: "CD2 实时推送", State: state, UpdatedAt: item.NextAttempt, Error: item.LastError})
			seen[key] = struct{}{}
		}
		u.state.mu.RUnlock()
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items
}

func dashboardPathPrefix(value, prefix string) bool {
	value, prefix = filepath.Clean(value), filepath.Clean(prefix)
	return value == prefix || strings.HasPrefix(value, prefix+string(filepath.Separator))
}

func sourceName(app starr.App) string {
	if app == FolderString {
		return "\u672c\u5730\u76ee\u5f55"
	}
	return "\u4e0b\u8f7d\u5668"
}
func statusName(status ExtractStatus) string {
	labels := map[ExtractStatus]string{WAITING: "\u7b49\u5f85\u7a33\u5b9a", QUEUED: "\u6392\u961f\u4e2d", EXTRACTING: "\u6b63\u5728\u89e3\u538b", EXTRACTFAILED: "\u89e3\u538b\u5931\u8d25", EXTRACTED: "\u5df2\u89e3\u538b", IMPORTED: "\u5df2\u5bfc\u5165", DELETING: "\u6b63\u5728\u6e05\u7406", DELETEFAILED: "\u6e05\u7406\u5931\u8d25", DELETED: "\u5df2\u5b8c\u6210", EXTRACTEDNOTHING: "\u65e0\u9700\u89e3\u538b"}
	if label, ok := labels[status]; ok {
		return label
	}
	return "\u672a\u77e5\u72b6\u6001"
}
func (u *Unpackerr) dashboardPage(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	page := bytes.ReplaceAll(dashboardHTML, []byte("/*__CSS__*/"), dashboardCSS)
	page = bytes.ReplaceAll(page, []byte("/*__JS__*/"), dashboardJS)
	_, _ = w.Write(page)
}

func (u *Unpackerr) dashboardIcon(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=\"favicon.svg\"")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(dashboardIcon)
}

// Redirect conventional .ico requests to the real SVG icon. Serving SVG bytes
// as an .ico response prevents several dashboard applications from detecting it.
func (u *Unpackerr) dashboardFavicon(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(dashboardFaviconICO())
}

func dashboardFaviconICO() []byte {
	// ICO files may embed PNG. This produces an actual application/x-icon
	// response rather than relying on consumers accepting an SVG at .ico.
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	blue, white := color.RGBA{23, 105, 224, 255}, color.RGBA{255, 255, 255, 255}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetRGBA(x, y, blue)
		}
	}
	for y := 9; y < 23; y++ {
		for x := 7; x < 25; x++ {
			if x == 7 || x == 24 || y == 9 || y == 22 || (y >= 15 && x >= 12 && x <= 19) {
				img.SetRGBA(x, y, white)
			}
		}
	}
	var pngData bytes.Buffer
	if png.Encode(&pngData, img) != nil {
		return nil
	}
	data := pngData.Bytes()
	ico := make([]byte, 22+len(data))
	binary.LittleEndian.PutUint16(ico[2:4], 1)
	binary.LittleEndian.PutUint16(ico[4:6], 1)
	ico[6], ico[7] = 32, 32
	binary.LittleEndian.PutUint16(ico[10:12], 1)
	binary.LittleEndian.PutUint16(ico[12:14], 32)
	binary.LittleEndian.PutUint32(ico[14:18], uint32(len(data)))
	binary.LittleEndian.PutUint32(ico[18:22], 22)
	copy(ico[22:], data)
	return ico
}

func (u *Unpackerr) dashboardAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	request := make(chan DashboardSnapshot, 1)
	select {
	case u.uiRequests <- request:
	case <-r.Context().Done():
		http.Error(w, "\u8bf7\u6c42\u5df2\u53d6\u6d88", http.StatusRequestTimeout)
		return
	case <-time.After(2 * time.Second):
		http.Error(w, "\u670d\u52a1\u6b63\u5728\u542f\u52a8", http.StatusServiceUnavailable)
		return
	}
	select {
	case snapshot := <-request:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(snapshot)
	case <-r.Context().Done():
		http.Error(w, "\u8bf7\u6c42\u5df2\u53d6\u6d88", http.StatusRequestTimeout)
	case <-time.After(2 * time.Second):
		http.Error(w, "\u72b6\u6001\u8bfb\u53d6\u8d85\u65f6", http.StatusServiceUnavailable)
	}
}
func (u *Unpackerr) passwordAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct {
		Action   string `json:"action"`
		Password string `json:"password"`
		Index    int    `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "\u8bf7\u6c42\u683c\u5f0f\u9519\u8bef", http.StatusBadRequest)
		return
	}
	var err error
	if input.Action == "remove" {
		err = u.removeUIPassword(input.Index)
	} else {
		err = u.addUIPassword(input.Password)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.writeJSON(w, map[string]any{"success": true, "passwords": sortedPasswords(u.uiPasswords())})
}
func (u *Unpackerr) notificationAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var settings UINotification
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, "\u8bf7\u6c42\u683c\u5f0f\u9519\u8bef", http.StatusBadRequest)
		return
	}
	settings.URL = strings.TrimSpace(settings.URL)
	if settings.Provider == notificationProviderMS {
		settings.URL = normalizeMSNotificationURL(settings.URL)
	}
	if settings.Enabled && settings.URL == "" {
		http.Error(w, "\u901a\u77e5\u5730\u5740\u4e0d\u80fd\u4e3a\u7a7a", http.StatusBadRequest)
		return
	}
	if err := u.saveNotification(settings); err != nil {
		http.Error(w, "\u4fdd\u5b58\u901a\u77e5\u8bbe\u7f6e\u5931\u8d25", http.StatusInternalServerError)
		return
	}
	u.writeJSON(w, map[string]any{"success": true, "notification": u.notificationSettings()})
}

func (u *Unpackerr) notificationTemplatesAPI(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var input struct {
		Action  string `json:"action"`
		ID      string `json:"id"`
		Name    string `json:"name"`
		Remark  string `json:"remark"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	settings := u.notificationSettings()
	switch input.Action {
	case "create":
		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Content) == "" {
			http.Error(w, "模板名称和内容不能为空", http.StatusBadRequest)
			return
		}
		input.ID = fmt.Sprintf("template-%d", time.Now().UnixNano())
		settings.Templates = append(settings.Templates, NotificationTemplate{ID: input.ID, Name: strings.TrimSpace(input.Name), Remark: strings.TrimSpace(input.Remark), Content: input.Content})
		settings.ActiveTemplateID = input.ID
	case "update":
		if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Content) == "" {
			http.Error(w, "模板名称和内容不能为空", http.StatusBadRequest)
			return
		}
		updated := false
		for i := range settings.Templates {
			if settings.Templates[i].ID == input.ID {
				settings.Templates[i].Name, settings.Templates[i].Remark, settings.Templates[i].Content = strings.TrimSpace(input.Name), strings.TrimSpace(input.Remark), input.Content
				updated = true
				break
			}
		}
		if !updated {
			http.Error(w, "模板不存在", http.StatusNotFound)
			return
		}
	case "delete":
		id := input.ID
		if id == "" {
			id = ps.ByName("id")
		}
		if id == defaultNotificationTemplateID {
			http.Error(w, "默认模板不能删除", http.StatusBadRequest)
			return
		}
		found := false
		filtered := settings.Templates[:0]
		for _, item := range settings.Templates {
			if item.ID == id {
				found = true
				continue
			}
			filtered = append(filtered, item)
		}
		if !found {
			http.Error(w, "模板不存在", http.StatusNotFound)
			return
		}
		settings.Templates = filtered
		if settings.ActiveTemplateID == id {
			settings.ActiveTemplateID = defaultNotificationTemplateID
		}
	case "select":
		id := input.ID
		if id == "" {
			id = ps.ByName("id")
		}
		found := false
		for _, item := range settings.Templates {
			if item.ID == id {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "模板不存在", http.StatusNotFound)
			return
		}
		settings.ActiveTemplateID = id
	default:
		http.Error(w, "未知模板操作", http.StatusBadRequest)
		return
	}
	if err := u.saveNotification(settings); err != nil {
		http.Error(w, "保存模板失败", http.StatusInternalServerError)
		return
	}
	u.writeJSON(w, map[string]any{"success": true, "notification": u.notificationSettings()})
}
func (u *Unpackerr) notificationTestAPI(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	settings := u.notificationSettings()
	if !settings.Enabled || settings.URL == "" {
		http.Error(w, "通知功能未启用或通知地址为空", http.StatusBadRequest)
		return
	}
	u.sendNotification(settings, "✅", "通知测试", "UnpackFlow 本地测试", "notification-test")
	u.writeJSON(w, map[string]any{"success": true, "message": "\u6d4b\u8bd5\u901a\u77e5\u5df2\u63d0\u4ea4"})
}

func (u *Unpackerr) cd2RefreshAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	// A manual cloud sync is deliberately ordered: first inspect the 115 source
	// folders, then ask CloudDrive2 to refresh the mounted fallback path. This
	// makes the button useful even when CD2 has not yet surfaced a new file.
	messages := make([]string, 0, 2)
	if u.CloudDrive2.N115Enabled && strings.TrimSpace(u.CloudDrive2.N115Cookie) != "" && (len(n115SourceCIDs(u.CloudDrive2)) > 0 || len(parse115DownloadMappings(u.CloudDrive2.N115DownloadMappings)) > 0) {
		u.poll115RecentOperations()
		messages = append(messages, "已同步 115 生活记录")
	} else {
		messages = append(messages, "115 未配置，已跳过")
	}
	u.cd2Mu.RLock()
	client := u.cd2Client
	u.cd2Mu.RUnlock()
	found := 0
	if u.CloudDrive2.Enabled && client != nil {
		refreshErrors := 0
		for _, refreshPath := range cloudDriveConfiguredRefreshPaths(u.CloudDrive2) {
			if err := client.ForceRefresh(r.Context(), refreshPath); err != nil {
				refreshErrors++
				u.Errorf("CloudDrive2 手动刷新失败：%s：%v", refreshPath, err)
			}
		}
		found = u.cloudDriveFallbackScanPaths(client, cloudDriveManualWatchPaths(u.CloudDrive2), u.CloudDrive2.PathOverrides)
		messages = append(messages, fmt.Sprintf("已刷新 CD2 配置目录，发现 %d 个压缩文件，失败 %d 个目录", found, refreshErrors))
	} else {
		messages = append(messages, "CD2 未连接，已跳过")
	}
	u.Printf("手动云端同步完成：%s", strings.Join(messages, "；"))
	u.writeJSON(w, map[string]any{"success": true, "found": found, "message": strings.Join(messages, "；")})
}

func (u *Unpackerr) n115SyncAPI(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	if !u.CloudDrive2.N115Enabled || strings.TrimSpace(u.CloudDrive2.N115Cookie) == "" {
		http.Error(w, "请先启用 115 云解压并填写 Cookie", http.StatusBadRequest)
		return
	}
	if len(n115SourceCIDs(u.CloudDrive2)) == 0 && len(parse115DownloadMappings(u.CloudDrive2.N115DownloadMappings)) == 0 {
		http.Error(w, "请至少配置一个云解压来源或本地下载文件夹", http.StatusBadRequest)
		return
	}
	u.poll115RecentOperations()
	u.Printf("115 手动同步完成")
	u.writeJSON(w, map[string]any{"success": true})
}

func (u *Unpackerr) n115FallbackAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Key) == "" {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	var pending Pending115
	found := false
	if u.state != nil {
		u.state.mu.RLock()
		for _, item := range u.state.Fallback115 {
			if item.TaskKey == input.Key || item.Key == input.Key {
				pending, found = item, true
				break
			}
		}
		u.state.mu.RUnlock()
	}
	if !found {
		http.Error(w, "未找到可本地解压的云端任务", http.StatusNotFound)
		return
	}
	// Persisted tasks may predate a folder mapping change. Never refresh the
	// historical path; resolve the configured route by CID at approval time.
	currentPath, configured := u.current115PendingPath(pending)
	if !configured || currentPath == "" {
		http.Error(w, "任务目录已从当前配置移除，请重新同步", http.StatusConflict)
		return
	}
	pending.CD2Path = currentPath
	u.update115Transfer(input.Key, pending.FileName, "正在批准本地下载", func(task *CD2Transfer) { task.CanFallback = false })
	u.approvePending115Task(input.Key)
	u.refresh115Fallback(N115Mapping{FallbackCID: pending.FallbackCID, CD2Path: pending.CD2Path}, n115File{FID: pending.FID, Name: pending.FileName})
	u.writeJSON(w, map[string]any{"success": true, "message": "已刷新 CD2 指定路径，等待缓存"})
}

func (u *Unpackerr) settingsAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var overrides UIOverrides
	if err := json.NewDecoder(r.Body).Decode(&overrides); err != nil {
		http.Error(w, "\u8bf7\u6c42\u683c\u5f0f\u9519\u8bef", http.StatusBadRequest)
		return
	}
	normalizeStructured115Settings(&overrides)
	if overrides.Workers > 0 {
		u.Parallel = overrides.Workers
	}
	switch overrides.LocalSourceAction {
	case "", "keep", "delete":
	case "archive":
		if strings.TrimSpace(overrides.LocalArchiveDir) == "" {
			http.Error(w, "选择归档原包时必须填写归档目录", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "本地原包处理方式无效", http.StatusBadRequest)
		return
	}
	if overrides.FolderInterval != "" {
		duration, err := time.ParseDuration(overrides.FolderInterval)
		if err != nil || duration < 0 {
			http.Error(w, "定时扫描间隔格式无效，例如：1s、30s、2m；填写 0s 可关闭定时扫描", http.StatusBadRequest)
			return
		}
	}
	if overrides.CD2FallbackInterval != "" {
		duration, err := time.ParseDuration(overrides.CD2FallbackInterval)
		if err != nil || duration < 0 {
			http.Error(w, "CD2 定时扫描间隔格式无效，例如：30m；填写 0s 可关闭", http.StatusBadRequest)
			return
		}
	}
	if overrides.N115RetryCount > 0 && overrides.N115RetryCount > 10 {
		http.Error(w, "115 云解压重试次数不能超过 10 次", http.StatusBadRequest)
		return
	}
	if overrides.N115RetryDelay != "" {
		duration, err := time.ParseDuration(overrides.N115RetryDelay)
		if err != nil || duration <= 0 {
			http.Error(w, "115 云解压重试间隔格式无效，例如：2m", http.StatusBadRequest)
			return
		}
	}
	if err := validate115CloudSettings(overrides); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// When 115 monitoring is disabled, an old 0s interval must not prevent the
	// user from saving unrelated CloudDrive2 paths and cache settings.
	if overrides.N115Enabled != nil && *overrides.N115Enabled && overrides.N115EventEnabled != nil && *overrides.N115EventEnabled && overrides.N115EventInterval != "" {
		duration, err := time.ParseDuration(overrides.N115EventInterval)
		if err != nil || duration <= 0 {
			http.Error(w, "115 事件间隔格式无效，例如：5m", http.StatusBadRequest)
			return
		}
	}
	switch overrides.N115SuccessAction {
	case "", "keep", "delete":
	case "archive":
		if strings.TrimSpace(overrides.N115ArchiveCID) == "" {
			http.Error(w, "归档 115 原包时必须填写归档 CID", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "115 原包处理方式无效", http.StatusBadRequest)
		return
	}
	if overrides.LocalSourceDelay != "" {
		duration, err := time.ParseDuration(overrides.LocalSourceDelay)
		if err != nil || duration < 0 {
			http.Error(w, "原包处理延迟格式无效，例如：0s、10m、1h", http.StatusBadRequest)
			return
		}
	}
	// Copy timeout only applies when the CD2 direct monitor is enabled. Keeping
	// an older zero value while CD2 is off should not block a settings update.
	if overrides.CD2Enabled != nil && *overrides.CD2Enabled && overrides.CopyTimeout != "" {
		duration, err := time.ParseDuration(overrides.CopyTimeout)
		if err != nil || duration <= 0 {
			http.Error(w, "复制超时格式无效，例如：24h", http.StatusBadRequest)
			return
		}
	}
	if err := u.saveUIOverrides(overrides); err != nil {
		u.Errorf("保存设置失败：%v", err)
		http.Error(w, "保存设置失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	u.writeJSON(w, map[string]any{"success": true, "restart_required": true, "paths_applied": true})
}

func (u *Unpackerr) historyAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct {
		Key    string `json:"key"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Key) == "" || (input.Action != "delete" && input.Action != "retry") {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	action := historyAction{Key: input.Key, Action: input.Action, result: make(chan error, 1)}
	select {
	case u.historyActions <- action:
	case <-r.Context().Done():
		http.Error(w, "请求已取消", http.StatusRequestTimeout)
		return
	}
	if err := <-action.result; err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.writeJSON(w, map[string]any{"success": true})
}

func (u *Unpackerr) cancelTaskAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Key) == "" {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(input.Key)
	if cancel, ok := u.cd2Cancel.Load(key); ok {
		cancel.(context.CancelFunc)()
	}
	u.cancelled.Store(key, struct{}{})
	u.cd2Copy.Delete(key)
	u.removePendingCD2("copy|" + filepath.Clean(key))
	if transfer, ok := u.cd2Tasks.Load(key); ok {
		if item, valid := transfer.(*CD2Transfer); valid && item != nil && item.CachedPath != "" {
			_ = os.Remove(item.CachedPath)
		}
		u.updateCD2Transfer(key, key, "已取消", func(item *CD2Transfer) { item.Error = "用户取消" })
	}
	u.writeJSON(w, map[string]any{"success": true})
}

// downloadsPauseAPI controls CloudDrive2-to-local cache copies only.
// Extraction already in progress is deliberately not interrupted.
func (u *Unpackerr) downloadsPauseAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct {
		Paused bool `json:"paused"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	if err := u.setDownloadsPaused(input.Paused); err != nil {
		u.Errorf("保存下载暂停状态失败：%v", err)
		http.Error(w, "保存暂停状态失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	if input.Paused {
		u.cd2Cancel.Range(func(_, value any) bool {
			if cancel, ok := value.(context.CancelFunc); ok && cancel != nil {
				cancel()
			}
			return true
		})
		u.Printf("已暂停所有本地下载任务，正在复制的任务将在当前进度停止")
	} else {
		u.Printf("已恢复本地下载任务")
		go u.resumeCD2Pending()
	}
	u.writeJSON(w, map[string]any{"success": true, "paused": input.Paused})
}

// downloadsCleanupAPI deletes globally paused or failed cache downloads.
// It intentionally refuses to run while downloads are active, avoiding races
// with a live copy and making the operation explicit in the web UI.
func (u *Unpackerr) downloadsCleanupAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !u.downloadsPaused.Load() {
		http.Error(w, "请先暂停下载，再清理未完成缓存", http.StatusConflict)
		return
	}
	u.cd2Cancel.Range(func(_, value any) bool {
		if cancel, ok := value.(context.CancelFunc); ok && cancel != nil {
			cancel()
		}
		return true
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		active := false
		u.cd2Copy.Range(func(_, _ any) bool {
			active = true
			return false
		})
		if !active {
			break
		}
		if time.Now().After(deadline) {
			http.Error(w, "仍有下载任务正在停止，请稍后再试", http.StatusConflict)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	cleared, err := u.clearIncompleteCD2Cache()
	if err != nil {
		u.Errorf("清理未完成缓存失败：%v", err)
		http.Error(w, "清理未完成缓存失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	u.Printf("已清理 %d 个未完成的本地下载任务及缓存", cleared)
	u.writeJSON(w, map[string]any{"success": true, "cleared": cleared})
}

func (u *Unpackerr) taskSystemAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || (input.Action != "stop_clear" && input.Action != "resume") {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	action := taskControlAction{Action: input.Action, result: make(chan taskControlResult, 1)}
	select {
	case u.taskActions <- action:
	case <-r.Context().Done():
		http.Error(w, "请求已取消", http.StatusRequestTimeout)
		return
	}
	result := <-action.result
	if result.Error != nil {
		http.Error(w, result.Error.Error(), http.StatusInternalServerError)
		return
	}
	message := "任务系统已恢复"
	if input.Action == "stop_clear" {
		message = fmt.Sprintf("任务系统已暂停，已清理 %d 个等待任务", result.Cleared)
	}
	u.writeJSON(w, map[string]any{"success": true, "paused": u.taskSystemPaused.Load(), "cleared": result.Cleared, "message": message})
}

func (u *Unpackerr) maintenanceAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var input struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	if input.Action != "clear_cache" && input.Action != "clear_history" {
		http.Error(w, "不支持的维护操作", http.StatusBadRequest)
		return
	}
	action := taskControlAction{Action: input.Action, result: make(chan taskControlResult, 1)}
	select {
	case u.taskActions <- action:
	case <-r.Context().Done():
		http.Error(w, "请求已取消", http.StatusRequestTimeout)
		return
	}
	result := <-action.result
	if result.Error != nil {
		http.Error(w, result.Error.Error(), http.StatusConflict)
		return
	}
	if input.Action == "clear_cache" {
		u.Printf("已清除全部本地缓存：%d 个项目", result.Cleared)
		u.writeJSON(w, map[string]any{"success": true, "cleared": result.Cleared, "message": fmt.Sprintf("已清除 %d 个缓存项目", result.Cleared)})
		return
	}
	u.Printf("已清除全部解压历史和防重复记录")
	u.writeJSON(w, map[string]any{"success": true, "message": "已清除全部历史记录"})
}

func (u *Unpackerr) handleHistoryAction(action historyAction) error {
	item, ok := u.deleteProcessed(action.Key)
	if !ok {
		return fmt.Errorf("历史记录不存在")
	}
	if action.Action == "delete" {
		return nil
	}
	if u.folders == nil {
		u.restoreProcessed(item)
		return fmt.Errorf("目录监控尚未启动")
	}
	if item.Source == "local" {
		if _, err := os.Stat(item.Path); err != nil {
			u.restoreProcessed(item)
			return fmt.Errorf("源文件不存在，无法重试")
		}
		u.folders.InjectFileEvent(item.Path, "history retry")
		return nil
	}
	if item.Source == "cd2" {
		if _, err := os.Stat(item.Path); err == nil && dashboardPathPrefix(item.Path, u.CloudDrive2.CacheDir) {
			u.cd2Cache.Store(filepath.Clean(item.Path), append([]string(nil), item.Files...))
			u.savePendingCD2(PendingCD2{Key: filepath.Clean(item.Path), Files: append([]string(nil), item.Files...), CachedPrimary: item.Path, Version: item})
			u.cd2Resume.Store(filepath.Clean(item.Path), struct{}{})
			u.folders.InjectFileEvent(item.Path, "history retry cached")
			return nil
		}
		files := append([]string(nil), item.Files...)
		if len(files) == 0 {
			files = []string{item.Path}
		}
		for _, file := range files {
			if _, err := os.Stat(file); err != nil {
				u.restoreProcessed(item)
				return fmt.Errorf("CD2 源文件不存在，无法重试")
			}
		}
		u.cacheCloudDrivePaths(files)
		return nil
	}
	u.restoreProcessed(item)
	return fmt.Errorf("不支持的任务来源")
}
func (u *Unpackerr) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
