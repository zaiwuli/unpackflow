package unpackerr

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golift.io/cnfg"
)

func lifecycleTestApp(t *testing.T) *Unpackerr {
	t.Helper()
	u := New()
	u.ConfigFile = filepath.Join(t.TempDir(), "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestHistoryVisibilitySurvivesRestartWithoutRemovingProtection(t *testing.T) {
	u := lifecycleTestApp(t)
	item := ProcessedSource{Key: "archive|123", Path: "archive.zip", Size: 123, Source: "local"}
	u.markProcessed(item)
	if err := u.setIgnoredPath("ignored.zip", true); err != nil {
		t.Fatal(err)
	}
	if err := u.hideHistory(item.Key); err != nil {
		t.Fatal(err)
	}
	if !u.wasProcessed(item) || len(u.processedHistory()) != 0 {
		t.Fatal("hide must retain processed identity but hide the row")
	}
	if err := u.clearAllHistory(); err != nil {
		t.Fatal(err)
	}
	v := New()
	v.ConfigFile = u.ConfigFile
	if err := v.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	if !v.wasProcessed(item) || !v.isIgnoredPath("ignored.zip") {
		t.Fatal("restart lost hidden processing rules")
	}
	if len(v.processedHistory()) != 0 {
		t.Fatal("hidden history reappeared")
	}
	for _, ignored := range v.state.Ignored {
		if !ignored.Hidden {
			t.Fatal("clear history did not hide ignored row")
		}
	}
}

func TestFailedRetryQueuesOnlyOnceAndResetsAttempts(t *testing.T) {
	u := lifecycleTestApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "retry.zip")
	createZipFixture(t, path, "file.txt", "content")
	u.folders = newTestFolders(t, &FolderConfig{Path: dir})
	item, err := sourceVersion("local", path)
	if err != nil {
		t.Fatal(err)
	}
	item.Stage, item.Error = "extract", "bad password"
	u.markFailed(item)
	u.folders.Folders[path] = &Folder{status: EXTRACTFAILED, retries: 5}
	u.MaxRetries = 5
	if err := u.retryHistory(item.Key); err != nil {
		t.Fatal(err)
	}
	if folder := u.folders.Folders[path]; folder.status != WAITING || folder.retries != 0 {
		t.Fatalf("retry did not reset state: %#v", folder)
	}
	if err := u.retryHistory(item.Key); err == nil {
		t.Fatal("duplicate retry accepted")
	}
}

func TestRetryUsesCompleteCacheWithoutMountedSource(t *testing.T) {
	u := lifecycleTestApp(t)
	cache := t.TempDir()
	path := filepath.Join(cache, "cached.7z")
	if err := os.WriteFile(path, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.CloudDrive2.CacheDir = cache
	u.folders = newTestFolders(t, &FolderConfig{Path: cache, ExternalOnly: true})
	item := ProcessedSource{Key: "cached.7z|5", Source: "cd2", Path: filepath.Join(t.TempDir(), "cached.7z"), CachedPath: path, Size: 5, Stage: "extract"}
	item.Files = []string{item.Path}
	u.markFailed(item)
	if err := u.retryHistory(item.Key); err != nil {
		t.Fatal(err)
	}
	if folder := u.folders.Folders[path]; folder == nil || folder.status != WAITING {
		t.Fatal("complete cache not queued")
	}
	if _, running := u.cd2Copy.Load(cloudDriveTaskKey(item.Path)); running {
		t.Fatal("retry downloaded an already complete cache")
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if completeRetryCache(item) {
		t.Fatal("truncated cache accepted")
	}
}

func TestMissingSourceRetryPreservesFailure(t *testing.T) {
	u := lifecycleTestApp(t)
	dir := t.TempDir()
	u.folders = newTestFolders(t, &FolderConfig{Path: dir})
	item := ProcessedSource{Key: "missing", Source: "local", Path: filepath.Join(dir, "missing.zip"), Stage: "extract", Error: "original failure"}
	u.markFailed(item)
	if err := u.retryHistory(item.Key); err == nil {
		t.Fatal("missing source accepted")
	}
	if u.state.Failed[item.Key].Error != "original failure" {
		t.Fatal("failure record lost")
	}
}

func TestCleanupRetryNeverQueuesExtraction(t *testing.T) {
	u := lifecycleTestApp(t)
	dir, archive := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "done.zip")
	if err := os.WriteFile(path, []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &FolderConfig{Path: dir, ArchivePath: archive, DeleteAfter: &cnfg.Duration{}}
	u.folders = newTestFolders(t, cfg)
	u.folders.Folders[path] = &Folder{config: cfg, status: DELETEFAILED}
	item, err := sourceVersion("local", path)
	if err != nil {
		t.Fatal(err)
	}
	u.markProcessed(item)
	item.Stage = "cleanup"
	u.markFailed(item)
	if err := u.retryHistory(item.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(archive, "done.zip")); err != nil {
		t.Fatal(err)
	}
	if u.folders.Folders[path] != nil {
		t.Fatal("cleanup queued extraction")
	}
	if !u.wasProcessed(item) {
		t.Fatal("cleanup retry removed deduplication")
	}
	if task := u.Map[path]; task != nil && task.Status <= EXTRACTING {
		t.Fatal("cleanup created a queued task")
	}
}

func TestCleanupRetryAfterRestartUsesOnlyRemainingFiles(t *testing.T) {
	u := lifecycleTestApp(t)
	dir, archive := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "remaining.zip")
	if err := os.WriteFile(path, []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	item, err := sourceVersion("local", path)
	if err != nil {
		t.Fatal(err)
	}
	u.markProcessed(item)
	item.Stage, item.Files = "cleanup", []string{path}
	u.markFailed(item)
	v := New()
	v.ConfigFile = u.ConfigFile
	if err := v.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	v.folders = newTestFolders(t, &FolderConfig{Path: dir, ArchivePath: archive, DeleteAfter: &cnfg.Duration{}})
	if err := v.retryHistory(item.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(archive, "remaining.zip")); err != nil {
		t.Fatal(err)
	}
	if !v.wasProcessed(item) || len(v.folders.Folders) != 0 {
		t.Fatal("restart cleanup touched extraction state")
	}
}

func TestDeleteFailureIsRetryableWithoutExtraction(t *testing.T) {
	u := lifecycleTestApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "delete.zip")
	if err := os.WriteFile(path, []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.folders = newTestFolders(t, &FolderConfig{Path: dir, DeleteOrig: true})
	item, err := sourceVersion("local", path)
	if err != nil {
		t.Fatal(err)
	}
	u.markProcessed(item)
	item.Stage, item.CleanupKind, item.Files = "cleanup", "delete", []string{path}
	u.finishLocalDelete(item, item.Files)
	if !u.hasFailedVersion(item) {
		t.Fatal("remaining source deletion failure was lost")
	}
	if err := u.retryHistory(item.Key); err != nil {
		t.Fatal(err)
	}
	if err := u.retryHistory(item.Key); err == nil {
		t.Fatal("duplicate cleanup accepted")
	}
	request := <-u.delChan
	if request.Version == nil || len(request.Paths) != 1 || request.Paths[0] != path {
		t.Fatal("wrong cleanup request")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	u.finishLocalDelete(*request.Version, request.Paths)
	if u.hasFailedVersion(item) || !u.wasProcessed(item) || len(u.folders.Folders) != 0 {
		t.Fatal("delete retry changed extraction protection")
	}
}

func TestDashboardSeparatesTerminalAndPendingFailures(t *testing.T) {
	u := lifecycleTestApp(t)
	u.markFailed(ProcessedSource{Key: "failed", Path: "failed.zip", Source: "local", Stage: "extract", Error: "bad password"})
	u.update115Transfer("pending", "pending.zip", "云解压失败，等待批准本地下载", func(task *CD2Transfer) { task.CanFallback = true })
	u.update115Transfer("failed", "failed.zip", "解压失败", nil)
	u.state.Fallback115["pending"] = Pending115{Key: "pending", TaskKey: "pending", FileName: "pending.zip", Approval: true}
	u.markFailed(ProcessedSource{Key: "pending", Path: "pending.zip", Source: "115"})
	snapshot := u.dashboardSnapshot()
	if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Key != "pending" || snapshot.Totals.Active != 1 {
		t.Fatalf("wrong current tasks: %#v", snapshot.Tasks)
	}
	if len(snapshot.History) != 1 || snapshot.History[0].Status != "failed" || !snapshot.History[0].CanRetry {
		t.Fatalf("wrong terminal history: %#v", snapshot.History)
	}
}

func TestDashboardRestoresCloudRetryForFailedArchive(t *testing.T) {
	u := lifecycleTestApp(t)
	u.state.Fallback115["pending"] = Pending115{Key: "pending", TaskKey: "cloud-failed", Kind: "cloud_failure", SourceCID: "source", FileName: "failed.7z", Approval: true}

	transfers := u.dashboardTransfers()
	if len(transfers) != 1 || !transfers[0].CanFallback || !transfers[0].CanCloudRetry {
		t.Fatalf("cloud failure actions were not restored: %#v", transfers)
	}
	snapshot := u.dashboardSnapshot()
	if len(snapshot.Tasks) != 1 || !snapshot.Tasks[0].CanFallback || !snapshot.Tasks[0].CanCloudRetry {
		t.Fatalf("cloud retry action missing from dashboard: %#v", snapshot.Tasks)
	}
}

func TestWaitingFailureIsActiveAndOrderingKeepsOriginalStart(t *testing.T) {
	for _, status := range []string{"云解压失败，等待批准本地下载", "解压失败，等待自动重试", "正在取消"} {
		if !dashboardTaskIsActive(status) {
			t.Fatalf("waiting task classified terminal: %s", status)
		}
	}
	if dashboardTaskIsActive("解压失败") {
		t.Fatal("terminal failure classified active")
	}
	first := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	merged := mergeDashboardTask(DashboardTask{Key: "one", StartedAt: first, Status: "正在复制"}, DashboardTask{Key: "one", StartedAt: time.Now().Format(time.RFC3339Nano), Status: "正在解压"})
	if merged.StartedAt != first {
		t.Fatal("stage transition changed task ordering")
	}
}

func TestFinish115CancelledTaskRemovesCurrentTask(t *testing.T) {
	u := lifecycleTestApp(t)
	u.update115Transfer("cancelled", "cancelled.7z", "正在取消", nil)
	u.n115Running.Store("cancelled", struct{}{})

	u.finish115CancelledTask("cancelled", "cancelled.7z")

	if _, exists := u.cd2Tasks.Load("cancelled"); exists {
		t.Fatal("cancelled 115 task remained visible after its operation returned")
	}
	if tasks := u.dashboardSnapshot().Tasks; len(tasks) != 0 {
		t.Fatalf("cancelled 115 task remained in current tasks: %#v", tasks)
	}
}

func TestTerminal115FailureLivesOnlyInHistory(t *testing.T) {
	u := lifecycleTestApp(t)
	version := ProcessedSource{Key: "terminal", Path: "terminal.7z", Source: "115", Stage: "cloud", Error: "service unavailable"}
	u.update115Transfer(version.Key, version.Path, "115 服务失败", nil)
	u.markFailed(version)
	u.cd2Tasks.Delete(version.Key)

	snapshot := u.dashboardSnapshot()
	if len(snapshot.Tasks) != 0 || snapshot.Totals.Active != 0 {
		t.Fatalf("terminal failure remained active: %#v", snapshot.Tasks)
	}
	if len(snapshot.History) != 1 || snapshot.History[0].Key != version.Key || snapshot.History[0].Status != "failed" {
		t.Fatalf("terminal failure missing from history: %#v", snapshot.History)
	}
}

func TestIgnoreSurvivesRestartAndUnignoreDoesNotQueue(t *testing.T) {
	u := lifecycleTestApp(t)
	key := "115|source|file|9"
	u.state.Fallback115["pending"] = Pending115{Key: "pending", TaskKey: key, SourceCID: "source", FID: "file", FileName: "named.zip", Size: 9}
	u.update115Transfer(key, "named.zip", "等待批准本地下载", nil)
	if err := u.handleTaskMutation(key, "ignore"); err != nil {
		t.Fatal(err)
	}
	v := New()
	v.ConfigFile = u.ConfigFile
	if err := v.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	if !v.isIgnoredPath("named.zip") || len(v.dashboardTransfers()) != 0 {
		t.Fatal("ignored task resurrected")
	}
	for ignoredKey := range v.state.Ignored {
		if err := v.handleTaskMutation(ignoredKey, "unignore"); err != nil {
			t.Fatal(err)
		}
	}
	if v.isIgnoredPath("named.zip") || len(v.dashboardTransfers()) != 0 {
		t.Fatal("unignore must only release rule")
	}
}

func TestResetProcessingHistoryAllowsArchiveAgain(t *testing.T) {
	u := lifecycleTestApp(t)
	item := ProcessedSource{Key: "archive.zip|10", Path: "archive.zip", Size: 10, Source: "local"}
	u.markProcessed(item)
	u.markFailed(ProcessedSource{Key: "failed.zip|20", Path: "failed.zip", Size: 20, Source: "local", Error: "failed"})
	if err := u.setIgnoredPath("ignored.zip", true); err != nil {
		t.Fatal(err)
	}
	u.cancelled.Store(item.Key, struct{}{})
	u.taskSystemPaused.Store(true)

	cleared, err := u.resetAllProcessingHistory()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 3 || u.wasProcessed(item) || u.hasFailedVersion(ProcessedSource{Key: "failed.zip|20", Path: "failed.zip", Size: 20}) || u.isIgnoredPath("ignored.zip") || u.taskCancelled(item.Key) {
		t.Fatalf("processing history was not reset: cleared=%d", cleared)
	}
}
