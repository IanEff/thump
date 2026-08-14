// Package config is each beat's typed, validated environment — a leaf
// package (leaf_test.go) so it can never grow an import into a beat's
// internals. Load reads every var exactly once and reports every missing
// required one together, so a bad deploy fails with a complete list instead
// of one redeploy per var discovered.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"time"
)

// BrokerStore is the WAL/S3/TLS/seal-key block every beat's broker path
// requires — one struct instead of four copies, so the next knob added to
// it lands in one place.
type BrokerStore struct {
	WALDir      string // WAL_DIR — required only in the broker path
	WALConfig   string // WAL_CONFIG — required only in the broker path; path to mounted wal.yaml
	S3Endpoint  string // S3_ENDPOINT — required only in the broker path
	S3Bucket    string // S3_BUCKET — required only in the broker path
	S3AccessKey string // S3_ACCESS_KEY — required only in the broker path
	S3SecretKey string // S3_SECRET_KEY — required only in the broker path
	TLSCertFile string // TLS_CERT_FILE — required only in the broker path; this beat's own leaf, shared across every TLS leg it dials or serves
	TLSKeyFile  string // TLS_KEY_FILE — required only in the broker path
	TLSCAFile   string // TLS_CA_FILE — required only in the broker path; the private CA both ends verify against
	SealKey     []byte // THUMP_SEAL_KEY — required only in the broker path; 32-byte AES-256 key, base64, sealing WAL segments (and clank's transcripts) before they reach the bucket
}

// loadBrokerStore reads the nine broker-path vars every beat's Load* function
// requires once it's running in broker mode. S3_ENDPOINT allows http as well
// as https — the dev cluster profile's S3Mock backend serves plain HTTP with
// no in-process TLS, same declared-plaintext posture as OTLPEndpoint's http
// branch; every other profile's operator-supplied endpoint is a real https
// bucket.
func loadBrokerStore(l *loader) BrokerStore {
	return BrokerStore{
		WALDir:      l.Require("WAL_DIR"),
		WALConfig:   l.Require("WAL_CONFIG"),
		S3Endpoint:  l.RequireURL("S3_ENDPOINT", "http", "https"),
		S3Bucket:    l.Require("S3_BUCKET"),
		S3AccessKey: l.Require("S3_ACCESS_KEY"),
		S3SecretKey: l.Require("S3_SECRET_KEY"),
		TLSCertFile: l.Require("TLS_CERT_FILE"),
		TLSKeyFile:  l.Require("TLS_KEY_FILE"),
		TLSCAFile:   l.Require("TLS_CA_FILE"),
		SealKey:     l.RequireBase64Key("THUMP_SEAL_KEY", 32),
	}
}

// Clank is clank's environment, one field per var its Main used to read ad
// hoc with os.Getenv.
type Clank struct {
	AnthropicAPIKey  string        // ANTHROPIC_API_KEY — required
	ActionCatalog    string        // ACTION_CATALOG - required; the authored action catalog YAML
	FailureClasses   string        // FAILURE_CLASSES - required; the authored failure-class definitions YAML
	DedupeWindow     time.Duration // DEDUPE_WINDOW — optional; how far back Engine.Propose looks for a live set on the same fingerprint before suppressing a redelivery; defaults to 1h
	NATSURL          string        // NATS_URL — optional; beat.Start already used this var to select broker mode before Load ran, so this is the same var validated a second time, not a new one
	OTLPEndpoint     string        // OTEL_EXPORTER_OTLP_ENDPOINT — optional; empty means unconfigured
	PromURL          string        // PROM_URL — optional; empty disables the metrics tool
	EvidenceQueries  string        // EVIDENCE_QUERIES — optional; only meaningful with PromURL set
	LokiURL          string        // LOKI_URL — optional; empty disables the loki tool
	WhirCatalog      string        // WHIR_CATALOG — optional; pairs with WhirStateQueries
	WhirStateQueries string        // WHIR_STATE_QUERIES — optional; pairs with WhirCatalog
	ArgoEnabled      bool          // ARGOCD_ENABLED - optional, default false; wires ArgoChangeSource against clank's in-cluster identity.
	Transcripts      string        // CLANK_TRANSCRIPTS — optional, offline path only; broker mode always persists to S3 via the WAL's S3_* creds, ignoring this var. Empty keeps turns in memory only.
	Inbox            string        // CLANK_INBOX — required only in the offline (non-broker) path
	Outbox           string        // CLANK_OUTBOX — required only in the offline path
	Outcomes         string        // CLANK_OUTCOMES — required only in the offline path
	Declines         string        // CLANK_DECLINES — required only in the offline path
	BrokerStore                    // WAL/S3/TLS/seal-key — required only in the broker path
	Weights          string        // CLANK_WEIGHTS -- required; path to mounted weights.yaml
	Limits           string        // CLANK_LIMITS -- required; path to mounted limits.yaml
}

// LoadClank reads clank's environment once. broker is whether Main resolved
// a NATS_URL (lc.NATSURL != "" after beat.Start) — the offline dir-poll
// inbox/outbox/outcomes trio is only required when it didn't; the broker
// path never reads them. WAL_DIR and the S3 fields are the inverse — no WAL
// in the offline path, nothing to ship. PROM_URL is cross-required with the
// WHIR pair: WhirTopology's Resolver dials Prometheus, so clank.buildIntake
// can trust a fully-set whir pair always carries a usable PromURL, rather
// than checking it again at construction time.
func LoadClank(broker bool) (Clank, error) {
	l := &loader{}
	c := Clank{
		AnthropicAPIKey:  l.Require("ANTHROPIC_API_KEY"),
		ActionCatalog:    l.Require("ACTION_CATALOG"),
		FailureClasses:   l.Require("FAILURE_CLASSES"),
		DedupeWindow:     l.OptionalDuration("DEDUPE_WINDOW", time.Hour),
		NATSURL:          l.OptionalURL("NATS_URL", "nats", "tls"),
		OTLPEndpoint:     l.OptionalURL("OTEL_EXPORTER_OTLP_ENDPOINT", "http", "https"),
		PromURL:          l.Optional("PROM_URL"),
		EvidenceQueries:  l.Optional("EVIDENCE_QUERIES"),
		LokiURL:          l.Optional("LOKI_URL"),
		WhirCatalog:      l.Optional("WHIR_CATALOG"),
		WhirStateQueries: l.Optional("WHIR_STATE_QUERIES"),
		ArgoEnabled:      l.OptionalBool("ARGOCD_ENABLED"),
		Transcripts:      l.Optional("CLANK_TRANSCRIPTS"),
		Weights:          l.Require("CLANK_WEIGHTS"),
		Limits:           l.Require("CLANK_LIMITS"),
	}
	if c.WhirCatalog != "" && c.WhirStateQueries != "" && c.PromURL == "" {
		l.errs = append(l.errs, errors.New("PROM_URL is required when WHIR_CATALOG and WHIR_STATE_QUERIES are set"))
	}
	if broker {
		c.Inbox = l.Optional("CLANK_INBOX")
		c.Outbox = l.Optional("CLANK_OUTBOX")
		c.Outcomes = l.Optional("CLANK_OUTCOMES")
		c.Declines = l.Optional("CLANK_DECLINES")
		c.BrokerStore = loadBrokerStore(l)
	} else {
		c.Inbox = l.Require("CLANK_INBOX")
		c.Outbox = l.Require("CLANK_OUTBOX")
		c.Outcomes = l.Require("CLANK_OUTCOMES")
		c.Declines = l.Require("CLANK_DECLINES")
	}
	return c, l.err()
}

// Hiss is hiss's environment. Policy is HISS_POLICY's raw path — hiss.go's
// own LoadPolicy reads and parses it into a Policy struct; config stops at
// the validated string, the same division whir/contract draw between
// "where's the file" (env) and "what's in it" (the beat's own YAML parse).
type Hiss struct {
	Policy       string // HISS_POLICY — required
	NATSURL      string // NATS_URL — optional; beat.Start already used this var to select broker mode before Load ran, so this is the same var validated a second time, not a new one
	OTLPEndpoint string // OTEL_EXPORTER_OTLP_ENDPOINT — optional; empty means unconfigured
	Inbox        string // HISS_INBOX — required only in the offline (non-broker) path
	Outbox       string // HISS_OUTBOX — required only in the offline path
	BrokerStore         // WAL/S3/TLS/seal-key — required only in the broker path

	ApprovalRequestsEnabled bool          // APPROVALREQUESTS_ENABLED — optional, default false; runs the ApprovalRequest controller against hiss's in-cluster identity. Off means calipers is the only path that releases a hold.
	ApprovalRetention       time.Duration // APPROVALREQUEST_RETENTION — optional; how long a Processed ApprovalRequest stays readable before the sweep reclaims it; defaults to 24h
}

// LoadHiss reads hiss's environment once. broker is whether Main resolved a
// NATS_URL (lc.NATSURL != "" after beat.Start) — the offline dir-poll
// inbox/outbox pair is only required when it didn't; WAL_DIR and the S3
// fields are the inverse, required only when it did (beat.NewWALPublisher
// rejects an empty walDir deep inside runBroker today — this surfaces the
// same requirement up front, alongside every other var, instead of a lone
// late failure).
func LoadHiss(broker bool) (Hiss, error) {
	l := &loader{}
	h := Hiss{
		Policy:       l.Require("HISS_POLICY"),
		NATSURL:      l.OptionalURL("NATS_URL", "nats", "tls"),
		OTLPEndpoint: l.OptionalURL("OTEL_EXPORTER_OTLP_ENDPOINT", "http", "https"),
	}
	if broker {
		h.Inbox = l.Optional("HISS_INBOX")
		h.Outbox = l.Optional("HISS_OUTBOX")
		h.BrokerStore = loadBrokerStore(l)
		h.ApprovalRequestsEnabled = l.OptionalBool("APPROVALREQUESTS_ENABLED")
		h.ApprovalRetention = l.OptionalDuration("APPROVALREQUEST_RETENTION", 24*time.Hour)
	} else {
		h.Inbox = l.Require("HISS_INBOX")
		h.Outbox = l.Require("HISS_OUTBOX")
	}
	return h, l.err()
}

// Rattle is rattle's environment. Rattle is the Detect beat — the first
// hop, nothing upstream of it — so unlike every other beat it has no inbox
// at all, and even its outbox is optional: runLoop tolerates a nil
// Publisher and just logs without publishing (rattle.go's "if pub != nil").
type Rattle struct {
	PromURL          string // PROM_URL — required unconditionally, not broker-gated
	NATSURL          string // NATS_URL — optional; beat.Start already used this var to select broker mode before Load ran, so this is the same var validated a second time, not a new one
	OTLPEndpoint     string // OTEL_EXPORTER_OTLP_ENDPOINT — optional; empty means unconfigured
	WhirCatalog      string // WHIR_CATALOG — optional; pairs with WhirStateQueries
	WhirStateQueries string // WHIR_STATE_QUERIES — optional; pairs with WhirCatalog
	Traffic          string // RATTLE_TRAFFIC — optional; empty disables the Hubble traffic source
	Outbox           string // RATTLE_OUTBOX — optional even offline; unset means detections are logged, not published
	WatchPath        string // RATTLE_WATCH - required unconditionally
	QueryConfig      string // RATTLE_QUERY_CONFIG - required unconditionally; path to mounted query.yaml
	BrokerStore             // WAL/S3/TLS/seal-key — required only in the broker path
}

// LoadRattle reads rattle's environment once. broker is whether Main
// resolved a NATS_URL (lc.NATSURL != "" after beat.Start) — WAL_DIR and the
// S3 fields are only required in that path; RATTLE_OUTBOX is optional
// either way.
func LoadRattle(broker bool) (Rattle, error) {
	l := &loader{}
	r := Rattle{
		PromURL:          l.Require("PROM_URL"),
		NATSURL:          l.OptionalURL("NATS_URL", "nats", "tls"),
		OTLPEndpoint:     l.OptionalURL("OTEL_EXPORTER_OTLP_ENDPOINT", "http", "https"),
		WhirCatalog:      l.Optional("WHIR_CATALOG"),
		WhirStateQueries: l.Optional("WHIR_STATE_QUERIES"),
		WatchPath:        l.Require("RATTLE_WATCH"),
		QueryConfig:      l.Require("RATTLE_QUERY_CONFIG"),
		Traffic:          l.Optional("RATTLE_TRAFFIC"),
		Outbox:           l.Optional("RATTLE_OUTBOX"),
	}
	if broker {
		r.BrokerStore = loadBrokerStore(l)
	}
	return r, l.err()
}

// Thump is thump's environment. WALDir backs both the orders and outcomes
// WAL publishers in the broker path (runBroker calls beat.NewWALPublisher
// twice against the one dir) — one field, two consumers.
type Thump struct {
	ActionCatalog   string // ACTION_CATALOG - required; the authored action catalog YAML
	Executor        string // THUMP_EXECUTOR - "dry" (default) | "live"; anything else refuses at load time, never a silent dry-run
	KillSwitchPath  string // THUMP_KILLSWITCH -- path to armed:bool file; only read in live mode
	NATSURL         string // NATS_URL — optional; beat.Start already used this var to select broker mode before Load ran, so this is the same var validated a second time, not a new one
	OTLPEndpoint    string // OTEL_EXPORTER_OTLP_ENDPOINT — optional; empty means unconfigured
	PromURL         string // PROM_URL — optional; empty disables the automatic reversal watcher
	EvidenceQueries string // EVIDENCE_QUERIES — optional; only meaningful with PromURL set
	SlackWebhookURL string // SLACK_WEBHOOK_URL - optional; empty means no Notifier is wired.
	ForgeRepo       string // FORGE_REPO - "owner/name" of the GitOps source of record; empty means no forge
	ForgeToken      string // FORGE_TOKEN - fine-grained PAT scoped to ForgeRepo
	Inbox           string // THUMP_INBOX — required only in the offline (non-broker) path
	Outbox          string // THUMP_OUTBOX — required only in the offline path
	BrokerStore            // WAL/S3/TLS/seal-key — required only in the broker path
}

// LoadThump reads thump's environment once. broker is whether Main resolved
// a NATS_URL (lc.NATSURL != "" after beat.Start) — the offline dir-poll
// inbox/outbox pair is only required when it didn't; WAL_DIR is the inverse.
func LoadThump(broker bool) (Thump, error) {
	l := &loader{}
	t := Thump{
		ActionCatalog:   l.Require("ACTION_CATALOG"),
		Executor:        l.OptionalOneOf("THUMP_EXECUTOR", "dry", "live"),
		KillSwitchPath:  l.Optional("THUMP_KILLSWITCH"),
		NATSURL:         l.OptionalURL("NATS_URL", "nats", "tls"),
		OTLPEndpoint:    l.OptionalURL("OTEL_EXPORTER_OTLP_ENDPOINT", "http", "https"),
		PromURL:         l.Optional("PROM_URL"),
		EvidenceQueries: l.Optional("EVIDENCE_QUERIES"),
		SlackWebhookURL: l.Optional("SLACK_WEBHOOK_URL"),
		ForgeRepo:       l.Optional("FORGE_REPO"),
		ForgeToken:      l.Optional("FORGE_TOKEN"),
	}
	if broker {
		t.Inbox = l.Optional("THUMP_INBOX")
		t.Outbox = l.Optional("THUMP_OUTBOX")
		t.BrokerStore = loadBrokerStore(l)
	} else {
		t.Inbox = l.Require("THUMP_INBOX")
		t.Outbox = l.Require("THUMP_OUTBOX")
	}
	return t, l.err()
}

// Bootstrap is the one-shot broker-bootstrap Job's environment (R6c) — its
// own NATS_URL and TLS triple, all required unconditionally. There's no
// offline path: this binary has exactly one job, and it's unreachable
// without a broker to reach.
type Bootstrap struct {
	NATSURL     string // NATS_URL — required
	TLSCertFile string // TLS_CERT_FILE — required; the Job's own leaf, distinct from every beat's — it alone holds $JS.API.> in nats.conf
	TLSKeyFile  string // TLS_KEY_FILE — required
	TLSCAFile   string // TLS_CA_FILE — required
}

// LoadBootstrap reads the broker-bootstrap Job's environment once.
func LoadBootstrap() (Bootstrap, error) {
	l := &loader{}
	c := Bootstrap{
		NATSURL:     l.RequireURL("NATS_URL", "nats", "tls"),
		TLSCertFile: l.Require("TLS_CERT_FILE"),
		TLSKeyFile:  l.Require("TLS_KEY_FILE"),
		TLSCAFile:   l.Require("TLS_CA_FILE"),
	}
	return c, l.err()
}

// Corpus is the Y1 WAL miner's environment (calipers corpus) — the same S3 quad
// every beat's broker path requires, unconditionally required here since the
// miner has no offline mode to fall back to. THUMP_SEAL_KEY is deliberately
// not a field: unseal.KeyFromEnv reads it instead, carrying the
// kubectl-double-base64 hint an operator running this by hand needs and a
// generic RequireBase64Key error does not.
type Corpus struct {
	S3Endpoint  string // S3_ENDPOINT — required
	S3Bucket    string // S3_BUCKET — required
	S3AccessKey string // S3_ACCESS_KEY — required
	S3SecretKey string // S3_SECRET_KEY — required
}

// LoadCorpus reads the WAL miner's environment once. See loadBrokerStore's
// comment for why S3_ENDPOINT allows both http and https.
func LoadCorpus() (Corpus, error) {
	l := &loader{}
	c := Corpus{
		S3Endpoint:  l.RequireURL("S3_ENDPOINT", "http", "https"),
		S3Bucket:    l.Require("S3_BUCKET"),
		S3AccessKey: l.Require("S3_ACCESS_KEY"),
		S3SecretKey: l.Require("S3_SECRET_KEY"),
	}
	return c, l.err()
}

// loader accumulates every missing-required var instead of stopping at the
// first — each Require/Optional call reads its var once; err joins whatever
// Require calls came back empty into a single error.
type loader struct {
	errs []error
}

func (l *loader) Require(name string) string {
	v := os.Getenv(name)
	if v == "" {
		l.errs = append(l.errs, fmt.Errorf("%s is required", name))
	}
	return v
}

func (l *loader) Optional(name string) string {
	return os.Getenv(name)
}

// OptionalOneOf reads name like Optional, then rejects a set value outside
// allowed — exact match, no case folding or trimming, so a near-miss fails
// the deploy at load time instead of silently selecting whatever the zero
// value means downstream.
func (l *loader) OptionalOneOf(name string, allowed ...string) string {
	v := os.Getenv(name)
	if v != "" && !slices.Contains(allowed, v) {
		l.errs = append(l.errs, fmt.Errorf("%s: must be one of %v, got %q", name, allowed, v))
	}
	return v
}

// OptionalDuration reads name as a time.Duration, defaulting to def when
// unset. A set-but-unparseable value is a configuration error, same as a
// missing Require, not a silent fall-back to def.
func (l *loader) OptionalDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid duration %q: %w", name, v, err))
		return def
	}
	return d
}

// RequireURL reads name like Require, then rejects a scheme outside
// allowed — a plaintext WAL endpoint fails at load time instead of shipping
// transcripts over the wire in the clear.
func (l *loader) RequireURL(name string, allowed ...string) string {
	v := l.Require(name)
	if v == "" {
		return v
	}
	l.checkScheme(name, v, allowed)
	return v
}

// OptionalURL is RequireURL for a URL whose absence is legal.
func (l *loader) OptionalURL(name string, allowed ...string) string {
	v := l.Optional(name)
	if v == "" {
		return v
	}
	l.checkScheme(name, v, allowed)
	return v
}

// OptionalBool reads 'name' as a bool, defaulting to false when unset.
func (l *loader) OptionalBool(name string) bool {
	v := os.Getenv(name)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid bool %q: %w", name, v, err))
		return false
	}
	return b
}

// checkScheme appends an error naming name if v's scheme isn't one of
// allowed — including no scheme at all, which would otherwise let the SDK
// choose the wire.
func (l *loader) checkScheme(name, v string, allowed []string) {
	u, err := url.Parse(v)
	if err == nil {
		if slices.Contains(allowed, u.Scheme) {
			return
		}
	}
	l.errs = append(l.errs, fmt.Errorf("%s: scheme must be one of %v, got %q", name, allowed, v))
}

// RequireBase64Key reads name as base64 and requires it decode to exactly n
// bytes — decode failure or wrong length is a configuration error, same as a
// missing Require, not a runtime surprise inside sealbox.
func (l *loader) RequireBase64Key(name string, n int) []byte {
	v := l.Require(name)
	if v == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid base64: %w", name, err))
		return nil
	}
	if len(b) != n {
		l.errs = append(l.errs, fmt.Errorf("%s: want %d bytes, got %d", name, n, len(b)))
		return nil
	}
	return b
}

func (l *loader) err() error {
	return errors.Join(l.errs...)
}
