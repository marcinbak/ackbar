package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var rawVersion string

// Version follows date-based versioning YYYYMMDD.rev loaded from the VERSION file
var Version = strings.TrimSpace(rawVersion)
