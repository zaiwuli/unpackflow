package unpackerr

import (
	"os"
	"path/filepath"
	"testing"
)

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
