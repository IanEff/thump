// Package config is each beat's typed, validated environment — a leaf
// package (leaf_test.go) so it can never grow an import into a beat's
// internals. Load reads every var exactly once and reports every missing
// required one together, so a bad deploy fails with a complete list instead
// of one redeploy per var discovered.
package config

import (
	"errors"
	"fmt"
	"os"
)

// Clank is clank's environment, one field per var its Main used to read ad
// hoc with os.Getenv.
type Clank struct {
	AnthropicAPIKey  string // ANTHROPIC_API_KEY — required
	ActionCatalog    string // ACTION_CATALOG - required; the authored action catalog YAML
	PromURL          string // PROM_URL — optional; empty disables the metrics tool
	EvidenceQueries  string // EVIDENCE_QUERIES — optional; only meaningful with PromURL set
	LokiURL          string // LOKI_URL — optional; empty disables the loki tool
	WhirCatalog      string // WHIR_CATALOG — optional; pairs with WhirStateQueries
	WhirStateQueries string // WHIR_STATE_QUERIES — optional; pairs with WhirCatalog
	Transcripts      string // CLANK_TRANSCRIPTS — optional; empty keeps turns in memory only
	Inbox            string // CLANK_INBOX — required only in the offline (non-broker) path
	Outbox           string // CLANK_OUTBOX — required only in the offline path
	Outcomes         string // CLANK_OUTCOMES — required only in the offline path
	WALDir           string // WAL_DIR — required only in the broker path
	S3Endpoint       string // S3_ENDPOINT — required only in the broker path
	S3Bucket         string // S3_BUCKET — required only in the broker path
	S3AccessKey      string // S3_ACCESS_KEY — required only in the broker path
	S3SecretKey      string // S3_SECRET_KEY — required only in the broker path
}

// LoadClank reads clank's environment once. broker is whether Main resolved
// a NATS_URL (lc.NATSURL != "" after beat.Start) — the offline dir-poll
// inbox/outbox/outcomes trio is only required when it didn't; the broker
// path never reads them. WAL_DIR and the S3 fields are the inverse — no WAL
// in the offline path, nothing to ship.
func LoadClank(broker bool) (Clank, error) {
	l := &loader{}
	c := Clank{
		AnthropicAPIKey:  l.Require("ANTHROPIC_API_KEY"),
		ActionCatalog:    l.Require("ACTION_CATALOG"),
		PromURL:          l.Optional("PROM_URL"),
		EvidenceQueries:  l.Optional("EVIDENCE_QUERIES"),
		LokiURL:          l.Optional("LOKI_URL"),
		WhirCatalog:      l.Optional("WHIR_CATALOG"),
		WhirStateQueries: l.Optional("WHIR_STATE_QUERIES"),
		Transcripts:      l.Optional("CLANK_TRANSCRIPTS"),
	}
	if broker {
		c.Inbox = l.Optional("CLANK_INBOX")
		c.Outbox = l.Optional("CLANK_OUTBOX")
		c.Outcomes = l.Optional("CLANK_OUTCOMES")
		c.WALDir = l.Require("WAL_DIR")
		c.S3Endpoint = l.Require("S3_ENDPOINT")
		c.S3Bucket = l.Require("S3_BUCKET")
		c.S3AccessKey = l.Require("S3_ACCESS_KEY")
		c.S3SecretKey = l.Require("S3_SECRET_KEY")
	} else {
		c.Inbox = l.Require("CLANK_INBOX")
		c.Outbox = l.Require("CLANK_OUTBOX")
		c.Outcomes = l.Require("CLANK_OUTCOMES")
	}
	return c, l.err()
}

// Hiss is hiss's environment. Policy is HISS_POLICY's raw path — hiss.go's
// own loadPolicy reads and parses it into a Policy struct; config stops at
// the validated string, the same division whir/contract draw between
// "where's the file" (env) and "what's in it" (the beat's own YAML parse).
type Hiss struct {
	Policy      string // HISS_POLICY — required
	Inbox       string // HISS_INBOX — required only in the offline (non-broker) path
	Outbox      string // HISS_OUTBOX — required only in the offline path
	WALDir      string // WAL_DIR — required only in the broker path
	S3Endpoint  string // S3_ENDPOINT — required only in the broker path
	S3Bucket    string // S3_BUCKET — required only in the broker path
	S3AccessKey string // S3_ACCESS_KEY — required only in the broker path
	S3SecretKey string // S3_SECRET_KEY — required only in the broker path
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
		Policy: l.Require("HISS_POLICY"),
	}
	if broker {
		h.Inbox = l.Optional("HISS_INBOX")
		h.Outbox = l.Optional("HISS_OUTBOX")
		h.WALDir = l.Require("WAL_DIR")
		h.S3Endpoint = l.Require("S3_ENDPOINT")
		h.S3Bucket = l.Require("S3_BUCKET")
		h.S3AccessKey = l.Require("S3_ACCESS_KEY")
		h.S3SecretKey = l.Require("S3_SECRET_KEY")
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
	WhirCatalog      string // WHIR_CATALOG — optional; pairs with WhirStateQueries
	WhirStateQueries string // WHIR_STATE_QUERIES — optional; pairs with WhirCatalog
	Traffic          string // RATTLE_TRAFFIC — optional; empty disables the Hubble traffic source
	Outbox           string // RATTLE_OUTBOX — optional even offline; unset means detections are logged, not published
	WatchPath        string // RATTLE_WATCH - required unconditionally
	WALDir           string // WAL_DIR — required only in the broker path
	S3Endpoint       string // S3_ENDPOINT — required only in the broker path
	S3Bucket         string // S3_BUCKET — required only in the broker path
	S3AccessKey      string // S3_ACCESS_KEY — required only in the broker path
	S3SecretKey      string // S3_SECRET_KEY — required only in the broker path
}

// LoadRattle reads rattle's environment once. broker is whether Main
// resolved a NATS_URL (lc.NATSURL != "" after beat.Start) — WAL_DIR and the
// S3 fields are only required in that path; RATTLE_OUTBOX is optional
// either way.
func LoadRattle(broker bool) (Rattle, error) {
	l := &loader{}
	r := Rattle{
		PromURL:          l.Require("PROM_URL"),
		WhirCatalog:      l.Optional("WHIR_CATALOG"),
		WhirStateQueries: l.Optional("WHIR_STATE_QUERIES"),
		WatchPath:        l.Require("RATTLE_WATCH"),
		Traffic:          l.Optional("RATTLE_TRAFFIC"),
		Outbox:           l.Optional("RATTLE_OUTBOX"),
	}
	if broker {
		r.WALDir = l.Require("WAL_DIR")
		r.S3Endpoint = l.Require("S3_ENDPOINT")
		r.S3Bucket = l.Require("S3_BUCKET")
		r.S3AccessKey = l.Require("S3_ACCESS_KEY")
		r.S3SecretKey = l.Require("S3_SECRET_KEY")
	}
	return r, l.err()
}

// Thump is thump's environment. WALDir backs both the orders and outcomes
// WAL publishers in the broker path (runBroker calls beat.NewWALPublisher
// twice against the one dir) — one field, two consumers.
type Thump struct {
	ActionCatalog string // ACTION_CATALOG - required; the authored action catalog YAML
	Inbox         string // THUMP_INBOX — required only in the offline (non-broker) path
	Outbox        string // THUMP_OUTBOX — required only in the offline path
	WALDir        string // WAL_DIR — required only in the broker path
	S3Endpoint    string // S3_ENDPOINT — required only in the broker path
	S3Bucket      string // S3_BUCKET — required only in the broker path
	S3AccessKey   string // S3_ACCESS_KEY — required only in the broker path
	S3SecretKey   string // S3_SECRET_KEY — required only in the broker path
}

// LoadThump reads thump's environment once. broker is whether Main resolved
// a NATS_URL (lc.NATSURL != "" after beat.Start) — the offline dir-poll
// inbox/outbox pair is only required when it didn't; WAL_DIR is the inverse.
func LoadThump(broker bool) (Thump, error) {
	l := &loader{}
	t := Thump{
		ActionCatalog: l.Require("ACTION_CATALOG"),
	}
	if broker {
		t.Inbox = l.Optional("THUMP_INBOX")
		t.Outbox = l.Optional("THUMP_OUTBOX")
		t.WALDir = l.Require("WAL_DIR")
		t.S3Endpoint = l.Require("S3_ENDPOINT")
		t.S3Bucket = l.Require("S3_BUCKET")
		t.S3AccessKey = l.Require("S3_ACCESS_KEY")
		t.S3SecretKey = l.Require("S3_SECRET_KEY")
	} else {
		t.Inbox = l.Require("THUMP_INBOX")
		t.Outbox = l.Require("THUMP_OUTBOX")
	}
	return t, l.err()
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

func (l *loader) err() error {
	return errors.Join(l.errs...)
}
