// Copyright 2014 Joel Scoble (github.com/mohae) All rights reserved.
// Use of this source code is governed by a BSD-style license that
// can be found in the LICENSE file.
//
package synchronicity

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"log"
	"os"
	"path/filepath"

	_ "runtime/pprof"
)

// Returns a FileData struct for the passed file using the defaults.
// Set any overrides before performing an operation.
func NewFileData(root, dir string, fi os.FileInfo, s *Synchro) FileData {
	if dir == "." {
		dir = ""
	}
	fd := FileData{Root: root, Dir: dir, Fi: fi, synchro: s}
	return fd
}

// FileData is used to provide additional information about a file beyond what
// os.FileInfo provides.
type FileData struct {
	Processed bool
	Digests   []Hash256
	synchro   *Synchro
	CurByte   int64  // for when the while file hasn't been hashed and
	Root      string // the relative root of this file: allows for synch support
	Dir       string // relative path to parent directory of Fi
	Fi        os.FileInfo
}

// String is an alias to RelPath.
func (fd *FileData) String() string {
	return fd.RelPath()
}

// SetHash opens the file and creates its hasher then calls the appropriate
// hashing method: how to hash the file (as opposed to hashing type).
func (fd *FileData) SetHash() error {
	f, hasher, err := fd.getFileHasher()
	if err != nil {
		return err
	}
	defer f.Close()
	switch fd.synchro.equalityType {
	case EqualityDigest:
		return fd.precomputeDigest(f, hasher)
	case EqualityChunkedDigest:
		return fd.chunkedPrecomputeDigest(f, hasher)
	}
	return nil
}

// getFileHasher returns a file and the hasher to use it on. If error, it
// returns that. The caller is responsible for closing the file.
func (fd *FileData) getFileHasher() (f *os.File, hasher hash.Hash, err error) {
	f, err = os.Open(fd.RootPath())
	if err != nil {
		log.Printf("error opening %s: %s", fd.RootPath(), err)
		return
	}
	hasher, err = fd.getHasher()
	return
}

// getHasher returns a hasher of the appropriate type, or an error.
func (fd *FileData) getHasher() (hasher hash.Hash, err error) {
	switch fd.synchro.hashType {
	case SHA256:
		hasher = sha256.New() //
	default:
		err = fmt.Errorf("%s hash type not supported", fd.synchro.hashType.String())
		log.Printf("error getting Hashtype: %s", err)
		return
	}
	return
}

// hashFile hashes the entire file.
func (fd *FileData) precomputeDigest(f *os.File, hasher hash.Hash) error {
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
func (fd *FileData) chunkedPrecomputeDigest(f *os.File, hasher hash.Hash) (err error) {
	reader := bufio.NewReaderSize(f, int(fd.synchro.chunkSize))
	//	var cnt int
	var bytes int64
	h := Hash256{}
	for cnt := 0; cnt < MaxChunks; cnt++ { // read until EOF || MaxChunks
		n, err := io.CopyN(hasher, reader, int64(fd.synchro.chunkSize))
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
	//	Logf("isEqual %s %s %s", fd.RootPath(), dstFd.RootPath(), strconv.FormatBool(fd.Fi.IsDir()))
	if fd.Fi.Size() != dstFd.Fi.Size() { // Check to see if size is different first
		return false, nil
	}
	// otherwise, examine the file contents
	switch fd.synchro.equalityType {
	case EqualityBasic:
		return fd.byteCompare(dstFd)
	case EqualityDigest, EqualityChunkedDigest:
		return fd.hashCompare(dstFd)
	}
	return false, fmt.Errorf("error: isEqual encountered an unsupported equality type %d", int(fd.synchro.equalityType))
}

func (fd *FileData) byteCompare(dstFd FileData) (bool, error) {
	// open the files
	srcF, err := os.Open(fd.RootPath())
	if err != nil {
		return false, err
	}
	defer srcF.Close()
	dstF, err := os.Open(dstFd.RootPath())
	if err != nil {
		return false, err
	}
	defer dstF.Close()
	// go through each file until a difference is encountered or eof.
	dstBuf := make([]byte, fd.synchro.chunkSize)
	srcBuf := make([]byte, fd.synchro.chunkSize)
	var srcN, dstN int
	for {
		srcN, err = srcF.Read(srcBuf)
		if err != nil && err != io.EOF {
			return false, err
		}
		dstN, err = dstF.Read(dstBuf)
		if err != nil && err != io.EOF {
			return false, err
		}
		if dstN == 0 && srcN == 0 {
			return true, nil
		}
		if !bytes.Equal(srcBuf[:srcN], dstBuf[:dstN]) {
			return false, nil
		}
	}
	return false, nil
}

func (fd *FileData) hashCompare(dstFd FileData) (bool, error) {
	f, hasher, err := fd.getFileHasher()
	if err != nil {
		log.Printf("error getting hasher for %s: %s", fd.RootPath(), err)
		return false, err
	}
	if f == nil {
		log.Printf("Nil file handle encountered: %s", fd.RootPath())
	}
	if hasher == nil {
		log.Printf("Nil hasher encountered for %s", fd.RootPath())
	}
	defer f.Close()
	// TODO support adaptive
	chunks := int(fd.Fi.Size()/int64(fd.synchro.chunkSize) + 1)
	if chunks > len(fd.Digests) {
		b, err := fd.isEqualMixed(chunks, f, hasher, dstFd)
		return b, err
	}

	return fd.isEqualCached(chunks, f, hasher, dstFd)
}

// isEqualMixed is used when the file size is larger than the amount of bytes
// we can precalculate. First the precalculated digests are used, then the
// original destination file is read and the pointer moved to the last read
// byte by the precalculation routine, until a difference is found or an EOF is
// encountered.
func (fd *FileData) isEqualMixed(chunks int, f *os.File, hasher hash.Hash, dstFd FileData) (bool, error) {
	if len(dstFd.Digests) > 0 {
		equal, err := fd.isEqualCached(fd.synchro.MaxChunks, f, hasher, dstFd)
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
	defer dstF.Close()
	// Go to the last read byte
	if dstFd.CurByte > 0 {
		_, err = dstF.Seek(dstFd.CurByte, 0)
		if err != nil {
			log.Printf("error seeking byte %d: %s", dstFd.CurByte, err)
			return false, err
		}
	}
	dstHasher, err := dstFd.getHasher()
	if err != nil {
		log.Print("error getting hasher for %s: %s", dstFd.String(), err)
		return false, err
	}
	//	dH := Hash256{}
	//	sH := Hash256{}
	// Check until EOF or a difference is found
	dstReader := bufio.NewReaderSize(dstF, int(fd.synchro.chunkSize))
	srcReader := bufio.NewReaderSize(f, int(fd.synchro.chunkSize))
	for {
		s, err := io.CopyN(hasher, srcReader, fd.synchro.chunkSize)
		if err != nil && err != io.EOF {
			log.Printf("error copying src hasher for %s: %s", fd.RootPath(), err)
			return false, err
		}
		d, err := io.CopyN(dstHasher, dstReader, fd.synchro.chunkSize)
		if err != nil && err != io.EOF {
			log.Printf("error copying dst hasher for %s: %s", dstFd.RootPath(), err)
			return false, err
		}
		if d != s { // if the bytes copied were different, return false
			return false, nil
		}
		//		copy(dH[:], dstHasher.Sum(nil))
		//		copy(sH[:], hasher.Sum(nil))
		//		if Hash256(dH) != Hash256(sH) {
		if fmt.Sprintf("%x", dstHasher.Sum(nil)) != fmt.Sprintf("%x", hasher.Sum(nil)) {
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
		_, err := io.CopyN(hasher, f, fd.synchro.chunkSize)
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
