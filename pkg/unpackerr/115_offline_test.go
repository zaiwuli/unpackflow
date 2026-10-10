package unpackerr

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
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
