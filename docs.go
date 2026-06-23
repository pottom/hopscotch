package main

import (
	_ "embed"

	"hopscotch/cmd"
)

//go:embed README.md
var readmeContent []byte

func init() {
	cmd.ReadmeContent = readmeContent
}
