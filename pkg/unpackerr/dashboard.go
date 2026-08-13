package unpackerr

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
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

type DashboardSnapshot struct {
	UpdatedAt    string              `json:"updated_at"`
	Totals       DashboardTotals     `json:"totals"`
	Tasks        []DashboardTask     `json:"tasks"`
	History      []string            `json:"history"`
	Folders      []DashboardFolder   `json:"folders"`
	CloudDrive   DashboardCloudDrive `json:"clouddrive2"`
	Passwords    []string            `json:"passwords"`
	Notification UINotification      `json:"notification"`
	Settings     UIOverrides         `json:"settings"`
	Logs         []DashboardLog      `json:"logs"`
}

type DashboardLog struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type DashboardTotals struct {
	Active   int  `json:"active"`
	Finished uint `json:"finished"`
	Retries  uint `json:"retries"`
	Workers  uint `json:"workers"`
}

type DashboardTask struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Status   string `json:"status"`
	Updated  string `json:"updated"`
	Retries  uint   `json:"retries"`
	Progress string `json:"progress,omitempty"`
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
		History:   []string{},
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
	}
	for name, item := range u.Map {
		task := DashboardTask{
			Name:    name,
			Source:  sourceName(item.App),
			Status:  statusName(item.Status),
			Updated: item.Updated.Format("2006-01-02 15:04:05"),
			Retries: item.Retries,
		}
		if item.XProg != nil {
			task.Progress = item.XProg.String()
		}
		if item.Status == QUEUED || item.Status == EXTRACTING || item.Status == WAITING {
			snapshot.Totals.Active++
		}
		snapshot.Tasks = append(snapshot.Tasks, task)
	}
	sort.Slice(snapshot.Tasks, func(i, j int) bool { return snapshot.Tasks[i].Updated > snapshot.Tasks[j].Updated })
	for _, item := range u.Items {
		if item != "" {
			snapshot.History = append(snapshot.History, item)
		}
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
	page := bytes.ReplaceAll(dashboardHTML, []byte("/*__CSS__*/"), dashboardCSS)
	page = bytes.ReplaceAll(page, []byte("/*__JS__*/"), dashboardJS)
	_, _ = w.Write(page)
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
	if settings.Enabled && settings.URL == "" {
		http.Error(w, "\u901a\u77e5\u5730\u5740\u4e0d\u80fd\u4e3a\u7a7a", http.StatusBadRequest)
		return
	}
	if err := u.saveNotification(settings); err != nil {
		http.Error(w, "\u4fdd\u5b58\u901a\u77e5\u8bbe\u7f6e\u5931\u8d25", http.StatusInternalServerError)
		return
	}
	u.writeJSON(w, map[string]any{"success": true})
}
func (u *Unpackerr) notificationTestAPI(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	item := &Extract{Path: "notification-test", App: FolderString, Updated: time.Now()}
	u.notifyUI(QUEUED, item)
	u.writeJSON(w, map[string]any{"success": true, "message": "\u6d4b\u8bd5\u901a\u77e5\u5df2\u63d0\u4ea4"})
}
func (u *Unpackerr) settingsAPI(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var overrides UIOverrides
	if err := json.NewDecoder(r.Body).Decode(&overrides); err != nil {
		http.Error(w, "\u8bf7\u6c42\u683c\u5f0f\u9519\u8bef", http.StatusBadRequest)
		return
	}
	if overrides.Workers > 0 {
		u.Parallel = overrides.Workers
	}
	if err := u.saveUIOverrides(overrides); err != nil {
		http.Error(w, "\u4fdd\u5b58\u8bbe\u7f6e\u5931\u8d25", http.StatusInternalServerError)
		return
	}
	u.writeJSON(w, map[string]any{"success": true, "restart_required": true})
}
func (u *Unpackerr) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
