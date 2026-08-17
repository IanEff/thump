package config_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/leaftest"
)

// testSealKeyB64 is a 32-byte AES-256 key, base64-encoded, standing in for
// THUMP_SEAL_KEY in every broker-mode test — these tests exercise loading and
// validation, not choice of key material.
const testSealKeyB64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

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
		"TEMPO_URL":          "http://tempo:3100",
		"WHIR_CATALOG":       "/etc/catalog-info.yaml",
		"WHIR_STATE_QUERIES": "/etc/state-queries.yaml",
		"CLANK_TRANSCRIPTS":  "/var/run/transcripts",
		"CLANK_WEIGHTS":      "/etc/clank/weights.yaml",
		"CLANK_LIMITS":       "/etc/clank/limits.yaml",
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
		TempoURL:         "http://tempo:3100",
		WhirCatalog:      "/etc/catalog-info.yaml",
		WhirStateQueries: "/etc/state-queries.yaml",
		WhirGraphWindow:  5 * time.Minute,
		Transcripts:      "/var/run/transcripts",
		Weights:          "/etc/clank/weights.yaml",
		Limits:           "/etc/clank/limits.yaml",
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
	t.Setenv("CLANK_WEIGHTS", "/etc/clank/weights.yaml")
	t.Setenv("CLANK_LIMITS", "/etc/clank/limits.yaml")
	t.Setenv("CLANK_INBOX", "/var/run/inbox")
	t.Setenv("CLANK_OUTBOX", "/var/run/outbox")
	t.Setenv("CLANK_OUTCOMES", "/var/run/outcomes")
	t.Setenv("CLANK_DECLINES", "/var/run/declines")
	for _, name := range []string{"PROM_URL", "EVIDENCE_QUERIES", "LOKI_URL", "TEMPO_URL", "WHIR_CATALOG", "WHIR_STATE_QUERIES", "WHIR_GRAPH_QUERIES", "CLANK_TRANSCRIPTS"} {
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
		WhirGraphWindow: 5 * time.Minute,
		Weights:         "/etc/clank/weights.yaml",
		Limits:          "/etc/clank/limits.yaml",
		Inbox:           "/var/run/inbox",
		Outbox:          "/var/run/outbox",
		Outcomes:        "/var/run/outcomes",
		Declines:        "/var/run/declines",
		// PromURL, EvidenceQueries, LokiURL, WhirCatalog, WhirStateQueries,
		// WhirGraphQueries, Transcripts all default to "" — genuinely optional, documented by
		// their zero value rather than a scattered `if x == ""` at call sites.
		// DedupeWindow and WhirGraphWindow defaults aren't "", so they're asserted explicitly above
		// rather than by omission like the string fields.
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadClank (-want +got):\n%s", diff)
	}
}

func TestLoadClank_RequiresPromURLWhenBothWhirVarsAreSet(t *testing.T) {
	setClankEnv(t)
	t.Setenv("PROM_URL", "")

	_, err := config.LoadClank(false /* broker */)
	if err == nil {
		t.Fatal("LoadClank: want an error — PROM_URL is unset but both WHIR vars are set")
	}
	if !strings.Contains(err.Error(), "PROM_URL") {
		t.Errorf("LoadClank error %q does not mention PROM_URL", err)
	}
}

func TestLoadClank_DoesNotRequirePromURLWhenOnlyOneWhirVarIsSet(t *testing.T) {
	setClankEnv(t)
	t.Setenv("PROM_URL", "")
	t.Setenv("WHIR_STATE_QUERIES", "") // WHIR_CATALOG alone must not trip the cross-check

	if _, err := config.LoadClank(false /* broker */); err != nil {
		t.Errorf("LoadClank: want no error — one whir var alone doesn't require PROM_URL, got %v", err)
	}
}

func TestLoadClank_BrokerMode_OfflineTrioNotRequired(t *testing.T) {
	// broker=true is clank's NATS path (lc.NATSURL != "") — CLANK_INBOX/
	// OUTBOX/OUTCOMES are the offline dir-poll fallback's vars and must not
	// be demanded when the broker path is what's actually going to run.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ACTION_CATALOG", "/etc/actions/catalog.yaml")
	t.Setenv("FAILURE_CLASSES", "/etc/actions/failure-classes.yaml")
	t.Setenv("CLANK_WEIGHTS", "/etc/clank/weights.yaml")
	t.Setenv("CLANK_LIMITS", "/etc/clank/limits.yaml")
	t.Setenv("WAL_DIR", "/var/run/wal")
	t.Setenv("WAL_CONFIG", "/etc/wal.yaml")
	t.Setenv("S3_ENDPOINT", "https://storage.googleapis.com")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	t.Setenv("TLS_CERT_FILE", "/etc/thump/tls/tls.crt")
	t.Setenv("TLS_KEY_FILE", "/etc/thump/tls/tls.key")
	t.Setenv("TLS_CA_FILE", "/etc/thump/tls/ca.crt")
	t.Setenv("THUMP_SEAL_KEY", testSealKeyB64)
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
	// to run (it's what publish.RunShipper ships the proposals WAL through).
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ACTION_CATALOG", "/etc/actions/catalog.yaml")
	t.Setenv("FAILURE_CLASSES", "/etc/actions/failure-classes.yaml")
	t.Setenv("CLANK_WEIGHTS", "/etc/clank/weights.yaml")
	t.Setenv("CLANK_LIMITS", "/etc/clank/limits.yaml")
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
	t.Setenv("WAL_CONFIG", "/etc/wal.yaml")
	t.Setenv("S3_ENDPOINT", "https://storage.googleapis.com")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	t.Setenv("TLS_CERT_FILE", "/etc/thump/tls/tls.crt")
	t.Setenv("TLS_KEY_FILE", "/etc/thump/tls/tls.key")
	t.Setenv("TLS_CA_FILE", "/etc/thump/tls/ca.crt")
	t.Setenv("THUMP_SEAL_KEY", testSealKeyB64)
	for _, name := range []string{"HISS_INBOX", "HISS_OUTBOX"} {
		t.Setenv(name, "")
	}

	if _, err := config.LoadHiss(true /* broker */); err != nil {
		t.Errorf("LoadHiss(broker=true): want no error with the offline pair unset, got %v", err)
	}
}

func TestLoadHiss_BrokerMode_ApprovalRetentionDefaultsTo24h(t *testing.T) {
	t.Setenv("HISS_POLICY", "/etc/policy.yaml")
	t.Setenv("WAL_DIR", "/var/run/wal")
	t.Setenv("WAL_CONFIG", "/etc/wal.yaml")
	t.Setenv("S3_ENDPOINT", "https://storage.googleapis.com")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	t.Setenv("TLS_CERT_FILE", "/etc/thump/tls/tls.crt")
	t.Setenv("TLS_KEY_FILE", "/etc/thump/tls/tls.key")
	t.Setenv("TLS_CA_FILE", "/etc/thump/tls/ca.crt")
	t.Setenv("THUMP_SEAL_KEY", testSealKeyB64)
	t.Setenv("APPROVALREQUEST_RETENTION", "")

	got, err := config.LoadHiss(true /* broker */)
	if err != nil {
		t.Fatalf("LoadHiss(broker=true): %v", err)
	}
	if got.ApprovalRetention != 24*time.Hour {
		t.Errorf("ApprovalRetention unset: want the 24h default (the constant extraction preserves), got %s", got.ApprovalRetention)
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
		"PROM_URL":            "http://prom:9090",
		"WHIR_CATALOG":        "/etc/catalog-info.yaml",
		"WHIR_STATE_QUERIES":  "/etc/state-queries.yaml",
		"RATTLE_TRAFFIC":      "/etc/traffic-queries.yaml",
		"RATTLE_OUTBOX":       "/var/run/outbox",
		"RATTLE_WATCH":        "/etc/watch.yaml",
		"RATTLE_QUERY_CONFIG": "/etc/query.yaml",
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
		QueryConfig:      "/etc/query.yaml",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadRattle (-want +got):\n%s", diff)
	}
}

func TestLoadRattle_OptionalDefaults(t *testing.T) {
	// Only what's unconditionally required: PROM_URL, RATTLE_WATCH, and
	// RATTLE_QUERY_CONFIG.
	t.Setenv("PROM_URL", "http://prom:9090")
	t.Setenv("RATTLE_WATCH", "/etc/watch.yaml")
	t.Setenv("RATTLE_QUERY_CONFIG", "/etc/query.yaml")
	for _, name := range []string{"WHIR_CATALOG", "WHIR_STATE_QUERIES", "RATTLE_TRAFFIC", "RATTLE_OUTBOX"} {
		t.Setenv(name, "")
	}

	got, err := config.LoadRattle(false /* broker */)
	if err != nil {
		t.Fatalf("LoadRattle: %v", err)
	}
	want := config.Rattle{
		PromURL:     "http://prom:9090",
		WatchPath:   "/etc/watch.yaml",
		QueryConfig: "/etc/query.yaml",
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

// setThumpBrokerEnv sets the vars LoadThump additionally requires in the
// broker path (broker=true) — WAL_DIR, the S3 quartet, and the TLS triple —
// on top of setThumpEnv's offline set.
func setThumpBrokerEnv(t *testing.T) {
	t.Helper()
	for name, val := range map[string]string{
		"WAL_DIR":        "/var/run/wal",
		"WAL_CONFIG":     "/etc/wal.yaml",
		"S3_ENDPOINT":    "https://storage.googleapis.com",
		"S3_BUCKET":      "thump-wal",
		"S3_ACCESS_KEY":  "test-access-key",
		"S3_SECRET_KEY":  "test-secret-key",
		"TLS_CERT_FILE":  "/etc/thump/tls/tls.crt",
		"TLS_KEY_FILE":   "/etc/thump/tls/tls.key",
		"TLS_CA_FILE":    "/etc/thump/tls/ca.crt",
		"THUMP_SEAL_KEY": testSealKeyB64,
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
		Executor:      "dry",
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

func TestLoadThump_ForgeRepoAndTokenOptional(t *testing.T) {
	setThumpEnv(t)
	for _, name := range []string{"THUMP_EXECUTOR", "THUMP_KILLSWITCH", "PROM_URL", "EVIDENCE_QUERIES", "SLACK_WEBHOOK_URL", "FORGE_REPO", "FORGE_TOKEN"} {
		t.Setenv(name, "")
	}

	got, err := config.LoadThump(false /* broker */)
	if err != nil {
		t.Fatalf("LoadThump: %v", err)
	}
	if got.ForgeRepo != "" || got.ForgeToken != "" {
		t.Errorf("ForgeRepo/ForgeToken unset must load empty, got repo=%q token=%q", got.ForgeRepo, got.ForgeToken)
	}

	t.Setenv("FORGE_REPO", "IanEff/thump")
	t.Setenv("FORGE_TOKEN", "ghp_secret")
	got, err = config.LoadThump(false /* broker */)
	if err != nil {
		t.Fatalf("LoadThump: %v", err)
	}
	if got.ForgeRepo != "IanEff/thump" || got.ForgeToken != "ghp_secret" {
		t.Errorf("ForgeRepo/ForgeToken = %q/%q, want configured values", got.ForgeRepo, got.ForgeToken)
	}
}

func TestLoadThump_BrokerMode_OfflinePairNotRequired(t *testing.T) {
	// broker=true is thump's NATS path — THUMP_INBOX/OUTBOX are the offline
	// dir-poll fallback's vars and must not be demanded when the broker path
	// is what's actually going to run. ACTION_CATALOG is required either way.
	t.Setenv("ACTION_CATALOG", "/etc/actions/catalog.yaml")
	t.Setenv("WAL_DIR", "/var/run/wal")
	t.Setenv("WAL_CONFIG", "/etc/wal.yaml")
	t.Setenv("S3_ENDPOINT", "https://storage.googleapis.com")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	t.Setenv("TLS_CERT_FILE", "/etc/thump/tls/tls.crt")
	t.Setenv("TLS_KEY_FILE", "/etc/thump/tls/tls.key")
	t.Setenv("TLS_CA_FILE", "/etc/thump/tls/ca.crt")
	t.Setenv("THUMP_SEAL_KEY", testSealKeyB64)
	for _, name := range []string{"THUMP_INBOX", "THUMP_OUTBOX"} {
		t.Setenv(name, "")
	}

	if _, err := config.LoadThump(true /* broker */); err != nil {
		t.Errorf("LoadThump(broker=true): want no error with the offline pair unset, got %v", err)
	}
}

func TestConfigIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "encoding/base64", "errors", "net/url", "fmt", "os", "slices", "strconv", "time")
}

func TestLoadThump_AcceptsEitherSchemeButRefusesASchemelessS3EndpointAtLoadTime(t *testing.T) {
	cases := map[string]struct {
		endpoint string
		wantErr  bool
	}{
		"LoadThump accepts an https S3 endpoint in the broker path": {
			endpoint: "https://storage.googleapis.com",
		},
		"LoadThump accepts a plaintext http S3 endpoint — the dev profile's S3Mock has no real network segment to protect": {
			endpoint: "http://s3mock.thump.svc.cluster.local:9090",
		},
		"LoadThump refuses a schemeless endpoint rather than letting the SDK choose a wire": {
			endpoint: "storage.googleapis.com",
			wantErr:  true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setThumpEnv(t)
			setThumpBrokerEnv(t)
			t.Setenv("S3_ENDPOINT", tc.endpoint)

			got, err := config.LoadThump(true)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("a %q endpoint must refuse at load time, got %+v", tc.endpoint, got)
				}
				if !strings.Contains(err.Error(), "S3_ENDPOINT") {
					t.Errorf("the error must name the offending var so a redeploy fixes the right thing, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.endpoint, got.S3Endpoint); diff != "" {
				t.Error("a valid endpoint was rewritten on the way through", diff)
			}
		})
	}
}

func TestEveryBrokerLoader_AcceptsAPlaintextS3EndpointNotJustThumps(t *testing.T) {
	cases := map[string]struct {
		load func(bool) error
		env  func(*testing.T)
	}{
		"LoadClank accepts a plaintext S3 endpoint in the broker path": {
			load: func(b bool) error { _, err := config.LoadClank(b); return err },
			env:  setClankEnv,
		},
		"LoadHiss accepts a plaintext S3 endpoint in the broker path": {
			load: func(b bool) error { _, err := config.LoadHiss(b); return err },
			env:  setHissEnv,
		},
		"LoadRattle accepts a plaintext S3 endpoint in the broker path": {
			load: func(b bool) error { _, err := config.LoadRattle(b); return err },
			env:  setRattleEnv,
		},
		"LoadThump accepts a plaintext S3 endpoint in the broker path": {
			load: func(b bool) error { _, err := config.LoadThump(b); return err },
			env:  setThumpEnv,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.env(t)
			setThumpBrokerEnv(t)
			t.Setenv("S3_ENDPOINT", "http://s3mock.thump.svc.cluster.local:9090")

			if err := tc.load(true); err != nil {
				t.Errorf("every beat's dev profile talks to S3Mock over plain HTTP — hardening one loader and not the rest is a hole with a test in front of it: %v", err)
			}
		})
	}
}

func TestLoadThump_ReportsASchemelessEndpointAlongsideEveryOtherMissingVar(t *testing.T) {
	setThumpEnv(t)
	setThumpBrokerEnv(t)
	t.Setenv("S3_ENDPOINT", "minio.thump.svc:9000")
	t.Setenv("S3_BUCKET", "")
	t.Setenv("ACTION_CATALOG", "")

	_, err := config.LoadThump(true)
	if err == nil {
		t.Fatal("three bad vars must not load")
	}
	for _, want := range []string{"S3_ENDPOINT", "S3_BUCKET", "ACTION_CATALOG"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s is missing from the error — one redeploy per var discovered is what this loader exists to prevent: %v", want, err)
		}
	}
}

func TestLoadClank_AcceptsAPlaintextPrometheusURLBecauseThatLegIsTheMeshs(t *testing.T) {
	setClankEnv(t)
	t.Setenv("PROM_URL", "http://prometheus-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090")

	got, err := config.LoadClank(false)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("http://prometheus-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090", got.PromURL); diff != "" {
		t.Error("the shipped chart's own Prometheus URL stopped loading", diff)
	}
}

func TestLoadClank_ValidatesNATSURLScheme(t *testing.T) {
	cases := map[string]struct {
		url     string
		wantErr bool
	}{
		"LoadClank accepts an empty NATS_URL because offline mode never sets one": {
			url: "",
		},
		"LoadClank accepts a nats scheme": {
			url: "nats://nats.thump.svc:4222",
		},
		"LoadClank accepts a tls scheme": {
			url: "tls://nats.thump.svc:4222",
		},
		"LoadClank refuses a scheme nats.Connect was never going to accept": {
			url:     "http://nats.thump.svc:4222",
			wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setClankEnv(t)
			t.Setenv("NATS_URL", tc.url)

			got, err := config.LoadClank(false)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("a %q NATS_URL must refuse at load time, got %+v", tc.url, got)
				}
				if !strings.Contains(err.Error(), "NATS_URL") {
					t.Errorf("the error must name the offending var, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.url, got.NATSURL); diff != "" {
				t.Error("a valid NATS_URL was rewritten on the way through", diff)
			}
		})
	}
}

func TestLoadClank_ValidatesSealKey(t *testing.T) {
	cases := map[string]struct {
		sealKey string
		wantErr bool
	}{
		"LoadClank accepts a 32-byte base64 key": {
			sealKey: testSealKeyB64,
		},
		"LoadClank refuses a key that isn't valid base64": {
			sealKey: "not valid base64!!",
			wantErr: true,
		},
		"LoadClank refuses a key shorter than 32 bytes": {
			sealKey: base64.StdEncoding.EncodeToString([]byte("too short")),
			wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setClankEnv(t)
			t.Setenv("WAL_DIR", "/var/run/wal")
			t.Setenv("WAL_CONFIG", "/etc/wal.yaml")
			t.Setenv("S3_ENDPOINT", "https://storage.googleapis.com")
			t.Setenv("S3_BUCKET", "thump-wal")
			t.Setenv("S3_ACCESS_KEY", "test-access-key")
			t.Setenv("S3_SECRET_KEY", "test-secret-key")
			t.Setenv("TLS_CERT_FILE", "/etc/thump/tls/tls.crt")
			t.Setenv("TLS_KEY_FILE", "/etc/thump/tls/tls.key")
			t.Setenv("TLS_CA_FILE", "/etc/thump/tls/ca.crt")
			t.Setenv("THUMP_SEAL_KEY", tc.sealKey)

			got, err := config.LoadClank(true)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("a %q THUMP_SEAL_KEY must refuse at load time, got %+v", tc.sealKey, got)
				}
				if !strings.Contains(err.Error(), "THUMP_SEAL_KEY") {
					t.Errorf("the error must name the offending var, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want, _ := base64.StdEncoding.DecodeString(tc.sealKey)
			if diff := cmp.Diff(want, got.SealKey); diff != "" {
				t.Error("a valid seal key didn't decode into SealKey as loaded", diff)
			}
		})
	}
}

func TestEveryBrokerLoader_RequiresSealKeyNotJustClanks(t *testing.T) {
	cases := map[string]struct {
		load func(bool) error
		env  func(*testing.T)
	}{
		"LoadClank requires THUMP_SEAL_KEY in the broker path":  {load: func(b bool) error { _, err := config.LoadClank(b); return err }, env: setClankEnv},
		"LoadHiss requires THUMP_SEAL_KEY in the broker path":   {load: func(b bool) error { _, err := config.LoadHiss(b); return err }, env: setHissEnv},
		"LoadRattle requires THUMP_SEAL_KEY in the broker path": {load: func(b bool) error { _, err := config.LoadRattle(b); return err }, env: setRattleEnv},
		"LoadThump requires THUMP_SEAL_KEY in the broker path":  {load: func(b bool) error { _, err := config.LoadThump(b); return err }, env: setThumpEnv},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.env(t)
			if err := tc.load(true); err == nil || !strings.Contains(err.Error(), "THUMP_SEAL_KEY") {
				t.Error("every beat seals its WAL segments — hardening one loader and not the rest is a hole with a test in front of it")
			}
		})
	}
}

func TestEveryLoader_RefusesAPlaintextNATSURLNotJustClanks(t *testing.T) {
	cases := map[string]struct {
		load func(bool) error
		env  func(*testing.T)
	}{
		"LoadClank refuses a plaintext NATS_URL": {
			load: func(b bool) error { _, err := config.LoadClank(b); return err },
			env:  setClankEnv,
		},
		"LoadHiss refuses a plaintext NATS_URL": {
			load: func(b bool) error { _, err := config.LoadHiss(b); return err },
			env:  setHissEnv,
		},
		"LoadRattle refuses a plaintext NATS_URL": {
			load: func(b bool) error { _, err := config.LoadRattle(b); return err },
			env:  setRattleEnv,
		},
		"LoadThump refuses a plaintext NATS_URL": {
			load: func(b bool) error { _, err := config.LoadThump(b); return err },
			env:  setThumpEnv,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.env(t)
			t.Setenv("NATS_URL", "http://nats.thump.svc:4222")

			if err := tc.load(false); err == nil {
				t.Error("every beat dials the same broker — validating one loader and not  in front of it")
			}
		})
	}
}

func TestLoadBootstrap_MissingRequired_ReportsAllAtOnce(t *testing.T) {
	for _, name := range []string{"NATS_URL", "TLS_CERT_FILE", "TLS_KEY_FILE", "TLS_CA_FILE"} {
		t.Setenv(name, "")
	}

	_, err := config.LoadBootstrap()
	if err == nil {
		t.Fatal("LoadBootstrap: want an error, got nil")
	}
	for _, want := range []string{"NATS_URL", "TLS_CERT_FILE", "TLS_KEY_FILE", "TLS_CA_FILE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadBootstrap error %q does not mention %s", err, want)
		}
	}
}

func TestLoadBootstrap_Valid_PopulatesStruct(t *testing.T) {
	t.Setenv("NATS_URL", "tls://nats.thump.svc:4222")
	t.Setenv("TLS_CERT_FILE", "/etc/thump/tls/tls.crt")
	t.Setenv("TLS_KEY_FILE", "/etc/thump/tls/tls.key")
	t.Setenv("TLS_CA_FILE", "/etc/thump/tls/ca.crt")

	got, err := config.LoadBootstrap()
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	want := config.Bootstrap{
		NATSURL:     "tls://nats.thump.svc:4222",
		TLSCertFile: "/etc/thump/tls/tls.crt",
		TLSKeyFile:  "/etc/thump/tls/tls.key",
		TLSCAFile:   "/etc/thump/tls/ca.crt",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadBootstrap (-want +got):\n%s", diff)
	}
}

func TestLoadBootstrap_RefusesAPlaintextNATSURL(t *testing.T) {
	t.Setenv("NATS_URL", "http://nats.thump.svc:4222")
	t.Setenv("TLS_CERT_FILE", "/etc/thump/tls/tls.crt")
	t.Setenv("TLS_KEY_FILE", "/etc/thump/tls/tls.key")
	t.Setenv("TLS_CA_FILE", "/etc/thump/tls/ca.crt")

	if _, err := config.LoadBootstrap(); err == nil {
		t.Error("LoadBootstrap must refuse a plaintext NATS_URL — the Job dials the same broker every beat does")
	}
}

func TestLoadCorpus_MissingRequired_ReportsAllAtOnce(t *testing.T) {
	for _, name := range []string{"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		t.Setenv(name, "")
	}

	_, err := config.LoadCorpus()
	if err == nil {
		t.Fatal("LoadCorpus: want an error, got nil")
	}
	for _, want := range []string{"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("LoadCorpus error %q does not mention %s", err, want)
		}
	}
}

func TestLoadCorpus_Valid_PopulatesStruct(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "https://s3.thump.svc:9000")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "access")
	t.Setenv("S3_SECRET_KEY", "secret")

	got, err := config.LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	want := config.Corpus{
		S3Endpoint:  "https://s3.thump.svc:9000",
		S3Bucket:    "thump-wal",
		S3AccessKey: "access",
		S3SecretKey: "secret",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadCorpus (-want +got):\n%s", diff)
	}
}

func TestLoadCorpus_AcceptsAPlaintextS3Endpoint(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "http://s3.thump.svc:9000")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "access")
	t.Setenv("S3_SECRET_KEY", "secret")

	if _, err := config.LoadCorpus(); err != nil {
		t.Errorf("LoadCorpus must accept a plaintext S3_ENDPOINT — the miner reads from the same dev S3Mock every beat ships sealed segments to: %v", err)
	}
}

func TestLoadCorpus_RefusesASchemelessS3Endpoint(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "s3.thump.svc:9000")
	t.Setenv("S3_BUCKET", "thump-wal")
	t.Setenv("S3_ACCESS_KEY", "access")
	t.Setenv("S3_SECRET_KEY", "secret")

	if _, err := config.LoadCorpus(); err == nil {
		t.Error("LoadCorpus must refuse a schemeless S3_ENDPOINT rather than letting the SDK choose a wire")
	}
}

func TestEveryBrokerLoader_RequiresTLSFilesNotJustClanks(t *testing.T) {
	cases := map[string]struct {
		load func(bool) error
		env  func(*testing.T)
	}{
		"LoadClank requires TLS_CERT_FILE in the broker path":  {load: func(b bool) error { _, err := config.LoadClank(b); return err }, env: setClankEnv},
		"LoadHiss requires TLS_CERT_FILE in the broker path":   {load: func(b bool) error { _, err := config.LoadHiss(b); return err }, env: setHissEnv},
		"LoadRattle requires TLS_CERT_FILE in the broker path": {load: func(b bool) error { _, err := config.LoadRattle(b); return err }, env: setRattleEnv},
		"LoadThump requires TLS_CERT_FILE in the broker path":  {load: func(b bool) error { _, err := config.LoadThump(b); return err }, env: setThumpEnv},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.env(t)
			if err := tc.load(true); err == nil || !strings.Contains(err.Error(), "TLS_CERT_FILE") {
				t.Error("every beat's NATS leg needs a cert — hardening one loader and not the rest is a hole with a test in front of it")
			}
		})
	}
}

// TestLoadThump_RefusesAnUnrecognisedExecutorRatherThanDisarming holds the
// executor to two spellings. buildExecutor tests cfg.Executor != "live", so
// every near-miss an operator can type lands in the dry branch — refusing at
// load is what keeps a typo from being indistinguishable from a choice.
func TestLoadThump_RefusesAnUnrecognisedExecutorRatherThanDisarming(t *testing.T) {
	tests := map[string]struct {
		executor string
		wantErr  bool
	}{
		"LoadThump refuses an executor value that is neither live nor dry": {
			executor: "sometimes", wantErr: true,
		},
		"LoadThump refuses a capitalised Live rather than silently disarming": {
			executor: "Live", wantErr: true,
		},
		"LoadThump accepts live because it is the one spelling that arms the engine": {
			executor: "live", wantErr: false,
		},
		"LoadThump accepts dry because naming the default explicitly is not an error": {
			executor: "dry", wantErr: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			setThumpEnv(t)
			t.Setenv("THUMP_EXECUTOR", tc.executor)

			_, err := config.LoadThump(false /* broker */)
			if tc.wantErr {
				if err == nil {
					t.Fatal("an unrecognised THUMP_EXECUTOR loaded clean and would have run dry")
				}
				if !strings.Contains(err.Error(), "THUMP_EXECUTOR") {
					t.Error("the load error does not name the var the operator got wrong", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestLoadThump_DefaultsTheExecutorToDryWhenTheVarIsUnset pins the default the
// Thump.Executor field comment documents — before OptionalEnum the unset case
// loaded as "", and only worked because "" != "live" by accident.
func TestLoadThump_DefaultsTheExecutorToDryWhenTheVarIsUnset(t *testing.T) {
	setThumpEnv(t)
	t.Setenv("THUMP_EXECUTOR", "")

	got, err := config.LoadThump(false /* broker */)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("dry", got.Executor); diff != "" {
		t.Error("an unset THUMP_EXECUTOR did not resolve to the documented default", diff)
	}
}
