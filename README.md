synchronicity
=============

A basic sync package for syncing two directories

Synchronicity uses Michael T Jones' walk package to walk each directory and perform the appropriate action on each file. Synchronicity supports file suffix include and exclude filters, file prefix include and exclude filters. Time based filters will be added in the future.

As support for operations get added, this will be updated. 

`synchronicity.Synchro`'s lock structure is for updating the counters. Walk has its own lock structures to manage its concurrency.

## Status
Under development

## Usage

    go get github.com/mohae/synchronicity


    import synch "github.com/mohae/synchronicity"


    message, err := synch.Push(source, destination)

## Operations
### `push`
The `push` sub-command pushes the contents of one directory, source, to another, destination.  A delete flag on `synchronicity.Synchro` controls whether or not the files in the destination that are not in the source are deleted. Deletion results in directory being a copy of the source.

Whether or not the destination directory is a clone of the source, as opposed to a copy, depends on how you configure `synchronicity.Synchro`.

## License
Modified BSD Style license. Please view LICENSE file for details.
