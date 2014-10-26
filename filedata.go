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
type Hash [32]byte

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
	Hash	[]byte
	HashType hashType
	ChunkSize int // The chunksize that this was created with.
	CurByte int64 // for when the while file hasn't been hashed and 
	Dir string // relative path to parent directory of Fi
	Fi        os.FileInfo
}

func (fd *FileData) String() string {
	return filepath.Join(fd.Dir, fd.Fi.Name())
}

// SetHash computes the hash of the FileData. The path of the file is passed 
// because FileData only knows it's name, not its location.
func (fd *FileData) SetHash() error {
	f, err := os.Open(fd.String())
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
		return fd.hashFile(hasher)
	}
	return fd.chunkedHashFile(hasher)
}

func (fd *FileData) hashFile(hasher hash.Hash) error {
	f, err := os.Open(fd.String())
	if err != nil {
		logger.Error(err)
		return err
	}
	defer f.Close()
	 _, err = io.Copy(hasher, f);
	if err != nil {
		logger.Error(err)
		return err
	}
	copy(fd.Hash, hasher.Sum(nil))
	return nil
}

func (fd *FileData) chunkedHashFile(hasher hash.Hash) error {
	f, err := os.Open(fd.String())
	reader := bufio.NewReaderSize(f, int(fd.ChunkSize))
	_, err = io.CopyN(hasher, reader, int64(fd.ChunkSize))
	if err != nil {
		logger.Error(err)
		return err
	}
	copy(fd.Hash, hasher.Sum(nil))
	logger.Debugf("%s\n%x\n", fd.String(), fd.Hash)
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
