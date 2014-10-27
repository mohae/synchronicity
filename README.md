synchronicity
=============

A basic sync package for syncing two directories.

Synchronicity uses Michael T Jones' walk package to concurrently walk each directory and perform the appropriate action on each file. While synchronicity is meant for synchronizing directory trees, it does not support synchronization of two directories at the moment. While this is theoretically possible, given that a `sync` operation is just a `push` and `pull` without file deletion.

Synchronicity does support `push` and `pull`, which is just a `push` in reverse, with optional deletion of orphaned files in the destination. 

## Actions
The destination directory is first indexed, with various properties tracked, and its checksum calculated.

The source directory is then walked. For each file encountered, the destination index is checked to see if it already exists. If it doese not exist, the `new` action is initiated for that file. If the file does exist, its checksum is compared to the destination file's checksum; a `copy` action is generated for each comparison that results in a difference. If the checksums are the same, the file properties, `mdate` and `mode`, are checked. If there are any differences in those properties, an `update` action is generated.

If `delete` is enabled, any orphaned files in the destination are deleted. An orphaned file is a file that exists in the destination but does not exist in the source.

### Experimental support
Synchronicity has experimental support for file filtering using `include` and `exclude` filters. Include filters only looks at files that match the `include` filters. Exclude filters excludes any files that match the `exclude` filters. These filters can be applied to either file suffixes or as prefixes to filenames.

### Future Functionality
Support for filtering on time.

Chunked hash comparison and calculation. Currently, synchronicity hashes the entire file contents, which can be memory unfriendly. Chunking is one way to handle this. When chunking is enabled, synchronicity will read up to *n* chunks of a file, or until EOF, whichever comes first.

This means that files larger than `Synchro.ChunkSize * Syncrho.MaxChunks` need to be checked for content equality using an additional strategy. The strategy used will be determined by the tradeoff desired between speed and accurracy.

The fastest method for evaluation of large files will be to compare properties and size for equality along with the pre-calculated checksums. If any of these are different, the files will not be considered equal. This method may lead to false positives because it will not detect files that have been modified beyond the already calculated bits that are also the same size as the original and have their properties set to be the same.

The accurate method chunks the source file and compares each chunk to their destination counterpart. Once all of the destination's pre-calculated hashes have been consumed, synchronicity will continue reading the destination file, in chunks, starting at the next byte afte the last byte read by the hash calculation process during the building of the destination file list.

Then again, this may be implemented differently...

### Notes:
`synchronicity.Synchro`'s lock structure is for updating the counters. Walk has its own lock structures to manage its concurrency. Channels are used for the work queues.

## Status
Experimental.

## Operations
### `push`
The `push` sub-command pushes the contents of one directory, source, to another, destination.  A delete flag on `synchronicity.Synchro` controls whether or not the files in the destination that are not in the source are deleted. Deletion results in directory being a copy of the source.

Whether or not the destination directory is a clone of the source, as opposed to a copy, depends on how you configure `synchronicity.Synchro`.

## License
Modified BSD Style license. Please view LICENSE file for details.
