package thump_test

import (
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/thump"
)

func TestBuildNotifier_UnsetWarnsThatHeldActionsPageNobody(t *testing.T) {
	getLogs := captureLog(t)

	n := thump.BuildNotifierForTest(config.Thump{}, func(string) thump.Notifier { return nil })

	if n != nil {
		t.Fatalf("want nil Notifier when SLACK_WEBHOOK_URL is unset, got %T", n)
	}
	lines := getLogs()
	if len(lines) != 1 {
		t.Fatalf("want exactly one warn line, got %d: %+v", len(lines), lines)
	}
	msg, _ := lines[0]["msg"].(string)
	if lines[0]["level"] != "WARN" || !strings.Contains(msg, "page nobody") {
		t.Errorf("want a WARN mentioning held actions paging nobody, got %+v", lines[0])
	}
}
