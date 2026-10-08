package unpackerr

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse115MappingsAcceptsFallbackAndLegacyFormats(t *testing.T) {
	mappings := parse115Mappings([]string{
		"100 => 200 => /115open/fallback/movie",
		"300 => /115open/legacy",
		"not a mapping",
		" => 400 => /115open/invalid",
	})
	if len(mappings) != 2 {
		t.Fatalf("mapping count = %d, want 2", len(mappings))
	}
	if mappings[0].SourceCID != "100" || mappings[0].FallbackCID != "200" || mappings[0].CD2Path != "/115open/fallback/movie" {
		t.Fatalf("unexpected fallback mapping: %#v", mappings[0])
	}
	if !mappings[0].fallbackEnabled() {
		t.Fatal("three-part mapping must enable fallback")
	}
	if mappings[1].SourceCID != "300" || mappings[1].FallbackCID != "" || mappings[1].CD2Path != "/115open/legacy" {
		t.Fatalf("unexpected legacy mapping: %#v", mappings[1])
	}
	if mappings[1].fallbackEnabled() {
		t.Fatal("legacy mapping must not attempt a move without a fallback CID")
	}
}

func TestN115ExtractStatus(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"number", map[string]any{"data": map[string]any{"unzip_status": float64(1)}}, 1},
		{"string", map[string]any{"data": map[string]any{"unzip_status": "6"}}, 6},
		{"missing", map[string]any{"data": map[string]any{}}, -1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := n115ExtractStatus(test.body); got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}

func TestN115FileKeyChangesForDifferentFiles(t *testing.T) {
	first := n115FileKey("100", n115File{FID: "1", Size: 10})
	second := n115FileKey("100", n115File{FID: "2", Size: 10})
	if first == second {
		t.Fatal("different 115 files must not share a running-task key")
	}
}

func TestN115SeparateFolderName(t *testing.T) {
	if got := n115ExtractFolderName("sample.tar.gz"); got != "sample" {
		t.Fatalf("tar.gz folder = %q", got)
	}
	if got := n115ExtractFolderName("movie.7z"); got != "movie" {
		t.Fatalf("archive folder = %q", got)
	}
}

func TestAvailable115ExtractFolderNameAvoidsExistingFolders(t *testing.T) {
	if got := available115ExtractFolderName("电影", map[string]struct{}{}); got != "电影" {
		t.Fatalf("unused name changed: %q", got)
	}
	existing := map[string]struct{}{
		"电影":    {},
		"电影（1）": {},
		"电影（2）": {},
	}
	if got := available115ExtractFolderName("电影", existing); got != "电影（3）" {
		t.Fatalf("collision name = %q", got)
	}
}

func TestN115DateFolderCachePersistsAndExpiresByDate(t *testing.T) {
	path := filepath.Join(t.TempDir(), n115DateCacheFilename)
	initial := n115DateFolderCache{Date: "2026-09-28", Folders: map[string]string{"source-cid": "date-cid"}}
	if err := writeN115DateFolderCache(path, initial); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	loaded, err := readN115DateFolderCache(path, "2026-09-28")
	if err != nil {
		t.Fatalf("read same-day cache: %v", err)
	}
	if loaded.Folders["source-cid"] != "date-cid" {
		t.Fatalf("same-day CID = %q, want date-cid", loaded.Folders["source-cid"])
	}

	nextDay, err := readN115DateFolderCache(path, "2026-09-29")
	if err != nil {
		t.Fatalf("read next-day cache: %v", err)
	}
	if nextDay.Date != "2026-09-29" || len(nextDay.Folders) != 0 {
		t.Fatalf("next-day cache was not cleared: %#v", nextDay)
	}
	if err := writeN115DateFolderCache(path, nextDay); err != nil {
		t.Fatalf("replace cache on day rollover: %v", err)
	}
	cleared, err := readN115DateFolderCache(path, "2026-09-29")
	if err != nil || cleared.Date != "2026-09-29" || len(cleared.Folders) != 0 {
		t.Fatalf("persisted rollover cache = %#v, err=%v", cleared, err)
	}
}

func TestN115DateFolderCIDUsesSameDayCacheWithoutNetwork(t *testing.T) {
	u := New()
	u.n115DateCache = n115DateFolderCache{Date: "2026-09-28", Folders: map[string]string{"parent-cid": "date-cid"}}
	got, err := u.n115DateFolderCID(context.Background(), "parent-cid", "2026-09-28")
	if err != nil {
		t.Fatalf("cached date folder lookup failed: %v", err)
	}
	if got != "date-cid" {
		t.Fatalf("cached CID = %q, want date-cid", got)
	}
}

func TestN115SeparateExtractKeepsOnlySuccessfulOutput(t *testing.T) {
	for _, test := range []struct {
		status string
		err    error
		keep   bool
	}{
		{status: "success", keep: true},
		{status: "failed", keep: false},
		{status: "password", keep: false},
		{status: "", err: context.DeadlineExceeded, keep: false},
	} {
		keep := keep115ExtractOutput(test.status, test.err)
		if keep != test.keep {
			t.Fatalf("status=%q err=%v keep=%v, want %v", test.status, test.err, keep, test.keep)
		}
	}
}

func TestN115QueueHasSingleSlot(t *testing.T) {
	u := New()
	if cap(u.n115Queue) != 1 {
		t.Fatalf("115 queue capacity = %d, want 1", cap(u.n115Queue))
	}
	u.n115Queue <- struct{}{}
	select {
	case u.n115Queue <- struct{}{}:
		t.Fatal("a second 115 extraction must wait for the active extraction")
	default:
	}
	<-u.n115Queue
}

func TestN115ResponseSummaryIncludesUsefulFields(t *testing.T) {
	got := n115ResponseSummary([]byte(`{"state":false,"error":"未登录","errno":401}`))
	if !strings.Contains(got, "未登录") || !strings.Contains(got, "401") {
		t.Fatalf("unexpected error summary: %q", got)
	}
}

func TestN115ServiceFailureDoesNotArchiveFiles(t *testing.T) {
	for _, err := range []error{
		&n115APIError{Status: 401, Detail: "未登录"},
		&n115APIError{Status: 429, Detail: "请求频繁"},
		&n115APIError{Detail: "Cookie 已失效"},
	} {
		if !n115ServiceFailure(err) {
			t.Fatalf("service error was treated as an archive failure: %v", err)
		}
	}
	if n115ServiceFailure(&n115APIError{Detail: "压缩包格式不支持"}) {
		t.Fatal("archive format errors must follow the file failure workflow")
	}
}

func TestCloudDriveManualWatchPathsKeepsLegacyPath(t *testing.T) {
	paths := cloudDriveManualWatchPaths(CloudDriveConfig{
		WatchPath:        "/115open/日常下载",
		ManualWatchPaths: []string{"/115open/日常下载", "/115open/手动"},
	})
	if len(paths) != 2 || !cloudDrivePathMatches("/115open/手动/test.7z", paths) || cloudDrivePathMatches("/115open/失败/test.7z", paths) {
		t.Fatalf("unexpected manual paths: %#v", paths)
	}
}

func TestMigrate115CloudSettingsUsesSharedFailureFolder(t *testing.T) {
	cfg := CloudDriveConfig{N115Mappings: []string{
		"100 => 900 => /115open/失败",
		"200 => 900 => /115open/失败",
	}}
	migrate115CloudSettings(&cfg)
	if len(cfg.N115SourceCIDs) != 2 || cfg.N115FailureCID != "900" || cfg.N115FailureCD2Path != "/115open/失败" {
		t.Fatalf("unexpected migrated settings: %#v", cfg)
	}
}

func TestCloudDriveWatchPathsExcludeApprovalFolders(t *testing.T) {
	cfg := CloudDriveConfig{
		N115DownloadMappings: []string{
			"100 => /115open/自动 => auto",
			"200 => /115open/审批 => approval",
		},
		N115FailureCD2Path: "/115open/失败",
		N115AutoFallback:   false,
	}
	watch := cloudDriveManualWatchPaths(cfg)
	if !cloudDrivePathMatches("/115open/自动/a.7z", watch) {
		t.Fatal("automatic download folder must be monitored")
	}
	if cloudDrivePathMatches("/115open/审批/a.7z", watch) || cloudDrivePathMatches("/115open/失败/a.7z", watch) {
		t.Fatalf("approval folders must not be automatically monitored: %#v", watch)
	}
	refresh := cloudDriveConfiguredRefreshPaths(cfg)
	if !cloudDrivePathMatches("/115open/审批/a.7z", refresh) || !cloudDrivePathMatches("/115open/失败/a.7z", refresh) {
		t.Fatalf("approval folders still need local refresh support: %#v", refresh)
	}
}

func TestValidate115CloudSettingsRejectsOverlappingRoles(t *testing.T) {
	enabled := true
	settings := UIOverrides{
		N115Enabled:        &enabled,
		N115SourceCIDs:     []string{"100"},
		N115FailureCID:     "900",
		N115FailureCD2Path: "/115open/下载",
		N115Downloads:      []string{"200 => /115open/下载/手动 => auto"},
	}
	if err := validate115CloudSettings(settings); err == nil {
		t.Fatal("overlapping failure and daily-download paths must be rejected")
	}
	settings.N115Downloads = []string{"100 => /115open/手动 => auto"}
	settings.N115FailureCD2Path = "/115open/失败"
	if err := validate115CloudSettings(settings); err == nil {
		t.Fatal("one CID must not have two roles")
	}
}

func TestPending115FallbackSurvivesStateRoundTrip(t *testing.T) {
	u := New()
	u.ConfigFile = t.TempDir() + "/unpackerr.conf"
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	u.savePending115Fallback(Pending115{Key: "fallback-key", SourceCID: "200", FID: "300", FileName: "test.7z"})
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	item, ok := u.pending115Fallback("fallback-key")
	if !ok || item.SourceCID != "200" || item.FID != "300" {
		t.Fatalf("fallback state was not restored: %#v, %v", item, ok)
	}
}

func TestFallbackLocalSuccessUsesCloudSuccessAction(t *testing.T) {
	u := New()
	u.ConfigFile = filepath.Join(t.TempDir(), "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	u.CloudDrive2.N115SuccessAction = "archive"
	u.savePending115Fallback(Pending115{Key: "fallback-key", TaskKey: "task-key", SourceCID: "source-cid", FallbackCID: "failure-cid", FID: "file-id", FileName: "test.7z"})
	u.update115Transfer("task-key", "test.7z", "正在批准本地下载", func(task *CD2Transfer) { task.CanFallback = true })

	u.handle115FallbackLocalSuccess(PendingCD2{N115TaskKey: "task-key", N115SourceCID: "source-cid", N115FailureCID: "failure-cid", N115FID: "file-id", N115FileName: "test.7z"})

	u.state.mu.RLock()
	failed := u.state.Failed["task-key"]
	_, pending := u.state.Fallback115["fallback-key"]
	u.state.mu.RUnlock()
	if failed.CleanupKind != "115_success" || failed.SourceCID != "failure-cid" || failed.Error == "" {
		t.Fatalf("fallback cleanup did not use cloud success settings: %#v", failed)
	}
	if pending {
		t.Fatal("completed local fallback must remove its pending download record")
	}
	if _, visible := u.cd2Tasks.Load("task-key"); visible {
		t.Fatal("completed local fallback left a stale current task")
	}
}

func TestFallbackLocalSuccessMovesTaskToSuccessHistory(t *testing.T) {
	u := New()
	u.ConfigFile = filepath.Join(t.TempDir(), "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	u.CloudDrive2.N115SuccessAction = "keep"
	u.savePending115Fallback(Pending115{Key: "fallback-key", TaskKey: "task-key", SourceCID: "failure-cid", FallbackCID: "failure-cid", FID: "file-id", FileName: "test.7z"})
	u.update115Transfer("task-key", "test.7z", "正在批准本地下载", func(task *CD2Transfer) {
		task.CanFallback = true
		task.CanCloudRetry = true
	})

	u.handle115FallbackLocalSuccess(PendingCD2{N115TaskKey: "task-key", N115SourceCID: "failure-cid", N115FailureCID: "failure-cid", N115FID: "file-id", N115FileName: "test.7z"})

	if _, visible := u.cd2Tasks.Load("task-key"); visible {
		t.Fatal("successful fallback remained in current tasks")
	}
	u.state.mu.RLock()
	processed := u.state.Processed["task-key"]
	_, failed := u.state.Failed["task-key"]
	_, pending := u.state.Fallback115["fallback-key"]
	u.state.mu.RUnlock()
	if processed.Key == "" || failed || pending {
		t.Fatalf("fallback was not finalized as success: processed=%#v failed=%v pending=%v", processed, failed, pending)
	}
}

func TestCurrent115PendingPathUsesLatestMapping(t *testing.T) {
	u := New()
	u.CloudDrive2.N115DownloadMappings = []string{"300 => /115open/绿联备份/下载 => auto"}
	item := Pending115{Kind: "manual_download", SourceCID: "300", FallbackCID: "300", CD2Path: "/115open/上传下载/下载"}
	if got, ok := u.current115PendingPath(item); !ok || got != "/115open/绿联备份/下载" {
		t.Fatalf("pending path = %q, %v", got, ok)
	}
	item.SourceCID, item.FallbackCID = "old", "old"
	if _, ok := u.current115PendingPath(item); ok {
		t.Fatal("stale pending task unexpectedly matched current settings")
	}
}
