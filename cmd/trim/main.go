package main

import (
	"os"

	"github.com/ianeff/thump/internal/trim"
)

func main() {
	os.Exit(trim.Main(os.Args[1:], os.Stdout, os.Stderr))
}
