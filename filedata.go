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
)

// SHA256 sized for hashed blocks.
type Hash [32]byte
// This allows for up to 128k of read data. If the file is larger than that, 
// a different approach should be done, i.e. don't precompute the hash and use
// other rules for determining difference. 
//
var MaxBlocks = 16   // Modify directly to change buffered hashes
const blockSize = 8*1024 // use 8k blocks

const (
	invalid hashType = iota
	SHA256
)

type hashType int // Not really needed atm, but it'll be handy for adding other types.

func (h hashType) String() string {
	switch h {
	case SHA256:
		return "sha256"
	case invalid:
		return "invalid"
	}
	return "unknown"
}

type FileData struct {
	Processed bool
	Hash	[]byte
	HashType hashType
	Dir string // relative path to parent directory of Fi
	Fi        os.FileInfo
}

func (fd *FileData) String() string {
	return filepath.Join(fd.Dir, fd.Fi.Name())
}

// SetHash computes the hash of the FileData. The path of the file is passed 
// because FileData only knows it's name, not its location.
func (fd *FileData) SetHash(prefix int64) error {
	f, err := os.Open(fd.String())
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, blockSize)
	var hasher hash.Hash
	switch fd.HashType {
	case SHA256:
		hasher = sha256.New() //
	default:
		return fmt.Errorf("%s hash type", fd.HashType.String())
	}
	if prefix == 0 {
		_, err := io.Copy(hasher, reader)
		if err != nil {
			logger.Error(err)
			return err
		}
	} else {
		_, err := io.CopyN(hasher, reader, prefix)
		if err != nil {
			logger.Error(err)
			return err
		}
	}
	hashed := hasher.Sum(nil)
	logger.Infof("hashed: %v\n", hashed)
	copy(fd.Hash, hashed)
	logger.Debugf("%s\n%x\n%x\n", fd.String(), fd.Hash,hashed)
	return nil
}
