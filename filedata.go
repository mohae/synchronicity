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
	"os"
	"path/filepath"
	"strings"
)

// This allows for up to 128k of read data. If the file is larger than that, 
// a different approach should be done, i.e. don't precompute the hash and use
// other rules for determining difference. 
//
var MaxChunks = 16   // Modify directly to change buffered hashes
var ChunkSize = 8*1024 // use 8k chunks

const (
	invalid hashType = iota
	SHA256
)
type hashType int // Not really needed atm, but it'll be handy for adding other types.
var useHashType hashType

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
	Hashes	[]Hash256
	HashType hashType
	ChunkSize int // The chunksize that this was created with.
	MaxChunks int
	CurByte int64 // for when the while file hasn't been hashed and 
	Root string // the relative root of this file: allows for synch support
	Dir string // relative path to parent directory of Fi
	Fi        os.FileInfo
}

// Returns a FileData struct for the passed file using the defaults.
// Set any overrides before performing an operation.
func NewFileData(root, dir string, fi os.FileInfo) FileData {
	if dir == "." {
		dir = ""
	}
	fd := FileData{HashType: useHashType, ChunkSize: ChunkSize, MaxChunks: MaxChunks, Root: root, Dir: dir, Fi: fi}
	return fd
}

// String is an alias to RelPath
func (fd *FileData) String() string {
	return fd.RelPath()
}

// SetHash computes the hash of the FileData. The path of the file is passed 
// because FileData only knows it's name, not its location.
func (fd *FileData) SetHash() error {
	f, err := os.Open(fd.RootPath())
	if err != nil {
		return err
	}
	defer f.Close()
	var hasher hash.Hash
	switch fd.HashType {
	case SHA256:
		hasher = sha256.New() //
	default:
		return fmt.Errorf("%s hash type", fd.HashType.String())
	}
	if fd.ChunkSize == 0 {
		return fd.hashFile(f, hasher)
	}
	return fd.chunkedHashFile(f, hasher)
}

func (fd *FileData) hashFile(f *os.File, hasher hash.Hash) error {
	_, err := io.Copy(hasher, f);
	if err != nil {
		logger.Error(err)
		return err
	}
	h := Hash256{}
	copy(h[:], hasher.Sum(nil))
	fd.Hashes = append(fd.Hashes, Hash256(h))
	return nil
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

func (fd *FileData) chunkedHashFile(f *os.File, hasher hash.Hash) error {
	reader := bufio.NewReaderSize(f, int(fd.ChunkSize))
	_, err := io.CopyN(hasher, reader, int64(fd.ChunkSize))
	if err != nil {
		logger.Error(err)
		return err
	}
	h := Hash256{}
	copy(h[:], hasher.Sum(nil))
	fd.Hashes = append(fd.Hashes, Hash256(h))
	logger.Debugf("chunked hash %s\n%x\n", fd.String(), fd.Hashes[0])
	return nil
}

func SetHashType(s string) {
	useHashType = ParseHashType(s)
}

func ParseHashType(s string) hashType {
	s = strings.ToLower(s)
	switch s {
	case "sha256":
		return SHA256
	}
	return invalid
}
