package main

import (
	"os"

	"github.com/ianeff/thump/internal/calipers"
)

func main() {
	os.Exit(calipers.Main(os.Args[1:], os.Stdout, os.Stderr))
}
