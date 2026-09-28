package unpackerr

import (
	"path/filepath"
	"testing"
)

func TestIgnoreCloudTaskResolvesNameAndLegacyRecord(t *testing.T) {
	u := New()
	u.ConfigFile = filepath.Join(t.TempDir(), "unpackerr.conf")
	if err := u.loadProcessingState(); err != nil {
		t.Fatal(err)
	}
	key := "115|123|456|789"
	u.state.Fallback115["fallback"] = Pending115{Key: "fallback", TaskKey: key, FileName: "archive.7z", Size: 789}
	u.update115Transfer(key, "archive.7z", "等待批准本地下载", nil)
	// Reproduce the old API's persisted internal-ID record.
	legacy := ignoredIdentity(key, 0)
	u.state.Ignored[legacy] = ProcessedSource{Key: legacy, Path: key}
	if !u.isIgnoredPath("archive.7z") {
		t.Fatal("legacy ignore did not resolve archive name")
	}
	if got := u.dashboardTransfers(); len(got) != 0 {
		t.Fatalf("ignored transfer visible: %#v", got)
	}
	if err := u.setIgnoredPath(legacy, false); err != nil {
		t.Fatal(err)
	}
	if u.isIgnoredPath("archive.7z") {
		t.Fatal("unignore failed")
	}
	if err := u.setIgnoredPath(key, true); err != nil {
		t.Fatal(err)
	}
	for _, item := range u.state.Ignored {
		if item.Path != "archive.7z" {
			t.Fatalf("saved internal ID as name: %#v", item)
		}
	}
	if !u.isIgnoredPath(filepath.Join(t.TempDir(), "archive.7z")) {
		t.Fatal("cache stage not ignored")
	}
	for id := range u.state.Ignored {
		if err := u.handleHistoryAction(historyAction{Key: id, Action: "delete"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(u.state.Ignored) == 0 || !u.isIgnoredPath("archive.7z") {
		t.Fatal("history deletion removed ignore protection")
	}
}
