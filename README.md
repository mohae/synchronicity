synchronicity
=============

**Refactor in process, do not use**

A flexible sync package for concurrently inventorying and syncing two directories allowing for idempotent pushes of sources to destinations; among other things.

Synchronicity is usable right away, with no additional configuration. It can be used using its internal Synchro struct or it can return a `synchronicity.Synchro` struct for you to use directly. Both can be configured to better suit the environment within which it will be running, but that's optional.

Synchronicity supports both byte based and checksum, or digest, comparison. By default, it compares bytes, which is much faster; using less CPU and memory..

Errors result in the operation failing, which may lead to the destination state being in an incomplete update state. If this is an issue, a way to backup and rollback the state of the destination directory should be implemented. *This is something synchronicity will be supporting in the future.*

At this point, synchronicity only supports the push operation.

## Comparisons
Synchronicity supports several methods of checking files for changes:
* File size: if the file size is different, it has changed.
* Byte comparison: the files are compared in chunks of bytes until a change is detected or EOF.
* Digest comparison: the file hashes are compared. Synchronicity supports SHA256.
* Chunked digest comparison: chunks of bytes are read from each file and a digest of the chunk is generated and compared.

If the files' contents are found to be equal, their properties are checked for changes to see if that information should be updated.

Source files always take precedence over destination files; this makes push operations idempotent.

## Tasks
File comparisons can result in the following tasks:
* New: the source file doesn't exist in the destination.
* Copy: the source and destination files are different at the byte level; update destination with source.
* Update: the source and destination file information are different; update destination with the sources'.
* No action: the source and destination files are exactly the same; they are flagged as duplicates.

## Execution overview
The destination directory is first indexed; the properties of each file encountered, and, optionally, its checksum or checksums of a portion of the file, are indexed.

The source directory is then walked. For each file encountered, the destination index is checked to see if it already exists. If it does not exist, a `new` action is initiated for that file. If the file does exist, its checksum is compared to the destination file's checksum; a `copy` task is generated for each comparison that results in a difference. If the checksums are the same, the files properties, `mdate` and `mode`, are checked. If there are any differences in those properties, an `update` task is generated. An `update` task does not result in the copying of the source file data, only its header information is copied to the destination file.

If `delete` is enabled, any orphaned files in the destination are deleted. An orphaned file is a file that exists in the destination but does not exist in the source. This means that any destination file that was not compared to a source file is deleted.

## Logging
Synchronicity uses the standard `log` package. By default it logs to `ioutil.Discard`. Call `synchronicity.SetLogger(*io.writer*)` to set the log output destination. To enable verbose output, set the `synchronicity.Verbose` bool to `true`.  The verbose output is written to the log: there currently isn't very much verbose information generated.

### Experimental filtering support
Synchronicity has experimental support for file filtering using `include` and `exclude` filters. Include filters only looks at files that match the `include` filters. Exclude filters excludes any files that match the `exclude` filters. These filters can be applied to either file suffixes or as prefixes to filenames.

### Future Functionality
* Support for filtering on time.
* Writing directory inventory and information, including checksums, to file or other persistent store.
* Creation of compressed archive for:
    * each destination file replaced or deleted
    * each set of destination files replaced or deleted
    * each source file pushed to a destination
    * each set of source files pushed to a destination
* Encryped archives
* Rollback
* Rollforward


## License
Modified BSD Style license. Please view LICENSE file for details.
