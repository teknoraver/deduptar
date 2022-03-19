// © Copyright Deduptar Authors (see CONTRIBUTORS.md)
package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"nontrivialpursuit.org/deduptar/tarops"
)

func halp(text string) {
	fmt.Fprintf(os.Stderr, "%s\nRun 'deduptar --help' for a command synopsis.\n", text)
	os.Exit(1)
}

func seppuku(err error) {
	fmt.Fprintf(os.Stderr, "Fatal: %v\n", err)
	os.Exit(1)
}

func fully_qualify_path(thepath *string) string {
	cwd, _ := os.Getwd()
	if len(*thepath) == 0 {
		return cwd
	}
	specced_path := filepath.Clean(*thepath)
	if filepath.IsAbs(specced_path) {
		return specced_path
	}
	return filepath.Join(cwd, specced_path)
}

func chatty(awaiter *sync.WaitGroup, progress *(chan tarops.ProgressMessage), verbose *bool) {
	defer awaiter.Done()
	for message := range *progress {
		if *verbose && (message.Type == tarops.VerboseMessage) {
			fmt.Fprintln(os.Stdout, message.Message)
		} else {
			if message.Type == tarops.WarningMessage {
				fmt.Fprintf(os.Stderr, "Warning: %s\n", message.Message)
			}
		}
	}
}

func RunCLI() {
	verbose := flag.Bool("v", false, "Verbosely list files processed.")
	follow_symlinks := flag.Bool("follow-symlinks", false, "Turn on the following of symlinks; archive the symlink destination rather than the symlink itself.")
	no_recursion := flag.Bool("no-recursion", false, "Turn off recursing into directories.")
	same_owner := flag.Bool("same-owner", false, "As in GNU Tar: upon extraction, set file ownership as recorded in the archive.")
	freakout := flag.Bool("freakout", false, "Normally, upon encountering an error during extraction, deduptar will print a warning to stderr, and will continue operations. But with --freakout specified, it will exit immediately. In either case, the process exit code will be nonzero.")
	version := flag.Bool("version", false, "Print version banner and exit.")
	license := flag.Bool("license", false, "Print software license and exit.")
	contributors := flag.Bool("contributors", false, "Print contributors and exit.")
	src_archive := flag.String("x", "", "Tar file to extract from")
	dst_archive := flag.String("c", "", "Tar file to create")
	change_dir := flag.String("C", "", "Extract archive contents to DIR rather than to the current working directory.")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `
%s

Usage:
  Archiving:
    deduptar [-v] -c archive.tar [--follow-symlinks] [--no-recursion] FILES...
  Extraction:
    deduptar [-v] -x archive.tar [-C DIR] [--same-owner] [--freakout]

  General options:
    -v
      Verbosely list files processed to stdout.
    --version
      Print version banner and exit.
    --license
      Print software license and exit.
    --contributors
      Print code contributors and exit.

  Archiving options:
    -c archive.tar
      Tar file to create. Will be overwritten if it already exists.
    --follow-symlinks
      Resolve symlinks; this archives the symlink destination rather than the symlink itself.
    --no-recursion
      Turn off recursing into directories.

  Extraction options:
    -x archive.tar
      Tar file to extract from.
    -C DIR
      Extract archive contents to DIR rather than to the current working directory.
    --freakout
      Normally, upon encountering a nonfatal error, deduptar will print a warning to stderr,
      and will continue operations. But with --freakout specified, it will exit immediately.
      In either case, the process exit code will be nonzero.
    --same-owner
      As in GNU tar: upon extraction, set file ownership as recorded in the archive.

`, deduptar_banner)
	}

	flag.Parse()

	switch {
	case *version:
		fmt.Print(deduptar_banner)
	case *license:
		fmt.Print(license)
	case *contributors:
		fmt.Print(contributors)
	default:
		dst_archive_is_specced, src_archive_is_specced, change_dir_is_specced := len(*dst_archive) > 0, len(*src_archive) > 0, len(*change_dir) > 0
		archive_progress := make(chan tarops.ProgressMessage)
		awaiter := new(sync.WaitGroup)
		awaiter.Add(1)
		go chatty(awaiter, &archive_progress, verbose)

		if !(dst_archive_is_specced || src_archive_is_specced) {
			halp("Fatal: Neither an archive to extract from, nor an archive to create have been specified.")
		} else if dst_archive_is_specced && src_archive_is_specced {
			halp("Fatal: Both an archive to extract from, and an archive to create have been specified.")
		} else if dst_archive_is_specced {
			if change_dir_is_specced {
				halp("Fatal: -C is only valid in combination with -x (extract).")
			}
			if *same_owner {
				halp("Fatal: --same-owner is only valid in combination with -x (extract).")
			}
			if *freakout {
				halp("Fatal: --freakout is only valid in combination with -x (extract).")
			}
			abort_err := tarops.Archive(dst_archive, flag.Args(), follow_symlinks, no_recursion, &archive_progress)
			close(archive_progress)
			awaiter.Wait()
			if abort_err != nil {
				seppuku(abort_err)
			}
		} else if src_archive_is_specced {
			tarfile, err := os.Open(*src_archive)
			if err != nil {
				seppuku(err)
			}
			defer tarfile.Close()
			allgood, abort_err := tarops.Extract(fully_qualify_path(change_dir), tarfile, same_owner, freakout, &archive_progress)
			close(archive_progress)
			awaiter.Wait()
			if abort_err != nil {
				seppuku(abort_err)
			} else {
				if !allgood {
					fmt.Fprintln(os.Stderr, "Warning: One or more errors were encountered, and ignored.")
					os.Exit(1)
				}
			}
		}
	}
}
