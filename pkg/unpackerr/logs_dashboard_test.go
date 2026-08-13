package unpackerr

import (
	"fmt"
	"testing"
)

func TestDashboardLogsNewestFirstAndLimited(t *testing.T) {
	t.Parallel()

	logger := &Logger{}
	for idx := 0; idx < dashboardLogLimit+5; idx++ {
		logger.addDashboardLog("信息", fmt.Sprintf("日志-%03d", idx))
	}

	logs := logger.dashboardLogs()
	if len(logs) != dashboardLogLimit {
		t.Fatalf("expected %d logs, got %d", dashboardLogLimit, len(logs))
	}
	if logs[0].Message != "日志-204" {
		t.Fatalf("expected newest log first, got %q", logs[0].Message)
	}
	if logs[len(logs)-1].Message != "日志-005" {
		t.Fatalf("expected oldest retained log last, got %q", logs[len(logs)-1].Message)
	}
}
