// © Copyright Deduptar Authors (see CONTRIBUTORS.md)
package tarops

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func ficlone_into_archive(srcfile *os.File, archive *os.File, header *tar.Header) error {
	pos := tell(archive)
	ficlonerange := unix.FileCloneRange{
		Src_fd:      int64(srcfile.Fd()),
		Src_offset:  0,
		Src_length:  0,
		Dest_offset: uint64(pos),
	}
	if clone_err := unix.IoctlFileCloneRange(int(archive.Fd()), &ficlonerange); clone_err != nil {
		return clone_err
	} else {
		newpos, _ := archive.Seek(0, io.SeekEnd)
		if written := newpos - pos; written != header.Size {
			return fmt.Errorf(error_writesize, "reading", header.Name, written, header.Size)
		}
		pad512(archive, newpos)
	}
	return nil
}

func copyrange_into_archive(srcfile *os.File, tarfile *os.File, header *tar.Header) error {
	pos := tell(tarfile)
	var infile_offset int64 = 0
	written, copy_err := unix.CopyFileRange(int(srcfile.Fd()), &infile_offset, int(tarfile.Fd()), &pos, int(header.Size), 0)
	if copy_err != nil {
		return errorDuringOp{Path: srcfile.Name(), Op: "copy_file_range()", Err: copy_err}
	}
	if int64(written) != header.Size {
		return fmt.Errorf(error_writesize, "writing", srcfile.Name(), written, header.Size)
	}
	return pad512(tarfile, pos)
}

func tarwrite(tarfile *os.File, header *tar.Header) (was_cloned bool, abort_err error) {
	was_cloned = false
	pos_header := tell(tarfile)
	var pristine_header_buf bytes.Buffer
	pristine_tarbuf := tar.NewWriter(&pristine_header_buf)
	pristine_tarbuf.WriteHeader(header)
	if header.Typeflag != tar.TypeReg || header.Size == 0 {
		// Just write the normal header, and no body. No tricks required.
		_, abort_err = pristine_header_buf.WriteTo(tarfile)
		return
	}
	infile, abort_err := os.OpenFile(header.Name, os.O_RDONLY|unix.O_NOATIME, 0)
	if abort_err != nil {
		if !errors.Is(abort_err, unix.EPERM) {
			return
		} else {
			// unprivileged users can only request O_NOATIME for their own files
			infile, abort_err = os.OpenFile(header.Name, os.O_RDONLY, 0)
			if abort_err != nil {
				return
			}
			defer infile.Close()
		}
	}
	header_growth, padded_header_buf := pad_tarheader(header, pos_header)
	if int64(header_growth) > header.Size {
		// Copying rather than cloning as the file's size is smaller than its clone-required header alignment padding would be
		if _, abort_err = pristine_header_buf.WriteTo(tarfile); abort_err != nil {
			return
		}
		abort_err = copyrange_into_archive(infile, tarfile, header)
	} else {
		// Clone time
		if _, abort_err = padded_header_buf.WriteTo(tarfile); abort_err != nil {
			return
		}
		ficlone_err := ficlone_into_archive(infile, tarfile, header)
		if ficlone_err != nil {
			log.Fatalln(ficlone_err)
			if errors.Is(ficlone_err, syscall.EXDEV) {
				// Uncloneable; cross-filesystem. Not fatal!
				if header_growth > 0 {
					// We used header padding so that we could clone, but it didn't work out. Be tidy and use the unpadded header then.
					// Roll back & re-apply.
					tarfile.Seek(pos_header, io.SeekStart)
					tarfile.Truncate(pos_header)
					if _, abort_err = pristine_header_buf.WriteTo(tarfile); abort_err != nil {
						return
					}
				}
				abort_err = copyrange_into_archive(infile, tarfile, header)
			}
		} else {
			was_cloned = true
		}
	}
	return
}

func pad_tarheader(header *tar.Header, header_offset int64) (header_growth int, header_buffer *bytes.Buffer) {
	// Measure the size of a pristine header block
	var pristine_header_buf bytes.Buffer
	pristine_tarbuf := tar.NewWriter(&pristine_header_buf)
	if ouch := pristine_tarbuf.WriteHeader(header); ouch != nil {
		log.Fatalf("error writing header: %s", ouch)
	}
	padout_size := int(FS_PAGESIZE - ((header_offset + int64(pristine_header_buf.Len())) % FS_PAGESIZE))
	if padout_size == 0 {
		// No padding tricks required
		return 0, &pristine_header_buf
	}
	// Pad the tar header out for page-alignment of the file body.
	// First measure the size of the header with PAX header overhead.
	header.PAXRecords = make(map[string]string)
	header.PAXRecords[pax_padding_headerkey] = pax_filler_char // need to have something here, as special "delete previous pax header" semantics apply to a zero-length value
	var padded_header_buffer bytes.Buffer
	padded_tarbuf := tar.NewWriter(&padded_header_buffer)
	if ouch := padded_tarbuf.WriteHeader(header); ouch != nil {
		log.Fatalf("error writing header: %s", ouch)
	}
	paxed_size := padded_header_buffer.Len()

	// Now we know the minimum header size, we'll need to find the appropriate PAX header value size to pad the record out.
	// A complicating factor is that the size of the record is dependent
	// on... its own size, as its own size is encoded string-decimally in a variable-length field in the record itself!
	// See https://web.archive.org/web/20230706143859/https://www.ibm.com/docs/en/zos/2.3.0?topic=SSLTBW_2.3.0/com.ibm.zos.v2r3.bpxa500/paxex.html
	left_to_pad := FS_PAGESIZE - ((header_offset + int64(paxed_size)) % FS_PAGESIZE)
	if left_to_pad == 0 {
		// Coincidentally spot on with a PAX value of length 1
		return padded_header_buffer.Len() - pristine_header_buf.Len(), &padded_header_buffer
	}
	current_pax_lengthfield_width := 0
	current_pax_header_and_value_length := pax_header_overhead + len(pax_padding_headerkey) + len(pax_filler_char)
	for {
		paxed_lengthfield_width_with_length := int(math.Ceil(math.Log10(float64(current_pax_lengthfield_width + current_pax_header_and_value_length))))
		if current_pax_lengthfield_width == paxed_lengthfield_width_with_length {
			break
		}
		current_pax_lengthfield_width = paxed_lengthfield_width_with_length
	}
	// Thus when we add more padding, we need to take into account that that could potentially change the width of the length field — if so, we'll need to decrease the
	// amount of padding accordingly.
	// To put it differently, we could be increasing the size of the total header by 2, just by adding 1 more character to the PAX value...
	projected_pax_lengthfield_width := 0
	for {
		paxed_lengthfield_width_with_length := int(math.Ceil(math.Log10(float64(current_pax_lengthfield_width + int(left_to_pad) + pax_header_overhead + len(pax_padding_headerkey)))))
		if projected_pax_lengthfield_width == paxed_lengthfield_width_with_length {
			break
		}
		projected_pax_lengthfield_width = paxed_lengthfield_width_with_length
	}
	// Subtract the field width difference (before / after padding) from the amount of padding to add.
	// *) There is an edge case where the downward-adjusted padding results in a shrunk width field. In that case, we won't be padding
	//    to the very edge of the 512-byte tar block, but then the tar-internal padding will pad it out to that boundary with NULLs.
	adjusted_to_pad := left_to_pad - int64(projected_pax_lengthfield_width-current_pax_lengthfield_width)
	header.PAXRecords[pax_padding_headerkey] = strings.Repeat(
		"X",
		1+int(adjusted_to_pad),
	)
	padded_header_buffer.Reset()

	padded_tarbuf = tar.NewWriter(&padded_header_buffer)
	if ouch := padded_tarbuf.WriteHeader(header); ouch != nil {
		log.Fatalf("error writing header: %s", ouch)
	}

	if targeted_headersize, created_headersize := padout_size+pristine_header_buf.Len(), padded_header_buffer.Len(); targeted_headersize != created_headersize {
		log.Fatalf("header padding miscalculation: wanted %d, got %d", targeted_headersize, created_headersize)
	}

	return padded_header_buffer.Len() - pristine_header_buf.Len(), &padded_header_buffer
}

func Archive(dst_archive *string, inpaths []string, follow_symlinks *bool, no_recurse *bool, archive_progress *(chan ProgressMessage)) (abort_err error) {
	visited_registry := make(map[nodeID]struct{})
	hardlink_registry := make(map[nodeID]string)
	outfile, abort_err := os.Create(*dst_archive)
	if abort_err != nil {
		return
	}
	defer outfile.Close()
	for _, inpath := range inpaths {
		if abort_err = archive_one_recursively(outfile, inpath, &visited_registry, &hardlink_registry, follow_symlinks, no_recurse, archive_progress); abort_err != nil {
			return
		}
	}
	abort_err = finalize_tar(outfile)
	return
}

func archive_one_recursively(tarfile *os.File, inpath string, visited_registry *map[nodeID]struct{}, hardlink_registry *map[nodeID]string, follow_symlinks *bool, no_recurse *bool, archive_progress *(chan ProgressMessage)) (abort_err error) {
	var finfo os.FileInfo
	var linktarget string

	if *follow_symlinks {
		finfo, abort_err = os.Stat(inpath)
	} else {
		finfo, abort_err = os.Lstat(inpath)
	}
	if abort_err != nil {
		return errorDuringOp{Path: inpath, Op: "stat()", Err: abort_err}
	}
	// the golang FileInfo structure doesn't have enough info (device & inode), we need to get the stat_t from under it
	unixstat, _ := finfo.Sys().(*syscall.Stat_t)
	thisnode := nodeID{unixstat.Dev, unixstat.Ino}
	if finfo.Mode().Type() == fs.ModeSymlink {
		// to store a symlink, we need to know its target too
		var readlink_err error
		linktarget, readlink_err = os.Readlink(inpath)
		if readlink_err != nil {
			return errorDuringOp{Path: inpath, Op: "readlink()", Err: readlink_err}
		}
	}
	header, headerify_err := tar.FileInfoHeader(finfo, linktarget)
	header.Format = tar.FormatPAX // for subsecond precision in timestamps
	if headerify_err != nil {
		return errorDuringOp{Path: inpath, Op: "FileInfoHeader", Err: headerify_err}
	}
	header.Name = path.Join(filepath.Dir(filepath.Clean(inpath)), finfo.Name())
	if finfo.Mode().IsDir() {
		header.Name += "/"
	}
	if unixstat.Nlink > 1 {
		// this potentially shares an inode with something we have encountered already, or may encounter later
		other_path, already_encountered := (*hardlink_registry)[thisnode]
		if already_encountered {
			header.Typeflag = tar.TypeLink
			header.Linkname = other_path
		} else {
			(*hardlink_registry)[thisnode] = header.Name
		}
	}
	was_cloned, abort_err := tarwrite(tarfile, header)
	if abort_err != nil {
		return
	}
	var recordtype string
	if was_cloned {
		recordtype = "file (cloned)"
	} else {
		recordtype = humanize_tar_recordtype(header.Typeflag)
	}
	verbose_message(archive_progress, fmt.Sprintf("%-14s\t%s", recordtype, header.Name))
	if finfo.Mode().IsDir() && !*no_recurse {
		thestat := new(unix.Stat_t)
		unix.Stat(header.Name, thestat)
		_, already_visited := (*visited_registry)[thisnode]
		if already_visited {
			warning_message(archive_progress, fmt.Sprintf("Skipping directory (already visited): %s", inpath))
		} else {
			(*visited_registry)[thisnode] = struct{}{}
			files, err := os.ReadDir(header.Name)
			if err != nil {
				return errorDuringOp{Path: inpath, Op: "readdir()", Err: err}
			}
			for _, file := range files {
				if abort_err = archive_one_recursively(tarfile, filepath.Join(header.Name, file.Name()), visited_registry, hardlink_registry, follow_symlinks, no_recurse, archive_progress); abort_err != nil {
					return
				}
			}
		}
	}
	return
}

func pad512(thefile *os.File, len int64) error {
	// Pad out to 512-byte record boundary
	if len == 0 {
		measured_len, err := thefile.Seek(0, io.SeekEnd)
		if err != nil {
			return errorDuringOp{Path: thefile.Name(), Op: "seek()", Err: err}
		}
		len = measured_len
	}
	if overboundary := len % TAR_BLOCKSIZE; overboundary > 0 {
		padding_required := TAR_BLOCKSIZE - overboundary
		if err := thefile.Truncate(len + padding_required); err != nil {
			return errorDuringOp{Path: thefile.Name(), Op: "ftruncate()", Err: err}
		}
	}
	if _, err := thefile.Seek(0, io.SeekEnd); err != nil {
		return errorDuringOp{Path: thefile.Name(), Op: "seek()", Err: err}
	}
	return nil
}

func finalize_tar(outfile *os.File) (abort_err error) {
	filelen, abort_err := outfile.Seek(0, io.SeekEnd)
	if abort_err != nil {
		return errorDuringOp{Path: outfile.Name(), Op: "seek()", Err: abort_err}
	}
	if abort_err := outfile.Truncate(filelen + 2*TAR_BLOCKSIZE); abort_err != nil {
		return errorDuringOp{Path: outfile.Name(), Op: "truncate()", Err: abort_err}
	}
	outfile.Close()
	return
}
