package unpackerr

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
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
	if len(entries) != 1 || entries[0].Name() != "sample.zip" {
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
