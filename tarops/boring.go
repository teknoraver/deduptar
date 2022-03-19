// © Copyright Deduptar Authors (see CONTRIBUTORS.md)
package tarops

import (
	"archive/tar"
	_ "embed"
	"fmt"

	"golang.org/x/sys/unix"
)

type unhandledRecord struct {
	Path     string
	Typeflag byte
}

type ProgressMessage struct {
	Type    int
	Message string
}

func (e unhandledRecord) Error() string {
	return fmt.Sprintf("Unhandled typeflag '%s' for '%s'", string(e.Typeflag), e.Path)
}

type targetAlreadyExists struct {
	Path string
}

func (e targetAlreadyExists) Error() string {
	return fmt.Sprintf("Target already exists: '%s'", e.Path)
}

type errorDuringOp struct {
	Path string
	Op   string
	Err  error
}

func (e errorDuringOp) Error() string {
	return fmt.Sprintf("During %s of '%s': %v", e.Op, e.Path, e.Err.Error())
}

const (
	VerboseMessage = iota
	WarningMessage
)

const (
	TAR_BLOCKSIZE = 512
	FS_PAGESIZE   = 4096 // OK, send me an angry email then

	error_writesize = "While %s file '%s': %d bytes, expected: %d\n"
	pax_filler_char = "X"

	pax_header_overhead = 1 + 1 + 1 // (1 space), (1 equals), (1 newline)
)

var (
	pax_padding_headerkey = "comment"

	openat_chroot_thatshow = unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_DIRECTORY,
		Mode:    0,
		Resolve: unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_IN_ROOT,
	}

	tar_typemap = map[byte]string{
		tar.TypeBlock:   "blockdev",
		tar.TypeChar:    "chardev",
		tar.TypeDir:     "directory",
		tar.TypeFifo:    "fifo",
		tar.TypeLink:    "hardlink",
		tar.TypeReg:     "file",
		tar.TypeSymlink: "symlink",
	}
)
