package main

import (
	"os"

	"github.com/ianeff/thump/internal/unseal"
)

func main() {
	os.Exit(unseal.Main(os.Args[1:], os.Stdout, os.Stderr))
}
