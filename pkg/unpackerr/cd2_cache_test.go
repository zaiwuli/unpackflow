package unpackerr

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyStableFileResumesPartialFile(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("resume-data-"), 10000)
	source, target := filepath.Join(dir, "source.7z"), filepath.Join(dir, "target.7z")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content[:len(content)/3], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyStableFileContext(context.Background(), source, target, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("resumed target does not match source")
	}
	sourceInfo, _ := os.Stat(source)
	targetInfo, _ := os.Stat(target)
	if !sourceInfo.ModTime().Equal(targetInfo.ModTime()) {
		t.Fatal("target modification time was not preserved")
	}
}

func TestCopyStableFileRestartsMismatchedPartialFile(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("correct-data-"), 5000)
	source, target := filepath.Join(dir, "source.7z"), filepath.Join(dir, "target.7z")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, bytes.Repeat([]byte("wrong"), 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyStableFileContext(context.Background(), source, target, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("mismatched target was not replaced")
	}
}

func TestCopyStableFileHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "source.7z"), filepath.Join(dir, "target.7z")
	if err := os.WriteFile(source, bytes.Repeat([]byte("x"), 2*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyStableFileContext(ctx, source, target, nil); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestArchiveVolumeGroupRAR(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"release.part1.rar", "release.part2.rar", "release.part3.rar"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, _, err := archiveVolumeGroup(filepath.Join(dir, "release.part2.rar"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("wanted 3 RAR volumes, got %d: %v", len(files), files)
	}
}

func TestArchiveVolumeGroupOldRAR(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"release.rar", "release.r00", "release.r01"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, _, err := archiveVolumeGroup(filepath.Join(dir, "release.r00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("wanted 3 old RAR volumes, got %d: %v", len(files), files)
	}
}

func TestArchiveVolumeGroup7Zip(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"release.7z.001", "release.7z.002", "release.7z.003"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, _, err := archiveVolumeGroup(filepath.Join(dir, "release.7z.002"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("wanted 3 7z volumes, got %d: %v", len(files), files)
	}
}

func TestArchiveVolumeGroupRejectsMissing7ZipVolume(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"release.7z.001", "release.7z.003"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := archiveVolumeGroup(filepath.Join(dir, "release.7z.003")); err == nil {
		t.Fatal("expected missing volume error")
	}
}

func TestArchiveVolumeGroupRejectsMissingRARPart(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"release.part1.rar", "release.part3.rar"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := archiveVolumeGroup(filepath.Join(dir, "release.part3.rar")); err == nil {
		t.Fatal("expected missing volume error")
	}
}

func TestCacheCloudDriveGroup(t *testing.T) {
	sourceDir, cacheDir := t.TempDir(), t.TempDir()
	source := filepath.Join(sourceDir, "sample.zip")
	if err := os.WriteFile(source, []byte("archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	u := New()
	u.CloudDrive2.CacheDir = cacheDir
	u.folders = &Folders{Config: []*FolderConfig{{Path: cacheDir}}, Events: make(chan *eventData, 1), Folders: map[string]*Folder{}}
	if err := u.cacheCloudDriveGroup([]string{source}, source); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	foundArchive := false
	for _, entry := range entries {
		if entry.Name() == "sample.zip" && !entry.IsDir() {
			foundArchive = true
		}
	}
	if !foundArchive {
		t.Fatalf("cache file was not created directly in cache root: %v", entries)
	}
	select {
	case event := <-u.folders.Events:
		if filepath.Ext(event.file) != ".zip" {
			t.Fatalf("unexpected injected file: %s", event.file)
		}
	default:
		t.Fatal("expected cache-ready event")
	}
}

func TestCacheStagingRootStaysInsideCacheMount(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	root := cacheStagingRoot(cacheDir)
	if !sameOrChild(root, cacheDir) {
		t.Fatalf("staging root escaped cache mount: %s", root)
	}
}

func TestPromoteCachedFile(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "staged.7z")
	target := filepath.Join(dir, "final.7z")
	if err := os.WriteFile(staged, []byte("new archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := promoteCachedFile(staged, target); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new archive" {
		t.Fatalf("unexpected promoted content: %q", content)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged file still exists: %v", err)
	}
}

func TestDeleteCachedSourceUsesProvidedGroupAfterPendingRemoval(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "release.part1.rar")
	second := filepath.Join(dir, "release.part2.rar")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	u := New()
	u.CloudDrive2.DeleteSource = true
	u.cd2Cache.Store(filepath.Join(dir, "cached.part1.rar"), []string{first, second})
	// Simulate the normal success path, where the pending state has already
	// been removed before the asynchronous source cleanup runs.
	u.deleteCachedSource(filepath.Join(dir, "cached.part1.rar"), []string{first, second})
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("source %s still exists: %v", path, err)
		}
	}
}

func TestPendingCD2ForFilesReturnsMatchingRetry(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "release.7z.001")
	second := filepath.Join(dir, "release.7z.002")
	u := New()
	u.state = &ProcessingState{
		Processed: map[string]ProcessedSource{},
		Pending: map[string]PendingCD2{
			"copy|release": {Key: "copy|release", Files: []string{first, second}, NextAttempt: time.Now().Add(time.Minute)},
		},
	}
	pending, ok := u.pendingCD2ForFiles([]string{second, first})
	if !ok || pending.Key != "copy|release" {
		t.Fatalf("matching retry not found: %#v, %v", pending, ok)
	}
}

func TestCD2TransferAppearsInUnifiedTaskListImmediately(t *testing.T) {
	u := New()
	mapped := filepath.Join(t.TempDir(), "new-cloud.7z")
	key := u.beginCD2EventTask([]string{mapped}, "/115open/new-cloud.7z")
	if key == "" {
		t.Fatal("CD2 event did not create a task")
	}
	snapshot := u.dashboardSnapshot()
	if len(snapshot.Tasks) != 1 {
		t.Fatalf("expected one unified task, got %#v", snapshot.Tasks)
	}
	if snapshot.Tasks[0].Name != "new-cloud.7z" || snapshot.Tasks[0].Status != "等待文件可见" || snapshot.Tasks[0].Source != "CloudDrive2" {
		t.Fatalf("unexpected CD2 task: %#v", snapshot.Tasks[0])
	}
}

func TestIncompleteCD2VolumeRemainsVisible(t *testing.T) {
	dir := t.TempDir()
	third := filepath.Join(dir, "release.7z.003")
	if err := os.WriteFile(third, []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}
	u := New()
	if submitted := u.cacheCloudDrivePaths([]string{third}); submitted != 0 {
		t.Fatalf("incomplete volume unexpectedly submitted: %d", submitted)
	}
	key := cloudDriveTaskKey(third)
	value, exists := u.cd2Tasks.Load(key)
	if !exists {
		t.Fatal("incomplete CD2 volume disappeared from the task list")
	}
	transfer, ok := value.(*CD2Transfer)
	if !ok || transfer == nil || transfer.Error == "" {
		t.Fatalf("incomplete CD2 task has no actionable error: %#v", value)
	}
}

func TestCD2TransferClearsWhenCachedTaskQueues(t *testing.T) {
	u := New()
	cached := filepath.Join(t.TempDir(), "queued.zip")
	key := cloudDriveTaskKey(cached)
	u.updateCD2Transfer(key, cached, "排队中", func(transfer *CD2Transfer) {
		transfer.CachedPath = cached
	})
	u.clearCD2TransferForCachedPath(cached)
	if _, exists := u.cd2Tasks.Load(key); exists {
		t.Fatal("copy phase remained after extraction task entered the queue")
	}
}
