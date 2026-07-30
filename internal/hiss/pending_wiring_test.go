package hiss_test

import (
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/hiss"
)

func TestNewRestartLossyHolds_WarnsThatHoldsStartEmpty(t *testing.T) {
	getLogs := captureLog(t)

	hiss.NewRestartLossyHoldsForTest()

	lines := getLogs()
	if len(lines) != 1 {
		t.Fatalf("want exactly one warn line, got %d: %+v", len(lines), lines)
	}
	msg, _ := lines[0]["msg"].(string)
	if lines[0]["level"] != "WARN" || !strings.Contains(msg, "rebuilt empty") {
		t.Errorf("want a WARN mentioning holds rebuilt empty, got %+v", lines[0])
	}
}
