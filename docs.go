package main

import (
	_ "embed"

	"github.com/pottom/hopscotch/cmd"
)

//go:embed README.md
var readmeContent []byte

func init() {
	cmd.ReadmeContent = readmeContent
}
