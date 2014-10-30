// Copyright 2014 Joel Scoble (github.com/mohae) All rights reserved.
// Use of this source code is governed by a BSD-style license that
// can be found in the LICENSE file.
//
package synchronicity

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// This allows for up to 128k of read data. If the file is larger than that,
// a different approach should be done, i.e. don't precompute the hash and use
// other rules for determining difference.
//
var MaxChunks = 16              // Modify directly to change buffered hashes
var chunkSize = int64(8 * 1024) // use 8k chunks as default

// SetChunkSize sets the chunkSize as 1k * i, i.e. 8 == 8k chunkSize
// If the multiplier, i, is <= 0, the default is used, 8.
func SetChunkSize(i int) {
	if i <= 0 {
		i = 8
	}
	chunkSize = int64(1024 * i)
}

const (
	invalid hashType = iota
	SHA256
)

type hashType int // Not really needed atm, but it'll be handy for adding other types.
var useHashType hashType // The hash type to use to create the digest

// SHA256 sized for hashed blocks.
type Hash256 [32]byte

func (h hashType) String() string {
	switch h {
	case SHA256:
		return "sha256"
	case invalid:
		return "invalid"
	}
	return "unknown"
}

func init() {
	useHashType = SHA256
}

type FileData struct {
	Processed bool
	Digests    []Hash256
	HashType  hashType
	ChunkSize int64 // The chunksize that this was created with.
	MaxChunks int
	CurByte   int64  // for when the while file hasn't been hashed and
	Root      string // the relative root of this file: allows for synch support
	Dir       string // relative path to parent directory of Fi
	Fi        os.FileInfo
}

// Returns a FileData struct for the passed file using the defaults.
// Set any overrides before performing an operation.
func NewFileData(root, dir string, fi os.FileInfo) FileData {
	if dir == "." {
		dir = ""
	}
	fd := FileData{HashType: useHashType, ChunkSize: chunkSize, MaxChunks: MaxChunks, Root: root, Dir: dir, Fi: fi}
	return fd
}

// String is an alias to RelPath
func (fd *FileData) String() string {
	return fd.RelPath()
}

// SetHash computes the hash of the FileData. The path of the file is passed
// because FileData only knows it's name, not its location.
func (fd *FileData) SetHash() error {
	f, hasher, err := fd.getFileHasher()
	if err != nil {
		return err
	}
	defer f.Close()
	if fd.ChunkSize == 0 {
		return fd.hashFile(f, hasher)
	}
	return fd.chunkedHashFile(f, hasher)
}

// getHasher returns a file and the hasher to use it on. If error, return that.
// Caller is responsible for closing the file.
func (fd *FileData) getFileHasher() (f *os.File, hasher hash.Hash, err error) {
	f, err = os.Open(fd.RootPath())
	if err != nil {
		log.Printf("error opening %s: %s", fd.RootPath(), err)
		return
	}
	hasher, err = fd.getHasher()
	return
}

// getHasher returns a hasher or an error
func (fd *FileData) getHasher() (hasher hash.Hash, err error) {
	switch fd.HashType {
	case SHA256:
		hasher = sha256.New() //
	default:
		err = fmt.Errorf("%s hash type not supported", fd.HashType.String())
		log.Printf("error getting Hashtype: %s", err)
		return
	}
	return
}

// hashFile hashes the entire file.
func (fd *FileData) hashFile(f *os.File, hasher hash.Hash) error {
	_, err := io.Copy(hasher, f)
	if err != nil {
		log.Printf("error copying for hashfile: %s/n", err)
		return err
	}
	h := Hash256{}
	copy(h[:], hasher.Sum(nil))
	fd.Digests = append(fd.Digests, Hash256(h))
	return nil
}

// chunkedHashFile reads up to max chunks, or the entire file, whichever comes
// first.
func (fd *FileData) chunkedHashFile(f *os.File, hasher hash.Hash) (err error) {
	reader := bufio.NewReaderSize(f, int(fd.ChunkSize))
//	var cnt int
	var bytes int64
	h := Hash256{}
	for cnt := 0; cnt < MaxChunks; cnt++ { // read until EOF || MaxChunks
		n, err := io.CopyN(hasher, reader, int64(fd.ChunkSize))
		if err != nil && err != io.EOF {
			log.Printf("error copying chunked Hash file: %s", err)
			return err
		}
		bytes += n
		copy(h[:], hasher.Sum(nil))
		fd.Digests = append(fd.Digests, Hash256(h))
	}
	return nil
}

// isEqual compares the current file with the passed file and returns
// whether or not they are equal. If the file length is greater than our
// checksum buffer, the rest of the file is read in chunks, until EOF or
// a difference is found, whichever comes first.
//
// If they are of different lengths, we assume they are different
func (fd *FileData) isEqual(dstFd FileData) (bool, error) {
	Logf("isEqual %s %s %s", fd.RootPath(), dstFd.RootPath(), strconv.FormatBool(fd.Fi.IsDir()))
	if fd.Fi.Size() != dstFd.Fi.Size() {
		return false, nil
	}
	// otherwise, examine the file contents
	f, hasher, err := fd.getFileHasher()
	if err != nil {
		log.Printf("error evaluating equality %s: %s", fd.String(), err)
		return false, err
	}
	defer f.Close()
	// TODO support adaptive
	chunks := int(fd.Fi.Size()/int64(fd.ChunkSize) + 1)
	if chunks > len(fd.Digests) || len(fd.Digests) == 0 {
		return fd.isEqualMixed(chunks, f, hasher, dstFd)
	}

	return fd.isEqualCached(chunks, f, hasher, dstFd)
}

// isEqualMixed is used when the file size is larger than the amount of bytes
// we can precalculate, 128k by default. First the precalculated digests are 
// used, then the original destination file is read and the pointer moved to
// the last read byte by the precalculation routine, until a difference is
// found or an EOF is encountered.
//
func (fd *FileData) isEqualMixed(chunks int, f *os.File, hasher hash.Hash, dstFd FileData) (bool, error) {
	if len(dstFd.Digests) > 0 {
		equal, err := fd.isEqualCached(dstFd.MaxChunks, f, hasher, dstFd)
		if err != nil {
			log.Printf("error checking equality using precalculated digests for %s: %s", dstFd.String(), err)
			return equal, err
		}
		if !equal {
			return equal, nil
		}
	}
	// Otherwise check the file from the current point
	dstF, err := os.Open(dstFd.RootPath())

	// Go to the last read byte
	if dstFd.CurByte > 0 {
		_, err = dstF.Seek(dstFd.CurByte, 0)
		if err != nil {
			log.Printf("error seeking byte %d: %s", dstFd.CurByte, err)
			return false, err
		}
	}
	dstHasher, err := fd.getHasher()
	if err != nil {
		log.Print("error getting hasher for %s: %s", fd.String(), err)
		return false, err
	}
	dH := Hash256{}
	sH := Hash256{}
	// Check until EOF or a difference is found
	for {
		s, err := io.CopyN(hasher, f, fd.ChunkSize)
		if err != nil && err != io.EOF {
			log.Printf("error copying hasher for %s: %s", f.Name(), err)
			return false, err
		}
		d, err := io.CopyN(dstHasher, dstF, fd.ChunkSize)
		if err != nil && err != io.EOF {
			log.Printf("error copying hasher for %s: ", err)
			return false, err
		}
		if d != s { // if the bytes copied were different, return false
			return false, nil
		}
		copy(dH[:], dstHasher.Sum(nil))
		copy(sH[:], hasher.Sum(nil))
		if Hash256(dH) != Hash256(sH) {
			return false, nil
		}
		// if EOF
		if s == 0 && d == 0 {
			break
		}
	}
	return true, nil
}

// isEqualCached is called when the file fits within the maxChunks.
func (fd *FileData) isEqualCached(chunks int, f *os.File, hasher hash.Hash, dstFd FileData) (bool, error) {
	h := Hash256{}
	for i := 0; i < chunks; i++ {
		_, err := io.CopyN(hasher, f, fd.ChunkSize)
		if err != nil {
			log.Printf("error copying hasher for precalculated digest comparison %s: %s", fd.String(), err)
			return false, err
		}
		copy(h[:], hasher.Sum(nil))
		if Hash256(h) != dstFd.Digests[i] {
			return false, nil
		}
	}
	return true, nil
}

// RelPath returns the relative path of the file, this is the file less the
// root information. This allows for easy comparision between two directories.
func (fd *FileData) RelPath() string {
	return filepath.Join(fd.Dir, fd.Fi.Name())
}

// RootPath returns the relative path of the file including its root. A root is
// the directory that Synchronicity considers a root, e.g. one of the
// directories being synched. This is not the FullPath of a file.
func (fd *FileData) RootPath() string {
	return filepath.Join(fd.Root, fd.Dir, fd.Fi.Name())
}

// SetHashType sets the hashtype to use based on the passed value. 
func SetHashType(s string) {
	useHashType = ParseHashType(s)
}

// ParseHashType returns the hashType for a given string.
func ParseHashType(s string) hashType {
	s = strings.ToLower(s)
	switch s {
	case "sha256":
		return SHA256
	}
	return invalid
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
