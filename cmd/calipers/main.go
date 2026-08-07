// Command calipers is the operator's read and ack surface: it reads emitted
// state and can publish a human approval, and it ships only as a binary, never
// as a container image. Of its nine verbs only approve and force publish
// anything, and neither can reach an executor or the kill switch.
package main

import (
	"os"

	"github.com/ianeff/thump/internal/calipers"
)

func main() {
	os.Exit(calipers.Main(os.Args[1:], os.Stdout, os.Stderr))
}
