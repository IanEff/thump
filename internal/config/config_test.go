package config_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/leaftest"
)

// setClankEnv sets every var LoadClank reads, restored by t.Setenv's own
// cleanup — a common baseline the missing/valid/optional cases each mutate.
func setClankEnv(t *testing.T) {
	t.Helper()
	for name, val := range map[string]string{
		"ANTHROPIC_API_KEY":  "test-key",
		"ACTION_CATALOG":     "/etc/actions/catalog.yaml",
		"PROM_URL":           "http://prom:9090",
		"EVIDENCE_QUERIES":   "/etc/evidence-queries.yaml",
		"LOKI_URL":           "http://loki:3100",
		"WHIR_CATALOG":       "/etc/catalog-info.yaml",
		"WHIR_STATE_QUERIES": "/etc/state-queries.yaml",
		"CLANK_TRANSCRIPTS":  "/var/run/transcripts",
		"CLANK_INBOX":        "/var/run/inbox",
		"CLANK_OUTBOX":       "/var/run/outbox",
		"CLANK_OUTCOMES":     "/var/run/outcomes",
	} {
		t.Setenv(name, val)
	}
}

func TestLoadClank_MissingRequired_ReportsAllAtOnce(t *testing.T) {
	setClankEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ACTION_CATALOG", "")
	t.Setenv("CLANK_INBOX", "") // offline-mode-required, and offline is the case under test

	_, err := config.LoadClank(false /* broker */)
	if err == nil {
		t.Fatal("LoadClank: want an error, got nil")
	}
	for _, want := range []string{"ANTHROPIC_API_KEY", "ACTION_CATALOG", "CLANK_INBOX"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadClank error %q does not mention %s — missing vars must be reported together, not one redeploy at a time", err, want)
		}
	}
}

func TestLoadClank_Valid_PopulatesStruct(t *testing.T) {
	setClankEnv(t)

	got, err := config.LoadClank(false /* broker */)
	if err != nil {
		t.Fatalf("LoadClank: %v", err)
	}
	want := config.Clank{
		AnthropicAPIKey:  "test-key",
		ActionCatalog:    "/etc/actions/catalog.yaml",
		PromURL:          "http://prom:9090",
		EvidenceQueries:  "/etc/evidence-queries.yaml",
		LokiURL:          "http://loki:3100",
		WhirCatalog:      "/etc/catalog-info.yaml",
		WhirStateQueries: "/etc/state-queries.yaml",
		Transcripts:      "/var/run/transcripts",
		Inbox:            "/var/run/inbox",
		Outbox:           "/var/run/outbox",
		Outcomes:         "/var/run/outcomes",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadClank (-want +got):\n%s", diff)
	}
}

func TestLoadClank_OptionalDefaults(t *testing.T) {
	// Only what's unconditionally required: the API key, plus the offline
	// trio since broker=false makes those required too.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ACTION_CATALOG", "/etc/actions/catalog.yaml")
	t.Setenv("CLANK_INBOX", "/var/run/inbox")
	t.Setenv("CLANK_OUTBOX", "/var/run/outbox")
	t.Setenv("CLANK_OUTCOMES", "/var/run/outcomes")
	for _, name := range []string{"PROM_URL", "EVIDENCE_QUERIES", "LOKI_URL", "WHIR_CATALOG", "WHIR_STATE_QUERIES", "CLANK_TRANSCRIPTS"} {
		t.Setenv(name, "")
	}

	got, err := config.LoadClank(false /* broker */)
	if err != nil {
		t.Fatalf("LoadClank: %v", err)
	}
	want := config.Clank{
		AnthropicAPIKey: "test-key",
		ActionCatalog:   "/etc/actions/catalog.yaml",
		Inbox:           "/var/run/inbox",
		Outbox:          "/var/run/outbox",
		Outcomes:        "/var/run/outcomes",
		// PromURL, EvidenceQueries, LokiURL, WhirCatalog, WhirStateQueries,
		// Transcripts all default to "" — genuinely optional, documented by
		// their zero value rather than a scattered `if x == ""` at call sites.
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadClank (-want +got):\n%s", diff)
	}
}

func TestLoadClank_BrokerMode_OfflineTrioNotRequired(t *testing.T) {
	// broker=true is clank's NATS path (lc.NATSURL != "") — CLANK_INBOX/
	// OUTBOX/OUTCOMES are the offline dir-poll fallback's vars and must not
	// be demanded when the broker path is what's actually going to run.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ACTION_CATALOG", "/etc/actions/catalog.yaml")
	for _, name := range []string{"CLANK_INBOX", "CLANK_OUTBOX", "CLANK_OUTCOMES"} {
		t.Setenv(name, "")
	}

	if _, err := config.LoadClank(true /* broker */); err != nil {
		t.Errorf("LoadClank(broker=true): want no error with the offline trio unset, got %v", err)
	}
}

// setRattleEnv sets every var LoadRattle reads in broker mode (WAL_DIR is
// the one field that's required only when broker=true — see setRattleEnv's
// callers) plus RATTLE_WATCH, the new C2 knob replacing the compiled-in
// watch list.
func setRattleEnv(t *testing.T) {
	t.Helper()
	for name, val := range map[string]string{
		"PROM_URL":           "http://prom:9090",
		"WHIR_CATALOG":       "/etc/catalog-info.yaml",
		"WHIR_STATE_QUERIES": "/etc/state-queries.yaml",
		"RATTLE_TRAFFIC":     "/etc/traffic-queries.yaml",
		"RATTLE_OUTBOX":      "/var/run/outbox",
		"RATTLE_WATCH":       "/etc/watch.yaml",
	} {
		t.Setenv(name, val)
	}
}

func TestLoadRattle_MissingRequired_ReportsAllAtOnce(t *testing.T) {
	setRattleEnv(t)
	t.Setenv("PROM_URL", "")
	t.Setenv("RATTLE_WATCH", "")

	_, err := config.LoadRattle(false /* broker */)
	if err == nil {
		t.Fatal("LoadRattle: want an error, got nil")
	}
	for _, want := range []string{"PROM_URL", "RATTLE_WATCH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadRattle error %q does not mention %s — missing vars must be reported together, not one redeploy at a time", err, want)
		}
	}
}

func TestLoadRattle_Valid_PopulatesStruct(t *testing.T) {
	setRattleEnv(t)

	got, err := config.LoadRattle(false /* broker */)
	if err != nil {
		t.Fatalf("LoadRattle: %v", err)
	}
	want := config.Rattle{
		PromURL:          "http://prom:9090",
		WhirCatalog:      "/etc/catalog-info.yaml",
		WhirStateQueries: "/etc/state-queries.yaml",
		Traffic:          "/etc/traffic-queries.yaml",
		Outbox:           "/var/run/outbox",
		WatchPath:        "/etc/watch.yaml",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadRattle (-want +got):\n%s", diff)
	}
}

func TestLoadRattle_OptionalDefaults(t *testing.T) {
	// Only what's unconditionally required: PROM_URL and RATTLE_WATCH.
	t.Setenv("PROM_URL", "http://prom:9090")
	t.Setenv("RATTLE_WATCH", "/etc/watch.yaml")
	for _, name := range []string{"WHIR_CATALOG", "WHIR_STATE_QUERIES", "RATTLE_TRAFFIC", "RATTLE_OUTBOX"} {
		t.Setenv(name, "")
	}

	got, err := config.LoadRattle(false /* broker */)
	if err != nil {
		t.Fatalf("LoadRattle: %v", err)
	}
	want := config.Rattle{
		PromURL:   "http://prom:9090",
		WatchPath: "/etc/watch.yaml",
		// WhirCatalog, WhirStateQueries, Traffic, Outbox all default to ""
		// — genuinely optional, documented by their zero value.
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadRattle (-want +got):\n%s", diff)
	}
}

func setThumpEnv(t *testing.T) {
	t.Helper()
	for name, val := range map[string]string{
		"ACTION_CATALOG": "/etc/actions/catalog.yaml",
		"THUMP_INBOX":    "/var/run/inbox",
		"THUMP_OUTBOX":   "/var/run/outbox",
	} {
		t.Setenv(name, val)
	}
}

func TestLoadThump_MissingRequired_ReportsAllAtOnce(t *testing.T) {
	setThumpEnv(t)
	t.Setenv("ACTION_CATALOG", "")
	t.Setenv("THUMP_INBOX", "")

	_, err := config.LoadThump(false /* broker */)
	if err == nil {
		t.Fatal("LoadThump: want an error, got nil")
	}
	for _, want := range []string{"ACTION_CATALOG", "THUMP_INBOX"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadThump error %q does not mention %s — missing vars must be reported together, not one redeploy at a time", err, want)
		}
	}
}

func TestLoadThump_Valid_PopulatesStruct(t *testing.T) {
	setThumpEnv(t)

	got, err := config.LoadThump(false /* broker */)
	if err != nil {
		t.Fatalf("LoadThump: %v", err)
	}
	want := config.Thump{
		ActionCatalog: "/etc/actions/catalog.yaml",
		Inbox:         "/var/run/inbox",
		Outbox:        "/var/run/outbox",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadThump (-want +got):\n%s", diff)
	}
}

func TestLoadThump_BrokerMode_OfflinePairNotRequired(t *testing.T) {
	// broker=true is thump's NATS path — THUMP_INBOX/OUTBOX are the offline
	// dir-poll fallback's vars and must not be demanded when the broker path
	// is what's actually going to run. ACTION_CATALOG is required either way.
	t.Setenv("ACTION_CATALOG", "/etc/actions/catalog.yaml")
	t.Setenv("WAL_DIR", "/var/run/wal")
	t.Setenv("S3_ENDPOINT", "http://minio:9000")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	for _, name := range []string{"THUMP_INBOX", "THUMP_OUTBOX"} {
		t.Setenv(name, "")
	}

	if _, err := config.LoadThump(true /* broker */); err != nil {
		t.Errorf("LoadThump(broker=true): want no error with the offline pair unset, got %v", err)
	}
}

func TestConfigIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "errors", "fmt", "os")
}
