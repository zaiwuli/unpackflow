package unpackerr

import (
	"path/filepath"
	"testing"
)

func TestDashboardCloudDetailsIncludeSourceAndActualOutput(t *testing.T) {
	u := lifecycleTestApp(t)
	u.CloudDrive2.N115ExtractCIDs = map[string]string{"source": "target"}
	u.update115Transfer("cloud", "archive.7z", "115 云端解压中", func(task *CD2Transfer) {
		task.Version = ProcessedSource{Source: "115", SourceCID: "source", CloudFile: &n115File{FID: "file"}}
		task.OutputCID, task.OutputName, task.Retries = "actual", "archive (1)", 2
	})
	snapshot := u.dashboardSnapshot()
	if len(snapshot.Tasks) != 1 {
		t.Fatalf("tasks: %#v", snapshot.Tasks)
	}
	task := snapshot.Tasks[0]
	if task.SourceCID != "source" || task.TargetCID != "target" || task.OutputCID != "actual" || task.FileID != "file" || task.Retries != 2 {
		t.Fatalf("incomplete cloud details: %#v", task)
	}
}

func TestDashboardCachedDetailsSurviveRestartMetadata(t *testing.T) {
	u := lifecycleTestApp(t)
	cache := filepath.Join(t.TempDir(), "archive.7z")
	source := filepath.Join(t.TempDir(), "archive.7z")
	u.CloudDrive2.CacheExtractPath = filepath.Join(t.TempDir(), "output")
	u.savePendingCD2(PendingCD2{Key: cache, CachedPrimary: cache, Files: []string{source}, N115TaskKey: "cloud", N115SourceCID: "cid", N115FID: "fid"})
	snapshot := u.dashboardSnapshot()
	if len(snapshot.Tasks) != 1 {
		t.Fatalf("tasks: %#v", snapshot.Tasks)
	}
	task := snapshot.Tasks[0]
	if task.Path != source || task.CachedPath != cache || task.OutputPath != u.CloudDrive2.CacheExtractPath || task.SourceCID != "cid" || task.FileID != "fid" || len(task.Files) != 1 {
		t.Fatalf("incomplete cached details: %#v", task)
	}
	if task.TargetCID != "" {
		t.Fatal("local download was mislabeled as cloud extraction")
	}
}

func TestDashboardMergePreservesPaths(t *testing.T) {
	current := DashboardTask{Key: "one", Status: "正在复制", Path: "/source/file.7z", CachedPath: "/cache/file.7z", OutputPath: "/output"}
	incoming := DashboardTask{Key: "one", Status: "正在解压"}
	for _, pair := range [][2]DashboardTask{{current, incoming}, {incoming, current}} {
		merged := mergeDashboardTask(pair[0], pair[1])
		if merged.Path != current.Path || merged.CachedPath != current.CachedPath || merged.OutputPath != current.OutputPath {
			t.Fatalf("lost metadata: %#v", merged)
		}
	}
}

func TestDashboardSnapshotIncludesLatest115Scan(t *testing.T) {
	u := New()
	u.n115ScanStateMu.Lock()
	u.n115LastResult = n115ScanResult{Folders: 2, Files: 240, Queued: 3, SkippedProcessed: 4, SkippedPending: 5, SkippedIgnored: 6, SkippedInvalid: 7, Partial: 1, ScannedAt: "2026-10-08T12:00:00+08:00", Duration: 1250}
	u.n115ScanStateMu.Unlock()

	scan := u.dashboardSnapshot().CloudDrive.N115Scan
	if scan.Folders != 2 || scan.Files != 240 || scan.Queued != 3 || scan.SkippedProcessed != 4 || scan.SkippedPending != 5 || scan.SkippedIgnored != 6 || scan.SkippedInvalid != 7 || scan.Partial != 1 || scan.ScannedAt == "" || scan.Duration != 1250 {
		t.Fatalf("unexpected 115 scan snapshot: %#v", scan)
	}
}
