synchronicity
=============

A basic sync package for syncing two directories allowing for idempotent pushes of sources to destinations.

Synchronicity uses Michael T Jones' walk package to concurrently walk each directory and perform the appropriate action on each file. While synchronicity is meant for synchronizing directory trees, it does not support synchronization of two directories at the moment. While this is theoretically possible, given that a `sync` operation is just a `push` and `pull` without file deletion.

At this point, synchronicity only supports the push operation.

## Comparisons
Synchronicity supports several methods of checking files for changes:
* File size: if the file size is different, it has changed.
* Byte comparison: the files are compared in chunks of bytes until a change is detected or EOF.
* Digest comparison: the file hashes are compared. Synchronicity supports SHA256.
* Chunked digest comparison: chunks of bytes are read from each file and a digest of the chunk is generated and compared.

If files are found to be equal, their properties are checked for changes to see if that information should be updated.

Source files always take precedence over destination files; this makes push operations idempotent.

## Actions
File comparisons can result in the following actions:
* New: the source file doesn't exist in the destination.
* Copy: the source and destination files are different at the byte level; update destination with source.
* Update: the source and destination file information are different; update destination with the sources'.
* No action: the source and destination files are exactly the same; they are flagged as duplicates.

## Execution overview
The destination directory is first indexed; the properties of each file encountered, along with its checksum, are indexed.

The source directory is then walked. For each file encountered, the destination index is checked to see if it already exists. If it doese not exist, the `new` action is initiated for that file. If the file does exist, its checksum is compared to the destination file's checksum; a `copy` action is generated for each comparison that results in a difference. If the checksums are the same, the file properties, `mdate` and `mode`, are checked. If there are any differences in those properties, an `update` action is generated.

If `delete` is enabled, any orphaned files in the destination are deleted. An orphaned file is a file that exists in the destination but does not exist in the source.

## Logging
Synchronicity uses the standard `log` package. By default it logs to `ioutil.Discard`. Call `synchronicity.SetLogger(*io.writer*)` to set the log output destination. To enable verbose output, set the `synchronicity.Verbose` bool to `true`.  The verbose output is written to the log.

### Experimental filtering support
Synchronicity has experimental support for file filtering using `include` and `exclude` filters. Include filters only looks at files that match the `include` filters. Exclude filters excludes any files that match the `exclude` filters. These filters can be applied to either file suffixes or as prefixes to filenames.

### Future Functionality
* Support for filtering on time.
* Writing directory inventory and information, including checksums, to file or other persistent store.

## License
Modified BSD Style license. Please view LICENSE file for details.
