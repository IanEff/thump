package config_test

import (
	"strings"
	"testing"
	"time"

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
		"FAILURE_CLASSES":    "/etc/actions/failure-classes.yaml",
		"PROM_URL":           "http://prom:9090",
		"EVIDENCE_QUERIES":   "/etc/evidence-queries.yaml",
		"LOKI_URL":           "http://loki:3100",
		"WHIR_CATALOG":       "/etc/catalog-info.yaml",
		"WHIR_STATE_QUERIES": "/etc/state-queries.yaml",
		"CLANK_TRANSCRIPTS":  "/var/run/transcripts",
		"CLANK_INBOX":        "/var/run/inbox",
		"CLANK_OUTBOX":       "/var/run/outbox",
		"CLANK_OUTCOMES":     "/var/run/outcomes",
		"CLANK_DECLINES":     "/var/run/declines",
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
		FailureClasses:   "/etc/actions/failure-classes.yaml",
		DedupeWindow:     time.Hour,
		PromURL:          "http://prom:9090",
		EvidenceQueries:  "/etc/evidence-queries.yaml",
		LokiURL:          "http://loki:3100",
		WhirCatalog:      "/etc/catalog-info.yaml",
		WhirStateQueries: "/etc/state-queries.yaml",
		Transcripts:      "/var/run/transcripts",
		Inbox:            "/var/run/inbox",
		Outbox:           "/var/run/outbox",
		Outcomes:         "/var/run/outcomes",
		Declines:         "/var/run/declines",
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
	t.Setenv("FAILURE_CLASSES", "/etc/actions/failure-classes.yaml")
	t.Setenv("CLANK_INBOX", "/var/run/inbox")
	t.Setenv("CLANK_OUTBOX", "/var/run/outbox")
	t.Setenv("CLANK_OUTCOMES", "/var/run/outcomes")
	t.Setenv("CLANK_DECLINES", "/var/run/declines")
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
		FailureClasses:  "/etc/actions/failure-classes.yaml",
		DedupeWindow:    time.Hour,
		Inbox:           "/var/run/inbox",
		Outbox:          "/var/run/outbox",
		Outcomes:        "/var/run/outcomes",
		Declines:        "/var/run/declines",
		// PromURL, EvidenceQueries, LokiURL, WhirCatalog, WhirStateQueries,
		// Transcripts all default to "" — genuinely optional, documented by
		// their zero value rather than a scattered `if x == ""` at call sites.
		// DedupeWindow's default isn't "", so it's asserted explicitly above
		// rather than by omission like the string fields.
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
	t.Setenv("FAILURE_CLASSES", "/etc/actions/failure-classes.yaml")
	t.Setenv("WAL_DIR", "/var/run/wal")
	t.Setenv("S3_ENDPOINT", "http://minio:9000")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	for _, name := range []string{"CLANK_INBOX", "CLANK_OUTBOX", "CLANK_OUTCOMES", "CLANK_DECLINES"} {
		t.Setenv(name, "")
	}

	if _, err := config.LoadClank(true /* broker */); err != nil {
		t.Errorf("LoadClank(broker=true): want no error with the offline trio unset, got %v", err)
	}
}

func TestLoadClank_BrokerMode_RequiresWALAndS3(t *testing.T) {
	// The offline trio (CLANK_INBOX/OUTBOX/OUTCOMES) is optional in broker
	// mode, but WAL_DIR and the S3 fields flip the other way — unset in
	// offline mode, required once the broker path is what's actually going
	// to run (it's what beat.RunShipper ships the proposals WAL through).
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ACTION_CATALOG", "/etc/actions/catalog.yaml")
	t.Setenv("FAILURE_CLASSES", "/etc/actions/failure-classes.yaml")
	for _, name := range []string{"WAL_DIR", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		t.Setenv(name, "")
	}

	_, err := config.LoadClank(true /* broker */)
	if err == nil {
		t.Fatal("LoadClank(broker=true): want an error, got nil")
	}
	for _, want := range []string{"WAL_DIR", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadClank error %q does not mention %s", err, want)
		}
	}
}

// setHissEnv sets every var LoadHiss reads in the offline path (broker=false)
// — HISS_POLICY plus the offline HISS_INBOX/HISS_OUTBOX pair. WAL_DIR and
// the S3 fields are broker-only and not set here; see
// TestLoadHiss_BrokerMode_OfflinePairNotRequired for those.
func setHissEnv(t *testing.T) {
	t.Helper()
	for name, val := range map[string]string{
		"HISS_POLICY": "/etc/policy.yaml",
		"HISS_INBOX":  "/var/run/inbox",
		"HISS_OUTBOX": "/var/run/outbox",
	} {
		t.Setenv(name, val)
	}
}

func TestLoadHiss_MissingRequired_ReportsAllAtOnce(t *testing.T) {
	setHissEnv(t)
	t.Setenv("HISS_POLICY", "")
	t.Setenv("HISS_INBOX", "")

	_, err := config.LoadHiss(false /* broker */)
	if err == nil {
		t.Fatal("LoadHiss: want an error, got nil")
	}
	for _, want := range []string{"HISS_POLICY", "HISS_INBOX"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadHiss error %q does not mention %s — missing vars must be reported together, not one redeploy at a time", err, want)
		}
	}
}

func TestLoadHiss_Valid_PopulatesStruct(t *testing.T) {
	setHissEnv(t)

	got, err := config.LoadHiss(false /* broker */)
	if err != nil {
		t.Fatalf("LoadHiss: %v", err)
	}
	want := config.Hiss{
		Policy: "/etc/policy.yaml",
		Inbox:  "/var/run/inbox",
		Outbox: "/var/run/outbox",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadHiss (-want +got):\n%s", diff)
	}
}

func TestLoadHiss_BrokerMode_OfflinePairNotRequired(t *testing.T) {
	// broker=true is hiss's NATS path — HISS_INBOX/OUTBOX are the offline
	// dir-poll fallback's vars and must not be demanded when the broker path
	// is what's actually going to run. WAL_DIR and the S3 fields flip the
	// other way: unset offline, required here.
	t.Setenv("HISS_POLICY", "/etc/policy.yaml")
	t.Setenv("WAL_DIR", "/var/run/wal")
	t.Setenv("S3_ENDPOINT", "http://minio:9000")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	for _, name := range []string{"HISS_INBOX", "HISS_OUTBOX"} {
		t.Setenv(name, "")
	}

	if _, err := config.LoadHiss(true /* broker */); err != nil {
		t.Errorf("LoadHiss(broker=true): want no error with the offline pair unset, got %v", err)
	}
}

func TestLoadHiss_BrokerMode_RequiresWALAndS3(t *testing.T) {
	t.Setenv("HISS_POLICY", "/etc/policy.yaml")
	for _, name := range []string{"WAL_DIR", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		t.Setenv(name, "")
	}

	_, err := config.LoadHiss(true /* broker */)
	if err == nil {
		t.Fatal("LoadHiss(broker=true): want an error, got nil")
	}
	for _, want := range []string{"WAL_DIR", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadHiss error %q does not mention %s", err, want)
		}
	}
}

// setRattleEnv sets every var LoadRattle reads in the offline path
// (broker=false) plus RATTLE_WATCH, the C2 knob replacing the compiled-in
// watch list. WAL_DIR and the S3 fields are broker-only and not set here;
// see TestLoadRattle_BrokerMode_RequiresWALAndS3 for those.
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

func TestLoadRattle_BrokerMode_RequiresWALAndS3(t *testing.T) {
	// WAL_DIR and the S3 fields are unset (and unread) offline — the three
	// existing LoadRattle tests above all run broker=false — but flip to
	// required once the broker path is what's actually going to run.
	setRattleEnv(t)
	for _, name := range []string{"WAL_DIR", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		t.Setenv(name, "")
	}

	_, err := config.LoadRattle(true /* broker */)
	if err == nil {
		t.Fatal("LoadRattle(broker=true): want an error, got nil")
	}
	for _, want := range []string{"WAL_DIR", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadRattle error %q does not mention %s", err, want)
		}
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
	for _, name := range []string{"THUMP_EXECUTOR", "THUMP_KILLSWITCH", "PROM_URL", "EVIDENCE_QUERIES"} {
		t.Setenv(name, "")
	}

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

func TestLoadThump_SlackWebhookURLOptional(t *testing.T) {
	setThumpEnv(t)
	for _, name := range []string{"THUMP_EXECUTOR", "THUMP_KILLSWITCH", "PROM_URL", "EVIDENCE_QUERIES", "SLACK_WEBHOOK_URL"} {
		t.Setenv(name, "")
	}

	got, err := config.LoadThump(false /* broker */)
	if err != nil {
		t.Fatalf("LoadThump: %v", err)
	}
	if got.SlackWebhookURL != "" {
		t.Errorf("SlackWebhookURL unset must load empty, got %q", got.SlackWebhookURL)
	}

	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.example/T000/B000/xxx")
	got, err = config.LoadThump(false /* broker */)
	if err != nil {
		t.Fatalf("LoadThump: %v", err)
	}
	if got.SlackWebhookURL != "https://hooks.slack.example/T000/B000/xxx" {
		t.Errorf("SlackWebhookURL = %q, want the configured value", got.SlackWebhookURL)
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
	leaftest.AssertLeaf(t, "errors", "fmt", "os", "time")
}
