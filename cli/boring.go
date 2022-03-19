// © Copyright Deduptar Authors (see CONTRIBUTORS.md)
package cli

import (
	_ "embed"
	"fmt"
)

//go:embed LICENSE.txt
var license string

//go:embed CONTRIBUTORS.md
var contributors string

const (
	deduptar_banner_template = `
Deduptar — a tar for Linux 4.5+ (archiving) / 5.6+ (extracting) which uses the FICLONE ioctl to share data between the tar archive and source/destination files.

Created by: Wicher Minnaard
Homepage  : https://curiosities.nontrivialpursuit.org/deduptar-the-tar-that-deduplicates.html
Version   : %s
`
)

var (
	deduptar_banner = fmt.Sprintf(deduptar_banner_template, deduptar_version)
)
