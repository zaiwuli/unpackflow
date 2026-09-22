package unpackerr

import (
	"context"
	"strings"
	"testing"
	"time"
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
	now := time.Date(2026, time.September, 22, 15, 45, 0, 0, time.Local)
	if got := available115ExtractFolderName("电影", map[string]struct{}{}, now); got != "电影" {
		t.Fatalf("unused name changed: %q", got)
	}
	existing := map[string]struct{}{
		"电影":                      {},
		"电影_解压_20260922-154500":   {},
		"电影_解压_20260922-154500_2": {},
	}
	if got := available115ExtractFolderName("电影", existing, now); got != "电影_解压_20260922-154500_3" {
		t.Fatalf("collision name = %q", got)
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
