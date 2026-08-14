package unpackerr

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Unpackerr/unpackerr/pkg/clouddrive"
	"github.com/julienschmidt/httprouter"
	"golift.io/cnfg"
)

func TestDockerDefaultFolderEnvironment(t *testing.T) {
	t.Setenv("UN_FOLDER_0_PATH", "/downloads")
	t.Setenv("UN_FOLDER_0_EXTRACT_PATH", "/output")
	t.Setenv("UN_FOLDER_0_MOVE_BACK", "false")
	t.Setenv("UN_FOLDER_0_DISABLE_LOG", "true")
	t.Setenv("UN_FOLDER_0_DELETE_ORIGINAL", "false")

	u := New()
	if _, err := cnfg.UnmarshalENV(u.Config, "UN"); err != nil {
		t.Fatal(err)
	}
	if len(u.Folders) != 1 {
		t.Fatalf("expected one default folder from environment, got %d", len(u.Folders))
	}
	folder := u.Folders[0]
	if folder.Path != "/downloads" || folder.ExtractPath != "/output" || folder.MoveBack || !folder.DisableLog || folder.DeleteOrig {
		t.Fatalf("unexpected default folder config: %#v", folder)
	}
}

func TestFolderPollerDetectsRootZipCreatedAfterStart(t *testing.T) {
	t.Parallel()

	watchPath := t.TempDir()
	cfg := &FolderConfig{Path: watchPath}
	folders, err := (FoldersConfig{Buffer: 32, Interval: cnfg.Duration{Duration: 50 * time.Millisecond}}).newWatcher(
		[]*FolderConfig{cfg}, noopLogger{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		folders.Watcher.Close()
		_ = folders.FSNotify.Close()
	})
	go func() { _ = folders.Watcher.Start(50 * time.Millisecond) }()

	archive := filepath.Join(watchPath, "root-created.zip")
	createZipFixture(t, archive, "found.txt", "found")

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-folders.Watcher.Event:
			folders.handleFileEvent(event.Path, "w "+event.Op.String())
		case event := <-folders.Events:
			if event.file == archive {
				return
			}
		case err := <-folders.Watcher.Error:
			t.Fatal(err)
		case <-deadline.C:
			t.Fatal("poller did not detect ZIP created in watched root")
		}
	}
}

func TestStartupScanTracksExistingArchivesAndSkipsCD2Cache(t *testing.T) {
	t.Parallel()

	watchPath, cachePath := t.TempDir(), t.TempDir()
	rootArchive := filepath.Join(watchPath, "already-there.zip")
	nestedDir := filepath.Join(watchPath, "nested")
	if err := os.MkdirAll(nestedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nestedArchive := filepath.Join(nestedDir, "nested.zip")
	cacheArchive := filepath.Join(cachePath, "copying.zip")
	createZipFixture(t, rootArchive, "root.txt", "root")
	createZipFixture(t, nestedArchive, "nested.txt", "nested")
	createZipFixture(t, cacheArchive, "cache.txt", "cache")

	u := New()
	localConfig := &FolderConfig{Path: watchPath}
	cacheConfig := &FolderConfig{Path: cachePath, ExternalOnly: true}
	u.Folders = []*FolderConfig{localConfig, cacheConfig}
	folders, err := (FoldersConfig{Buffer: 32}).newWatcher(u.Folders, noopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	u.folders = folders
	t.Cleanup(func() {
		folders.Watcher.Close()
		_ = folders.FSNotify.Close()
	})

	done := make(chan struct{})
	go func() {
		u.scanExistingFolderArchives()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startup scan timed out")
	}

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case event := <-folders.Events:
			seen[event.file] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("startup scan events missing: %#v", seen)
		}
	}
	if !seen[rootArchive] || !seen[nestedArchive] {
		t.Fatalf("expected existing local archives, got %#v", seen)
	}
	if seen[cacheArchive] {
		t.Fatal("CD2 external cache must not be startup-scanned")
	}
}

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
		Workers:           2,
		LocalSourceAction: "archive",
		LocalArchiveDir:   "/archive",
		FolderInterval:    "30s",
		CD2Enabled:        &enabled,
		CD2URL:            "http://192.168.31.2:19798",
		CD2Token:          "secret-token",
		WatchPath:         "/115open/上传下载",
		RefreshPath:       "/115open/上传下载",
		RefreshInterval:   "15m",
		PathOverrides:     []string{"/115open=>/mnt/cd2/115open"},
		CacheDir:          "/cache",
		CacheExtractPath:  "/output",
		KeepCache:         &keep,
		DeleteSource:      &deleteSource,
		CacheDeleteDelay:  "2m",
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
	restarted.Folders = []*FolderConfig{{Path: "/downloads", ExtractPath: "/output"}}
	if err := restarted.loadUIStore(); err != nil {
		t.Fatal(err)
	}
	if restarted.Parallel != 3 || !restarted.CloudDrive2.Enabled || restarted.CloudDrive2.WatchPath != overrides.WatchPath {
		t.Fatalf("settings were not restored: %#v", restarted.uiSettings())
	}
	if restarted.Folder.Interval.Duration != 30*time.Second {
		t.Fatalf("folder interval was not restored: %v", restarted.Folder.Interval.Duration)
	}
	if folder := restarted.localFolder(); folder == nil || folder.ArchivePath != "/archive" || folder.DeleteOrig {
		t.Fatalf("local archive policy was not restored: %#v", folder)
	}
	if restarted.CloudDrive2.Token != "secret-token" {
		t.Fatal("CD2 token was not restored")
	}
	if got := restarted.uiPasswords(); len(got) != 1 || got[0] != "plain-password" {
		t.Fatalf("passwords were not restored: %#v", got)
	}
	if got := restarted.notificationSettings(); !got.Enabled || got.URL != "http://notify.local/webhook" {
		t.Fatalf("notification was not restored: %#v", got)
	} else if got.Events == nil || !got.Events.Discovery || !got.Events.Cache || !got.Events.Extract || !got.Events.Complete || !got.Events.Cleanup {
		t.Fatalf("legacy notification settings must enable every stage: %#v", got.Events)
	}
}

func TestUIPasswordAddDeletePersistsAcrossRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "unpackerr.conf")
	u := New()
	u.ConfigFile = configPath
	if err := u.loadUIStore(); err != nil {
		t.Fatal(err)
	}
	if err := u.addUIPassword("z-password"); err != nil {
		t.Fatal(err)
	}
	if err := u.addUIPassword("a-password"); err != nil {
		t.Fatal(err)
	}
	if err := u.removeUIPassword(0); err != nil {
		t.Fatal(err)
	}

	restarted := New()
	restarted.ConfigFile = configPath
	if err := restarted.loadUIStore(); err != nil {
		t.Fatal(err)
	}
	if got := restarted.uiPasswords(); len(got) != 1 || got[0] != "z-password" {
		t.Fatalf("unexpected persisted passwords: %#v", got)
	}
}

func TestPersistentHistoryDeleteDoesNotRetryAndRetryDoes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	archive := filepath.Join(dir, "history.zip")
	createZipFixture(t, archive, "file.txt", "content")
	cfg := &FolderConfig{Path: dir}
	u := New()
	u.ConfigFile = filepath.Join(dir, "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	u.folders = newTestFolders(t, cfg)

	version, err := sourceVersion("local", archive)
	if err != nil {
		t.Fatal(err)
	}
	u.markProcessed(version)
	if err := u.handleHistoryAction(historyAction{Key: version.Key, Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-u.folders.Events:
		t.Fatalf("deleting history must not submit extraction: %+v", event)
	default:
	}

	u.markProcessed(version)
	if err := u.handleHistoryAction(historyAction{Key: version.Key, Action: "retry"}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-u.folders.Events:
		if event.file != archive {
			t.Fatalf("unexpected retry event: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not submit archive")
	}
}

func TestProcessedHistoryDeduplicatesSameNameAndSizeAfterMetadataChange(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "same-name.zip")
	if err := os.WriteFile(archive, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	u := New()
	u.ConfigFile = filepath.Join(dir, "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	first, err := sourceVersion("local", archive)
	if err != nil {
		t.Fatal(err)
	}
	u.markProcessed(first)
	if err := os.WriteFile(archive, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := sourceVersion("local", archive)
	if err != nil {
		t.Fatal(err)
	}
	if !u.wasProcessed(changed) {
		t.Fatal("same archive name and size must remain deduplicated until history is deleted")
	}
	u.deleteProcessed(first.Key)
	if u.wasProcessed(changed) {
		t.Fatal("deleting history must allow the same name and size to be processed again")
	}
}

func TestProcessedHistoryDeduplicatesSameNameAndSizeAcrossSources(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "same-name.7z")
	if err := os.WriteFile(archive, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	u := New()
	u.ConfigFile = filepath.Join(dir, "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	first, err := sourceGroupVersion("cd2", []string{archive})
	if err != nil {
		t.Fatal(err)
	}
	u.markProcessed(first)
	changed, err := sourceVersion("local", archive)
	if err != nil {
		t.Fatal(err)
	}
	if !u.wasProcessed(changed) {
		t.Fatal("same name and size must be deduplicated across CD2 and local sources")
	}
}

func TestStartupScanSkipsPersistedArchiveUntilSizeChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	archive := filepath.Join(dir, "once.zip")
	createZipFixture(t, archive, "one.txt", "one")
	cfg := &FolderConfig{Path: dir}
	u := New()
	u.ConfigFile = filepath.Join(dir, "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	u.Folders = []*FolderConfig{cfg}
	u.folders = newTestFolders(t, cfg)
	version, err := sourceVersion("local", archive)
	if err != nil {
		t.Fatal(err)
	}
	u.markProcessed(version)
	u.scanExistingFolderArchives()
	select {
	case event := <-u.folders.Events:
		t.Fatalf("persisted archive was submitted again: %+v", event)
	default:
	}

	file, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("changed"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	u.scanExistingFolderArchives()
	select {
	case event := <-u.folders.Events:
		if event.file != archive {
			t.Fatalf("unexpected archive event after size change: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("archive was not submitted after size change")
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

func TestNotificationEventIsSentImmediately(t *testing.T) {
	t.Parallel()

	requestReceived := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived <- r.Clone(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	u := New()
	u.uiStore = &UIStore{Notification: UINotification{Enabled: true, URL: server.URL}}
	started := time.Now()
	u.notifyEvent(notifyDiscovery, "📦", "发现压缩包", "CloudDrive2", "/115open/test.7z")

	select {
	case request := <-requestReceived:
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("notification was delayed: %v", elapsed)
		}
		text := request.URL.Query().Get("text")
		if !strings.Contains(text, "发现压缩包") || !strings.Contains(text, "CloudDrive2") {
			t.Fatalf("unexpected event notification: %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for immediate notification")
	}
}

func TestNotificationStageSelection(t *testing.T) {
	t.Parallel()

	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Query().Get("text")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	u := New()
	u.uiStore = &UIStore{Notification: UINotification{
		Enabled: true,
		URL:     server.URL,
		Events: &UINotificationEvents{
			Discovery: true,
			Complete:  true,
		},
	}}

	u.notifyEvent(notifyCache, "✅", "缓存完成", "CloudDrive2", "/115open/test.7z")
	select {
	case text := <-requests:
		t.Fatalf("disabled cache stage unexpectedly sent notification: %q", text)
	case <-time.After(150 * time.Millisecond):
	}

	u.notifyEvent(notifyDiscovery, "📦", "发现压缩包", "CloudDrive2", "/115open/test.7z")
	select {
	case text := <-requests:
		if !strings.Contains(text, "发现压缩包") {
			t.Fatalf("unexpected discovery notification: %q", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("enabled discovery stage did not send notification")
	}
}

func TestCD2DiscoverySubmitsCopyAndSendsNotification(t *testing.T) {
	requestReceived := make(chan *http.Request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived <- r.Clone(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sourceDir, cacheDir := t.TempDir(), t.TempDir()
	source := filepath.Join(sourceDir, "cd2-notify.zip")
	createZipFixture(t, source, "notify.txt", "notification")
	u := New()
	u.ConfigFile = filepath.Join(t.TempDir(), "unpackerr.conf")
	u.uiStore = &UIStore{Path: filepath.Join(t.TempDir(), "unpackflow-ui.json"), Notification: UINotification{Enabled: true, URL: server.URL}}
	u.state = &ProcessingState{Path: filepath.Join(t.TempDir(), "state.json"), Processed: map[string]ProcessedSource{}, Pending: map[string]PendingCD2{}}
	u.CloudDrive2.CacheDir = cacheDir
	u.CloudDrive2.CopyTimeout.Duration = time.Minute
	u.folders = &Folders{Config: []*FolderConfig{{Path: cacheDir}}, Events: make(chan *eventData, 1), Folders: map[string]*Folder{}}

	if submitted := u.cacheCloudDrivePaths([]string{source}); submitted != 1 {
		t.Fatalf("expected one CD2 copy task, got %d", submitted)
	}
	select {
	case request := <-requestReceived:
		text := request.URL.Query().Get("text")
		if !strings.Contains(text, "发现压缩包") || !strings.Contains(text, "CloudDrive2") || !strings.Contains(text, "cd2-notify.zip") {
			t.Fatalf("unexpected CD2 discovery notification: %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CD2 discovery did not send notification")
	}
	select {
	case <-u.folders.Events:
	case <-time.After(2 * time.Second):
		t.Fatal("CD2 copy did not finish")
	}
}

func TestVisibleCD2Paths(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.zip")
	present := filepath.Join(dir, "present.zip")
	if err := os.WriteFile(present, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if visibleCD2Paths([]string{missing}) {
		t.Fatal("missing path reported as visible")
	}
	if !visibleCD2Paths([]string{missing, present}) {
		t.Fatal("present path was not detected")
	}
}

func TestDashboardLogKind(t *testing.T) {
	for _, test := range []struct {
		message string
		want    string
	}{
		{"CloudDrive2 已提交复制任务", "user"},
		{"[目录任务] 解压完成", "user"},
		{"通知已发送：解压完成", "user"},
		{"WebUI：已启动，监听 0.0.0.0:5656", "system"},
		{"Using Config File: /config/unpackerr.conf", "system"},
	} {
		if got := dashboardLogKind(test.message); got != test.want {
			t.Fatalf("dashboardLogKind(%q) = %q, want %q", test.message, got, test.want)
		}
	}
}

func TestDebugAndExplicitSystemLogsNeverEnterUserLog(t *testing.T) {
	logger := &Logger{
		Info:  log.New(io.Discard, "", 0),
		Error: log.New(io.Discard, "", 0),
		Debug: log.New(io.Discard, "", 0),
	}
	logger.Debugf("CloudDrive2 实时推送：收到文件变化")
	logger.Systemf("CloudDrive2 文件变化触发任务：test.7z")
	logs := logger.dashboardLogs()
	if len(logs) != 2 {
		t.Fatalf("unexpected log count: %d", len(logs))
	}
	for _, item := range logs {
		if item.Kind != "system" {
			t.Fatalf("diagnostic log leaked into user log: %#v", item)
		}
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

func TestCD2FallbackScanUsesDirectMappingWithoutMountAPI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("CD2 container path mappings are Linux paths")
	}
	t.Parallel()

	mountRoot, cacheDir, outputDir := t.TempDir(), t.TempDir(), t.TempDir()
	cloudRoot := filepath.Join(mountRoot, "115open")
	watchDir := filepath.Join(cloudRoot, "测试解压")
	if err := os.MkdirAll(watchDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(watchDir, "fallback.zip")
	createZipFixture(t, source, "fallback.txt", "fallback")

	u := New()
	u.ConfigFile = filepath.Join(t.TempDir(), "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	u.CloudDrive2.CacheDir = cacheDir
	u.CloudDrive2.CacheExtractPath = outputDir
	cacheConfig := &FolderConfig{Path: cacheDir, ExtractPath: outputDir, ExternalOnly: true}
	u.folders = newTestFolders(t, cacheConfig)
	client := &clouddrive.Client{BaseURL: "http://127.0.0.1:1", Token: "unused"}
	override := "/115open=>" + filepath.ToSlash(cloudRoot)

	u.cloudDriveFallbackScan(client, "/115open/测试解压", []string{override})
	select {
	case event := <-u.folders.Events:
		if event.file != filepath.Join(cacheDir, "fallback.zip") {
			t.Fatalf("unexpected fallback cache event: %+v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fallback scan did not copy and submit the archive")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "fallback.zip")); err != nil {
		t.Fatalf("fallback cache was not created: %v", err)
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
