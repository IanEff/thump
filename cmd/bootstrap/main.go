package main

import (
	"os"

	"github.com/ianeff/thump/internal/bootstrap"
)

func main() {
	os.Exit(bootstrap.Main(os.Args[1:], os.Stderr))
}
