package unpackerr

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestOfflineRetryRejectsNonFailedTask(t *testing.T) {
	u := New()
	u.ConfigFile = filepath.Join(t.TempDir(), "unpackerr.conf")
	if err := u.load115OfflineStore(); err != nil { t.Fatal(err) }
	u.offlineStore.Batches["batch"] = &n115OfflineBatch{ID: "batch", Tasks: []*n115OfflineTask{{ID: "task", Status: "success"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/115/offline/retry", bytes.NewBufferString(`{"batch_id":"batch","task_id":"task"}`))
	recorder := httptest.NewRecorder()
	u.n115OfflineRetryAPI(recorder, request, nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected conflict for successful task, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestLegacyNotificationEnablesOfflineByDefault(t *testing.T) {
	settings := UINotification{Events: &UINotificationEvents{Complete: true}}
	if !notificationStageEnabled(settings, notifyOffline) {
		t.Fatal("legacy notification configuration disabled offline notifications")
	}
	disabled := false
	settings.Events.Offline = &disabled
	if notificationStageEnabled(settings, notifyOffline) {
		t.Fatal("explicitly disabled offline notification was ignored")
	}
	if !notificationStageEnabled(UINotification{Events: &UINotificationEvents{}}, notifyCloud115) {
		t.Fatal("legacy notification configuration disabled 115 cloud notifications")
	}
}

func TestSuccessfulOfflineTaskWaitsForCloudHandoff(t *testing.T) {
	batch := &n115OfflineBatch{Tasks: []*n115OfflineTask{{Status: "success"}}}
	if !offlineBatchNeedsCheck(batch) { t.Fatal("successful task was not retained for cloud handoff") }
	batch.Tasks[0].ExtractedAt = time.Now()
	if offlineBatchNeedsCheck(batch) { t.Fatal("handed-off successful task still requested polling") }
}

func TestOfflineTXTFileFilter(t *testing.T) {
	for _, name := range []string{"magnets.txt", "MAGNETS.TXT"} {
		if !isOfflineTXTFile(name) { t.Fatalf("expected TXT file: %s", name) }
	}
	for _, name := range []string{".hidden.txt", "~writing.txt", "video.mkv"} {
		if isOfflineTXTFile(name) { t.Fatalf("unexpected TXT candidate: %s", name) }
	}
}

func TestOfflineTXTFolderIsSiblingOfArchiveWatch(t *testing.T) {
	root := t.TempDir()
	u := New()
	u.Folders = []*FolderConfig{{Path: filepath.Join(root, "压缩包监控")}}
	want := filepath.Join(root, offlineTXTFolderName)
	if got := u.offlineTXTFolder(); got != want {
		t.Fatalf("TXT folder mixed into archive watch directory: got %q want %q", got, want)
	}
}

func TestOfflineMappingUsesParentRuleAndActualSource(t *testing.T) {
	cfg := CloudDriveConfig{N115OfflineCID: "monitor-parent", N115FailureCID: "failed", N115FailureCD2Path: "/failed"}
	mapping := n115OfflineMapping(cfg, "dated-child")
	if mapping.SourceCID != "dated-child" {
		t.Fatalf("source CID = %q, want dated child", mapping.SourceCID)
	}
	if mapping.ruleCID() != "monitor-parent" {
		t.Fatalf("rule CID = %q, want monitor parent", mapping.ruleCID())
	}
	if mapping.FallbackCID != "failed" || mapping.CD2Path != "/failed" {
		t.Fatalf("offline mapping did not inherit shared fallback: %#v", mapping)
	}
}

func TestOfflineCIDMustUseMonitoredSourceRule(t *testing.T) {
	enabled := true
	settings := UIOverrides{
		N115Enabled:        &enabled,
		N115SourceCIDs:     []string{"monitor-a"},
		N115OfflineCID:     "other-folder",
		N115FailureCID:     "failed",
		N115FailureCD2Path: "/failed",
	}
	if err := validate115CloudSettings(settings); err == nil {
		t.Fatal("offline CID outside monitored sources must be rejected")
	}
	settings.N115OfflineCID = "monitor-a"
	if err := validate115CloudSettings(settings); err != nil {
		t.Fatalf("shared offline/source rule was rejected: %v", err)
	}
}

func TestParseOfflineLinksMixedTextAndDeduplicates(t *testing.T) {
	text := "说明 ed2k://|file|A.zip|123|ABCDEF|/\n" +
		"ed2k://|file|A-copy.zip|123|abcdef|/ magnet:?xt=urn:btih:112233&dn=B.zip"
	links := parseOfflineLinks(text)
	if len(links) != 2 {
		t.Fatalf("expected two unique links, got %d: %#v", len(links), links)
	}
	if offlineLinkName(links[0]) != "A.zip" {
		t.Fatalf("unexpected ed2k name: %q", offlineLinkName(links[0]))
	}
}

func TestOfflineStringValueNeverReturnsNilMarker(t *testing.T) {
	item := map[string]any{"info_hash": nil, "hash": "ABC123"}
	if got := offlineStringValue(item, "info_hash", "hash"); got != "ABC123" {
		t.Fatalf("fallback hash = %q, want ABC123", got)
	}
	if got := offlineStringValue(map[string]any{"info_hash": nil}, "info_hash"); got != "" {
		t.Fatalf("nil marker leaked as %q", got)
	}
}

func TestOfflineLinkHash(t *testing.T) {
	if got := offlineLinkHash("ed2k://|file|0.zip|123|ABCDEF|/"); got != "abcdef" {
		t.Fatalf("ed2k hash = %q, want abcdef", got)
	}
	if got := offlineLinkHash("magnet:?xt=urn:btih:1122AABB&dn=0.zip"); got != "1122aabb" {
		t.Fatalf("magnet hash = %q, want 1122aabb", got)
	}
}

func TestOfflineLinkSize(t *testing.T) {
	if got := offlineLinkSize("ed2k://|file|0.zip|123456|ABCDEF|/"); got != 123456 {
		t.Fatalf("ed2k size = %d, want 123456", got)
	}
	if got := offlineLinkSize("magnet:?xt=urn:btih:1122AABB"); got != 0 {
		t.Fatalf("magnet size = %d, want 0", got)
	}
}

func TestBindOfflineArchiveUsesExactNameAndSize(t *testing.T) {
	u := New()
	u.ConfigFile = filepath.Join(t.TempDir(), "unpackerr.conf")
	if err := u.load115OfflineStore(); err != nil { t.Fatal(err) }
	u.offlineStore.Batches["batch"] = &n115OfflineBatch{ID: "batch", Tasks: []*n115OfflineTask{
		{ID: "first", Name: "0.zip", Size: 123, Status: "success"},
		{ID: "wrong-size", Name: "1.zip", Size: 456, Status: "success"},
	}}
	u.bind115OfflineArchive("batch", n115File{FID: "fid", CID: "cid", Name: "0.zip", Size: 123}, "115|cid|fid|123")
	first := u.offlineStore.Batches["batch"].Tasks[0]
	if first.ArchiveTaskKey != "115|cid|fid|123" || first.ArchiveFID != "fid" || first.ExtractedAt.IsZero() {
		t.Fatalf("archive association missing: %#v", first)
	}
	second := u.offlineStore.Batches["batch"].Tasks[1]
	if second.ArchiveTaskKey != "" || !second.ExtractedAt.IsZero() {
		t.Fatalf("wrong archive was associated: %#v", second)
	}
}

func TestOfflineStatusesIndexSuccessfulTaskByHashLinkAndName(t *testing.T) {
	link := "ed2k://|file|0.zip|123|ABCDEF|/"
	statuses := offlineStatuses(map[string]any{"data": map[string]any{"tasks": []any{map[string]any{
		"infoHash": "ABC123", "url": link, "name": "0.zip", "status": float64(2), "error_msg": nil,
	}}}})
	for label, found := range map[string]bool{
		"hash": statuses.byHash["abc123"].Status == "success",
		"link": statuses.byLink[offlineLinkIdentity(link)].Status == "success",
		"name": statuses.byName["0.zip"].Status == "success",
	} {
		if !found { t.Fatalf("successful offline task not indexed by %s", label) }
	}
}
