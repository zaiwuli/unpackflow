package unpackerr

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
)

func TestDashboardPageContainsRequiredChineseViews(t *testing.T) {
	t.Parallel()

	u := New()
	recorder := httptest.NewRecorder()
	u.dashboardPage(recorder, httptest.NewRequest(http.MethodGet, "/", nil), nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected dashboard status: %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, required := range []string{"UnpackFlow", `data-view="tasks-view"`, `data-view="password-view"`, `data-view="logs-view"`, `id="logs"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard is missing %q", required)
		}
	}
	if strings.Contains(body, "/*__CSS__*/") || strings.Contains(body, "/*__JS__*/") {
		t.Fatal("dashboard assets were not embedded")
	}
}

func TestHealthAPI(t *testing.T) {
	t.Parallel()

	u := New()
	recorder := httptest.NewRecorder()
	u.healthAPI(recorder, httptest.NewRequest(http.MethodGet, "/health", nil), httprouter.Params{})
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected health status: %d", recorder.Code)
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "ok" {
		t.Fatalf("unexpected health response: %#v", response)
	}
}

func TestUISettingsPersistAcrossRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "unpackerr.conf")
	u := New()
	u.ConfigFile = configPath
	if err := u.loadUIStore(); err != nil {
		t.Fatal(err)
	}
	enabled, keep, deleteSource := true, true, false
	overrides := UIOverrides{
		Workers:          2,
		CD2Enabled:       &enabled,
		CD2URL:           "http://192.168.31.2:19798",
		CD2Token:         "secret-token",
		WatchPath:        "/115open/上传下载",
		RefreshPath:      "/115open/上传下载",
		RefreshInterval:  "15m",
		PathOverrides:    []string{"/115open=>/mnt/cd2/115open"},
		CacheDir:         "/cache",
		CacheExtractPath: "/output",
		KeepCache:        &keep,
		DeleteSource:     &deleteSource,
		CacheDeleteDelay: "2m",
	}
	if err := u.saveUIOverrides(overrides); err != nil {
		t.Fatal(err)
	}
	withoutToken := overrides
	withoutToken.CD2Token = ""
	withoutToken.Workers = 3
	if err := u.saveUIOverrides(withoutToken); err != nil {
		t.Fatal(err)
	}
	if err := u.saveNotification(UINotification{Enabled: true, URL: "http://notify.local/webhook"}); err != nil {
		t.Fatal(err)
	}
	if err := u.addUIPassword("plain-password"); err != nil {
		t.Fatal(err)
	}

	restarted := New()
	restarted.ConfigFile = configPath
	if err := restarted.loadUIStore(); err != nil {
		t.Fatal(err)
	}
	if restarted.Parallel != 3 || !restarted.CloudDrive2.Enabled || restarted.CloudDrive2.WatchPath != overrides.WatchPath {
		t.Fatalf("settings were not restored: %#v", restarted.uiSettings())
	}
	if restarted.CloudDrive2.Token != "secret-token" {
		t.Fatal("CD2 token was not restored")
	}
	if got := restarted.uiPasswords(); len(got) != 1 || got[0] != "plain-password" {
		t.Fatalf("passwords were not restored: %#v", got)
	}
	if got := restarted.notificationSettings(); !got.Enabled || got.URL != "http://notify.local/webhook" {
		t.Fatalf("notification was not restored: %#v", got)
	}
}

func TestNotificationUsesGETAndEncodedText(t *testing.T) {
	t.Parallel()

	requestReceived := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived <- r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer server.Close()

	u := New()
	u.uiStore = &UIStore{Notification: UINotification{Enabled: true, URL: server.URL + "?apikey=test"}}
	u.notifyUI(EXTRACTED, &Extract{Path: "中文 文件.zip", App: FolderString, Updated: time.Now()})

	select {
	case request := <-requestReceived:
		if request.Method != http.MethodGet {
			t.Fatalf("expected GET notification, got %s", request.Method)
		}
		if request.URL.Query().Get("apikey") != "test" {
			t.Fatal("existing notification query parameter was lost")
		}
		text := request.URL.Query().Get("text")
		if !strings.Contains(text, "UnpackFlow 解压完成") || !strings.Contains(text, "中文 文件.zip") {
			t.Fatalf("unexpected notification text: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for notification request")
	}
}

func TestCD2CacheThenRealZipExtraction(t *testing.T) {
	t.Parallel()

	sourceDir, cacheDir, outputDir := t.TempDir(), t.TempDir(), t.TempDir()
	source := filepath.Join(sourceDir, "cloud.zip")
	createZipFixture(t, source, "from-cloud.txt", "cloud data")

	u := New()
	u.CloudDrive2.CacheDir = cacheDir
	u.CloudDrive2.CacheExtractPath = outputDir
	cacheConfig := &FolderConfig{Path: cacheDir, ExtractPath: outputDir, ExternalOnly: true}
	u.folders = newTestFolders(t, cacheConfig)
	if err := u.cacheCloudDriveGroup([]string{source}, source); err != nil {
		t.Fatal(err)
	}

	var event *eventData
	select {
	case event = <-u.folders.Events:
	case <-time.After(2 * time.Second):
		t.Fatal("CD2 cache did not submit an extraction event")
	}
	u.folders.processEvent(event, time.Now())
	cached := filepath.Join(cacheDir, "cloud.zip")
	if _, ok := u.folders.Folders[cached]; !ok {
		t.Fatalf("cached archive was not tracked: %s", cached)
	}

	done := runExtractionTo(t, cached, outputDir)
	if done.Error != nil {
		t.Fatalf("cached CD2 ZIP extraction failed: %v", done.Error)
	}
	data, err := os.ReadFile(filepath.Join(done.Output, "from-cloud.txt"))
	if err != nil {
		t.Fatalf("extracted CD2 file is missing: %v; output=%s; new_files=%v; output_tree=%v; cache_tree=%v",
			err, done.Output, done.NewFiles, listFiles(outputDir), listFiles(cacheDir))
	}
	if string(data) != "cloud data" {
		t.Fatalf("unexpected extracted content: %q", data)
	}
}

func createZipFixture(t *testing.T, archive, name, content string) {
	t.Helper()
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(entry, bytes.NewBufferString(content)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}
