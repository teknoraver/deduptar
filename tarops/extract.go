// © Copyright Deduptar Authors (see CONTRIBUTORS.md)
package tarops

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func copyrange_from_archive(src_fd int, src_offset int64, len int, dst_fd int, dst_offset int64, full_path string) error {
	written, copy_err := unix.CopyFileRange(src_fd, &src_offset, dst_fd, &dst_offset, len, 0)
	if written != len {
		return fmt.Errorf(error_writesize, "writing", full_path, written, len)
	}
	if copy_err != nil {
		return errorDuringOp{Path: full_path, Op: "copy_file_range()", Err: copy_err}
	}
	return nil
}

func getdirhandle(basedir_handle int, path string) (dirhandle int, err error) {
	dirhandle, err = unix.Openat2(basedir_handle, path, &openat_chroot_thatshow)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			// Dir might not exist yet. Get a handle on its parent, then mkdirat it and return its handle
			itsparent := filepath.Dir(path)
			itsname := filepath.Base(path)
			parent_dirhandle, inner_err := getdirhandle(basedir_handle, itsparent)
			if inner_err != nil {
				return
			}
			if mkdir_err := unix.Mkdirat(parent_dirhandle, itsname, 0o777); mkdir_err != nil {
				return dirhandle, errorDuringOp{Path: path, Op: "mkdirat()", Err: mkdir_err}
			}
			return getdirhandle(basedir_handle, path)
		} else {
			return dirhandle, errorDuringOp{Path: path, Op: "openat2()", Err: err}
		}
	}
	return
}

func makedev(maj uint, min uint) (packeddev uint64) {
	// From https://github.com/appc/spec/blob/v0.8.11/pkg/device/device_linux.go
	return uint64(min&0xff) | (uint64(maj&0xfff) << 8) |
		((uint64(min) & ^uint64(0xff)) << 12) |
		((uint64(maj) & ^uint64(0xfff)) << 32)
}

func extract_one(extractdir_fd int, full_path *string, header *tar.Header, tarfile *os.File, tar_reader *tar.Reader, dir_timestamps *map[string][]unix.Timeval, same_owner *bool) (was_cloned bool, abort_err error) {
	destfile_dirhandle, abort_err := getdirhandle(extractdir_fd, filepath.Dir(filepath.Clean(header.Name)))
	if abort_err != nil {
		return
	}
	thing_basename := filepath.Base(header.Name)
	whatsthere_stat := new(unix.Stat_t)
	staterr := unix.Fstatat(destfile_dirhandle, thing_basename, whatsthere_stat, unix.AT_SYMLINK_NOFOLLOW)
	if staterr == nil {
		return was_cloned, targetAlreadyExists{Path: *full_path}
	}
	if !errors.Is(staterr, unix.ENOENT) {
		return was_cloned, errorDuringOp{Path: *full_path, Op: "stat()", Err: staterr}
	}

	var outfile_handle int
	var extra_openflags int
	switch header.Typeflag {
	case tar.TypeReg:
		var openat_err error
		outfile_handle, openat_err = unix.Openat(destfile_dirhandle, thing_basename, os.O_EXCL|os.O_CREATE|unix.O_WRONLY|unix.O_LARGEFILE|unix.AT_SYMLINK_NOFOLLOW, uint32(header.Mode))
		if openat_err != nil {
			return was_cloned, errorDuringOp{Path: *full_path, Op: "openat()", Err: openat_err}
		}
		tar_pos := tell(tarfile)
		if tar_pos%FS_PAGESIZE == 0 {
			// ficloneable
			page_spill := header.Size % FS_PAGESIZE // Leftovers, not making up a full page
			ficlonerange := unix.FileCloneRange{
				Src_fd:      int64(tarfile.Fd()),
				Src_offset:  uint64(tar_pos),
				Src_length:  uint64(header.Size) - uint64(page_spill),
				Dest_offset: 0,
			}
			clone_err := unix.IoctlFileCloneRange(outfile_handle, &ficlonerange)
			if clone_err != nil {
				if errors.Is(clone_err, syscall.EXDEV) {
					// ficlone cross-device not possible, copyrange instead
					copyrange_from_archive(int(tarfile.Fd()), tar_pos, int(header.Size), outfile_handle, 0, *full_path)
				} else {
					return was_cloned, errorDuringOp{Path: *full_path, Op: "ficlone", Err: clone_err}
				}
			} else {
				if page_spill > 0 {
					// Still some stuff left to copy
					copyrange_from_archive(int(tarfile.Fd()), tar_pos, int(page_spill), outfile_handle, header.Size-page_spill, *full_path)
				}
				was_cloned = true
			}
		} else {
			copyrange_from_archive(int(tarfile.Fd()), tar_pos, int(header.Size), outfile_handle, 0, *full_path)
		}
		unix.Fsync(outfile_handle)
	case tar.TypeDir:
		if err := unix.Mkdirat(destfile_dirhandle, thing_basename, uint32(header.Mode)); err != nil {
			return was_cloned, errorDuringOp{Path: *full_path, Op: "mkdirat()", Err: err}
		}
		extra_openflags = unix.O_DIRECTORY
	case tar.TypeSymlink:
		if err := unix.Symlinkat(header.Linkname, destfile_dirhandle, thing_basename); err != nil {
			return was_cloned, errorDuringOp{Path: *full_path, Op: "symlinkat()", Err: err}
		}
	case tar.TypeBlock, tar.TypeChar:
		mode := uint32(header.Mode)
		switch header.Typeflag {
		case tar.TypeBlock:
			mode = mode | syscall.S_IFBLK
		case tar.TypeChar:
			mode = mode | syscall.S_IFCHR
		}
		if err := unix.Mknodat(destfile_dirhandle, thing_basename, mode, int(makedev(uint(header.Devmajor), uint(header.Devminor)))); err != nil {
			return was_cloned, &errorDuringOp{Path: *full_path, Op: "mknodat()", Err: err}
		}
	case tar.TypeFifo:
		if err := unix.Mkfifoat(destfile_dirhandle, thing_basename, uint32(header.Mode)); err != nil {
			return was_cloned, errorDuringOp{Path: *full_path, Op: "mkfifoat()", Err: err}
		}
		extra_openflags = unix.O_NONBLOCK
	case tar.TypeLink:
		var linkdest_dirhandle int
		linkdest_dirhandle, abort_err = getdirhandle(extractdir_fd, filepath.Dir(filepath.Clean(header.Linkname)))
		if abort_err != nil {
			return
		}
		if linkat_err := unix.Linkat(linkdest_dirhandle, filepath.Base(header.Linkname), destfile_dirhandle, thing_basename, 0); linkat_err != nil {
			return was_cloned, errorDuringOp{Path: *full_path, Op: "linkat()", Err: linkat_err}
		}
	default:
		return was_cloned, &unhandledRecord{Typeflag: header.Typeflag, Path: *full_path}
	}

	timestamp := []unix.Timeval{{Sec: header.AccessTime.Unix(), Usec: int64(header.AccessTime.Nanosecond())}, {Sec: header.ModTime.Unix(), Usec: int64(header.ModTime.Nanosecond())}}

	if header.Typeflag == tar.TypeSymlink {
		// Special case for symlinks
		// - we cannot get a file descriptor for a symlink to apply Fchown & Futimes to :-(,
		// - there's no chmodding a symlink
		// - and other variants of calls are required to not dereference them
		if *same_owner {
			unix.Fchownat(destfile_dirhandle, thing_basename, header.Uid, header.Gid, unix.AT_SYMLINK_NOFOLLOW)
			if err := unix.Lchown(*full_path, header.Uid, header.Gid); err != nil {
				return was_cloned, errorDuringOp{Path: *full_path, Op: "chown()", Err: err}
			}
		}
		unix.Lutimes(*full_path, timestamp)
	} else {
		// if we don't have a handle yet (if the FS entity cannot be created through openat2 - eg anything but an ordinary file),
		// acquire one so that we can set metadata.
		if outfile_handle == 0 {
			var openat_err error
			outfile_handle, openat_err = unix.Openat(destfile_dirhandle, thing_basename, unix.AT_SYMLINK_NOFOLLOW|extra_openflags, 0)
			if openat_err != nil {
				return was_cloned, errorDuringOp{Path: *full_path, Op: "reopening", Err: openat_err}
			}
		}
		if err := unix.Fchmod(outfile_handle, uint32(header.Mode)); err != nil {
			return was_cloned, errorDuringOp{Path: *full_path, Op: "fchmod()", Err: err}
		}
		if *same_owner {
			if err := unix.Fchown(outfile_handle, header.Uid, header.Gid); err != nil {
				return was_cloned, errorDuringOp{Path: *full_path, Op: "fchown()", Err: err}
			}
		}
		unix.Futimes(outfile_handle, timestamp)
		unix.Close(outfile_handle)
	}

	if header.Typeflag == tar.TypeDir {
		// Creating files in this directory later on is going to change its mtimes.
		// We need to record the mtime so that we can restore it later in such cases.
		(*dir_timestamps)[*full_path] = timestamp
	} else {
		if parent_dir_timestamps := (*dir_timestamps)[filepath.Dir(*full_path)]; parent_dir_timestamps != nil {
			// This node was created in a parent dir for which we have set mtimes.
			// Creating the node updated those mtimes, so we need to restore them.
			unix.Futimes(destfile_dirhandle, parent_dir_timestamps)
		}
	}

	unix.Close(destfile_dirhandle)
	return was_cloned, nil
}

func Extract(extractdir string, tarfile *os.File, same_owner *bool, freakout *bool, archive_progress *(chan ProgressMessage)) (allgood bool, abort_err error) {
	tar_reader := tar.NewReader(tarfile)
	dir_timestamps := make(map[string][]unix.Timeval)
	allgood = true
	extractdir_fd, err := unix.Openat(unix.AT_FDCWD, extractdir, unix.O_PATH|unix.O_DIRECTORY, 0)
	if err != nil {
		abort_err = errorDuringOp{Path: extractdir, Op: "openat()", Err: err}
		return
	}

records_loop:
	for {
		header, err := tar_reader.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			abort_err = errorDuringOp{Path: tarfile.Name(), Op: "Next()", Err: err}
			return
		}
		full_path := filepath.Clean(filepath.Join(extractdir, header.Name))

		was_cloned, extract_err := extract_one(extractdir_fd, &full_path, header, tarfile, tar_reader, &dir_timestamps, same_owner)

		if !*freakout {
			var unhandledRecordErr unhandledRecord
			if errors.As(extract_err, &unhandledRecordErr) {
				warning_message(archive_progress, fmt.Sprintf("Skipping: %v", unhandledRecordErr))
				allgood = false
				continue records_loop
			}
			var targetAlreadyExistsErr targetAlreadyExists
			if errors.As(extract_err, &targetAlreadyExistsErr) {
				warning_message(archive_progress, fmt.Sprintf("Skipping: %v", targetAlreadyExistsErr))
				allgood = false
				continue records_loop
			}
		} else {
			if extract_err != nil {
				abort_err = extract_err
				return
			}
		}

		var recordtype string
		if was_cloned {
			recordtype = "file (cloned)"
		} else {
			recordtype = humanize_tar_recordtype(header.Typeflag)
		}
		verbose_message(archive_progress, fmt.Sprintf("%-15s\t%s", recordtype, header.Name))
	}
	return
}
