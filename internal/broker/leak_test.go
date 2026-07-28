package broker_test

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain carries this package's two process-wide test concerns.
//
// $JS_KEY: nats-server's conf parser resolves variable references against the
// process environment and fails the whole parse when one is missing. In the
// cluster the real key arrives via envFrom; these tests read the permission
// table, never the key, so the value only has to lex as a single token — the
// parser reads a variable's value as config text, so anything containing a
// space fails a second time and more confusingly than the first.
//
// goleak: a connection that retries forever is a goroutine that runs forever
// if nobody closes it, so this package is exactly where a missed close stops
// being theoretical.
func TestMain(m *testing.M) {
	if err := os.Setenv("JS_KEY", "dummyjetstreamkey"); err != nil {
		panic(err)
	}
	goleak.VerifyTestMain(m)
}
