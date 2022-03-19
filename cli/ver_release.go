// © Copyright Deduptar Authors (see CONTRIBUTORS.md)
//go:build release

package cli

import (
	_ "embed"
)

//go:embed version.txt
var deduptar_version string
