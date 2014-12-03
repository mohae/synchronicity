// Copyright 2014 Joel Scoble (github.com/mohae) All rights reserved.
// Use of this source code is governed by a BSD-style license that
// can be found in the LICENSE file.
package synchronicity

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type equalityType int

const (
	UnknownEquality equalityType = iota
	BasicEquality                // compare bytes for equality check
	DigestEquality               // compare digests for equality check: digest entire file at once
	ChunkedEquality              // compare digests for equality check digest using chunks
)

func (e equalityType) String() string {
	switch e {
	case BasicEquality:
		return "compare bytes"
	case DigestEquality:
		return "compare digests"
	case ChunkedEquality:
		return "compare chunked digests"
	}
	return "unknown"
}

func EqualityType(s string) equalityType {
	switch s {
	case "byte", "bytes":
		return BasicEquality
	case "digest", "hash":
		return DigestEquality
	case "chunked", "chunkeddigest", "chunkedhash":
		return ChunkedEquality
	}
	return UnknownEquality
}

type hashType int // Not really needed atm, but it'll be handy for adding other types.

const (
	invalidHash hashType = iota
	SHA256
)

// SHA256 sized for hashed blocks.
type Hash256 [32]byte

func (h hashType) String() string {
	switch h {
	case SHA256:
		return "sha256"
	case invalidHash:
		return "invalid hash type"
	}
	return "unknown"
}

type actionType int

const (
	nilAction    actionType = iota
	newAction               // creates new file in dst; doesn't exist in dst
	copyAction              // copy file from src to dst; contents are different.
	deleteAction            // delete file from dst; doesn't exist in source
	updateAction            // update file properties in dst; contents same but properties diff.
)

func (a actionType) String() string {
	switch a {
	case nilAction:
		return "duplicate"
	case newAction:
		return "new"
	case copyAction:
		return "copy"
	case deleteAction:
		return "delete"
	case updateAction:
		return "update"
	}
	return "unknown"
}

// Defaults for new Synchro objects.
// Chunking settings: the best chunkSize is the one that allows the task to be
// completed in the fastest amount of time. This depends on the system this
// executes on.
var defaultEqualityType equalityType
var defaultHashType hashType
var MaxChunks = 4               // Modify directly to change buffered hashes
var chunkSize = int64(2 * 1024) // use 16k chunks as default; cuts down on garbage
var maxProcs int
var cpuMultiplier int // 0 == 1, default == 2
var cpu int = runtime.NumCPU()
var ReadAll = true
var unsetTime time.Time

// SetChunkSize sets the chunkSize as 1k * i, i.e. 4 == 4k chunkSize
// If the multiplier, i, is < 0, the default is used, 4.
func SetChunkSize(i int) {
	if i <= 0 {
		i = 2
	}
	chunkSize = int64(1024 * i)
}

func init() {
	defaultHashType = SHA256
	cpuMultiplier = 4
	maxProcs = cpuMultiplier * cpu
	nSynch = NewSynch()
	defaultEqualityType = BasicEquality
}

var nSynch *Synch            // Synchronicity's global Synch
var archSynch *ArchivedSynch // Synchronicity's global archived synch

// SetMaxProcs sets the maxProcs to the passed value, or 1 for <= 0.
func (s *Synch) SetMaxProcs(i int) {
	if i <= 0 {
		s.maxProcs = 1
	} else {
		s.maxProcs = cpu * i
	}
}

// SetCPUMultiplier sets both the multipler and the maxProcs.
// If the multiplier is <= 0, 1 is used
func SetCPUMultiplier(i int) {
	if i <= 0 {
		i = 1
	}
	cpuMultiplier = i
	maxProcs = cpu * cpuMultiplier
}

// SetHashType sets the hashtype to use based on the passed value.
func SetHashType(s string) {
	defaultHashType = ParseHashType(s)
}

// ParseHashType returns the hashType for a given string.
func ParseHashType(s string) hashType {
	s = strings.ToLower(s)
	switch s {
	case "sha256":
		return SHA256
	}
	return invalidHash
}

// getFileParts splits the passed string into directory, filename, and file
// extension, or as many of those parts that exist.
func getFileParts(s string) (dir, file, ext string) {
	// see if there is path involved, if there is, get the last part of it
	dir, filename := filepath.Split(s)
	parts := strings.Split(filename, ".")
	l := len(parts)
	switch l {
	case 2:
		file := parts[0]
		ext := parts[1]
		return dir, file, ext
	case 1:
		file := parts[0]
		return dir, file, ext
	default:
		// join all but the last parts together with a "."
		file := strings.Join(parts[0:l-1], ".")
		ext := parts[l-1]
		return dir, file, ext
	}
	return "", "", ""
}

type counter struct {
	Name  string
	Files int32
	Bytes int64
}

func newCounter(n string) counter {
	return counter{Name: n}
}

func (c counter) String() string {
	var buf bytes.Buffer
	buf.WriteString(strconv.Itoa((int(c.Files))))
	buf.WriteString(" files totalling ")
	buf.WriteString(strconv.Itoa(int(c.Bytes)))
	buf.WriteString(" bytes were ")
	buf.WriteString(c.Name)
	return buf.String()
}
