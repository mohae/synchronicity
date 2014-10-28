synchronicity
=============

A basic sync package for syncing two directories.

Synchronicity uses Michael T Jones' walk package to concurrently walk each directory and perform the appropriate action on each file. While synchronicity is meant for synchronizing directory trees, it does not support synchronization of two directories at the moment. While this is theoretically possible, given that a `sync` operation is just a `push` and `pull` without file deletion.

Synchronicity does support `push` and `pull`, which is just a `push` in reverse, with optional deletion of orphaned files in the destination. 

## Actions
The destination directory is first indexed; the properties of each file encountered, along with its checksum, are indexed.

The source directory is then walked. For each file encountered, the destination index is checked to see if it already exists. If it doese not exist, the `new` action is initiated for that file. If the file does exist, its checksum is compared to the destination file's checksum; a `copy` action is generated for each comparison that results in a difference. If the checksums are the same, the file properties, `mdate` and `mode`, are checked. If there are any differences in those properties, an `update` action is generated.

If `delete` is enabled, any orphaned files in the destination are deleted. An orphaned file is a file that exists in the destination but does not exist in the source.

## File Comparison
Files that exist in both the source and the destination are examined to see if the source should be copied to the destination or if the destination's properties should be updated.

For change detection, files are checked for size equality. If they are different sizes, the source is copied to the destination. If the file sizes are the same, the files contents are compare for equality. This is done by reading chunks of bytes, 8KB by default, and comparing their digests. If their digests are different, the source is copied to the destination.

If both files are the same, their file properties are checked for changes. If there has been a change in `mtime` or `mode`, the destination file's properties are updated with the source's.

### Full file
If the chunkSize is  set to 0, the full entire contents of the file will be read and hashed. This can lead to high memory consumption.

### Chunked
If the chunkSize is set to a non-zero value, the contents of the files will be read using the chunkSize as the number of bytes to read. ChunkSize is always a multiple of 1k, 1024 bytes.

When inventorying destination files, their hashes will be calculated too. When chunked reading is used, each chunk generates its own hash, which is appended to FileData.Hashes. Reading continues until MaxChunks have been read or EOF has been reached. If the file size is larger thank chunkSize * MaxChunks, the rest of the file will be handled by the mixed procssing.

### Mixed
For larger files, mixed processing is used for file comparison. The mixed evaluation will first behave the same as the chunked evaluation with the cached destination hashes. Once MaxChunks has been processed, the destination file will be opened for reading, and the files will be compared starting with the byte after the last chunk read.

This provides rudimentary support for large files, allowing for detection of changes before the entire file has been processed, if a change has occurred, while limiting the amount of RAM used to pre-calculate file digests.
 
### Experimental support
Synchronicity has experimental support for file filtering using `include` and `exclude` filters. Include filters only looks at files that match the `include` filters. Exclude filters excludes any files that match the `exclude` filters. These filters can be applied to either file suffixes or as prefixes to filenames.

### Future Functionality
* Support for filtering on time.
* Adaptive chunking of large files.
* Writing directory inventory and information to file or other persistent store.

## Notes:
`synchronicity.Synchro`'s lock structure is for updating the counters. Walk has its own lock structures to manage its concurrency. Channels are used for the work queues.

## Status
Experimental.

## Operations
### `push`
The `push` sub-command pushes the contents of one directory, source, to another, destination.  A delete flag on `synchronicity.Synchro` controls whether or not the files in the destination that are not in the source are deleted. Deletion results in directory being a copy of the source.

Whether or not the destination directory is a clone of the source, as opposed to a copy, depends on how you configure `synchronicity.Synchro`.

## License
Modified BSD Style license. Please view LICENSE file for details.
