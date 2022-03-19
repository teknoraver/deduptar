
# Deduptar

A tar for Linux 4.5+ (archiving) / 5.6+ (extracting) which uses the `FICLONE` ioctl to share data between the tar archive and source/destination files.

For background info, head [here](https://curiosities.nontrivialpursuit.org/deduptar-the-tar-that-deduplicates.html).


You can [download deduptar here](https://nontrivialpursuit.org/deduptar_releases/deduptar).

Alternatively, you can build and install it yourself by running `go install -v "nontrivialpursuit.org/deduptar@latest"` after which, in the common case, you'll have a `deduptar` executable in your `~/go/bin` ready to go.

### What is it

Some Linux filesystems (currently btrfs and XFS) support deduplicating filesystem file pages, that is, they allow files with (partially) identical contents to share the storage for that content, so that it only needs to be stored once. This allows file content to be deduplicated. There are many tools that deduplicate existing file contents, such as [bees](https://github.com/Zygo/bees) and [duperemove](https://github.com/markfasheh/duperemove). These kind of tools deduplicate ex post facto — first you'll write redundant data, and only once you've done so, these tools will be able to find and deduplicate it (and then only if your redundant data is page-aligned).

Deduptar is different. It creates and extracts tar archives just like [GNU tar](https://www.gnu.org/software/tar/) or [BSD tar](https://www.libarchive.org/), except that:

1. when archiving, it "clones in" file data into the tar archive. In a way, it "deduplicates into" the tar archive. This means that the data of the file that deduptar is archiving is actually neither read in, nor is it written out; the underlying filesystem simply created some bookkeeping to make a certain part of the archive refer to the same data storage as that of the archivee file. Thus you can pack up 1 GB of files while you only have 10 MB of free space left to store the archive.

2. when unarchiving, it "clones out" parts of the tar archive as files in the filesystem. Thus you can unpack that 1 GB tarball while you only have 10 MB of free space. A tar archive created with deduptar will be full of thus clonable files, but tar archives created with other tools will typically not be as for those there's an average probability of just 1 in 8 of file contents coincidentally starting neatly on a filesystem page boundary — read on to learn how deduptar manages to align files inside the archive on the filesystem page boundaries.

#### Show me!

```
# Create a file with 100 MB of random bytes
$ dd if=/dev/urandom bs=1M count=100 of=100MB.bin

# Pack it up with deduptar
$ deduptar -v -c packed.tar 100MB.bin
file (cloned)   100MB.bin

# How large is that tarball (superficially)?
$ du -h packed.tar
101M    packed.tar

# OK, but we promised deduplication. PROVE IT
$ btrfs filesystem du packed.tar
     Total   Exclusive  Set shared  Filename
 100.00MiB     4.00KiB   100.00MiB  packed.tar
```

There you go. Only 4 KB is allocated exclusively to `packed.tar` — that'll be the tar record header, located at the start of the file, padded out to the page boundary. There is 100 MB shared with "something else" where that something else is, naturally, the `100MB.bin` file full of random data.

Demonstrating the deduplication for the extraction scenario is left as an exercise. Or see the tests.

If you time it, you might find that `deduptar` can be much faster than your storage hardware — it can archive terabytes of file data in an instant ;-)

#### Why create this?

- Hardline ideological reasons! It's just… efficient!
- "Free tarballs"? "Fast archiving"? "Fast unpacking"?
- Snapshots — these tarballs are a bit like `btrfs` subvolume snapshots; you can bundle up filesystem state and only pay the storage cost the moment that the tarball content or parts of it become unique (say, when pages in the archived files change). In contrast to a subvolume snapshot, however, you can be selective in which files of a subvolume you want "snapshotted" this way. Also, it's still a tarball, so you can mix in files from outside the tarball-storing volume, and, for instance, serve it out as a single file over the web - you can't do either of that with a btrfs snapshot.

#### My company needs this, but for the ZIP archive format. Can you write that?
Sure, [send me an email](mailto:wicher+dedupzip@nontrivialpursuit.org).

### Using

See the usage section (`deduptar --help`):

```
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
```


#### How is this different from hardlinks?

Well… Hardlinks "deduplicate" on a higher level. They operate on a per-inode basis, and thus you can't "hardlink" a file into some part of another file or vice versa, while that is what deduptar does on a conceptual level.
Hardlinks are really just about sharing an inode (the true identity of a file) in the naming system. In other words: files can have many names.
Also, there's no copy-on-write with hardlinks.

Sidenote: deduptar supports storing and extracting hardlinks just like any other modern `tar`.

#### Do I get the same data sharing effect when I use GNU tar for unpacking/packing and subsequently run my file system deduplication tool?

No. Well, only in corner cases. The problem is that the `tar` format uses a 512-byte record size, while for deduplication ranges need to be aligned by filesystem page size (typically 4096 bytes). If you tar up a single file with GNU `tar` or BSD `tar` it will most certainly not be de-duplicatable with respect to the archive file, since in the archive file, the archivee file content will be preceded by a metadata header, typically taking up one, two, or three "tar pages". So no duplication (except if there is lots of redundancy in your files anyway), unless you somehow pad out that metadata header to make the start of the file inside the tar archive land on a pagesize boundary!

Which is exactly what deduptar does. Thus in contrast to other brands, deduptar-tarballs are very deduplicatable this way.

#### Compatibility

Long story short:

- GNU `tar` can extract from tarballs created by `deduptar`
- And `deduptar` can extract from tarballs created by GNU `tar`. Just run the tests — `make test`!
- Likely all modern `tar`s are bi-compatible this way.

However...

1. deduptar needs to add padding to tar record headers, so that archivee file contents are aligned on 4096-byte filesystem page boundaries
2. the tar format only specifies padding to pad out to a full 512-byte block, after which either a header, file contents, or the end-of-archive marker must start!
3. so is there something we can abuse to create arbitrary padding to make the start of file contents land on offsets that are a multiple of 4096, anyway?

Yes, there is such a loophole! Deduptar abuses [PAX headers](https://web.archive.org/web/20230706143859/https://www.ibm.com/docs/en/zos/2.3.0?topic=SSLTBW_2.3.0/com.ibm.zos.v2r3.bpxa500/paxex.html) to create the desired padding; there is a [comment header type](https://web.archive.org/web/20230707165644/https%3A%2F%2Fwww.mkssoftware.com%2Fdocs%2Fman4%2Fpax.4.asp) that does the job.

GNU `tar` and BSD `tar` ignore that PAX comment header. They don't stumble when working with a `deduptar`-produced file.

As for `deduptar` unpacking GNU- or BSD-`tar` produced tarballs — it cannot "clone out" files that aren't page-aligned inside the archive. And there's only a 1 in 8 probability of GNU or BSD `tar` coincidentally aligning the start of file data inside the archive on a filesystem page boundary. 

When `deduptar` can, it will "clone out" files, but when it can't, it'll fall back to copying data out. You can see whether any cloning took place by adding the `-v` flag.

### Questions, remarks, bug reports

Yes please! Send an email to [the public mailing list](mailto:~nullenenenen/deduptar-discuss@lists.sr.ht).

### Developing

#### Building and testing

Simply `make` or `make dev`.

Run the tests with `make test`.

#### Use as a library

Yes, that's possible, if you heed the terms and conditions of the [AGPL license](https://www.gnu.org/licenses/agpl-3.0.html) that this software is licensed to the public under.

See the `Archive()` and `Extract()` functions in the `nontrivialpursuit.org/deduptar/tarops` package. See `cli/cli.go` for an example of how to call them.

### Contributing

Yes! Send a patch to [the public mailing list](mailto:~nullenenenen/deduptar-discuss@lists.sr.ht).

See [here](https://man.sr.ht/tutorials/#contributing-to-srht-projects) for a tutorial on this collaboration mode.