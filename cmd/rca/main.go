package main

import (
	"os"

	"github.com/ianeff/thump/internal/rca"
)

func main() {
	os.Exit(rca.Main(os.Args[1:], os.Stdout, os.Stderr))
}
