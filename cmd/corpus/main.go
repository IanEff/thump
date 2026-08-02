package main

import (
	"os"

	"github.com/ianeff/thump/internal/corpus"
)

func main() {
	os.Exit(corpus.Main(os.Args[1:], os.Stdout, os.Stderr))
}
