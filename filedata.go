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

type FileData struct {
	actionType
	Processed bool
	loaded    bool // whether or not this file's data has been loaded.
	Digests   []Hash256
	synchro   *Synch        // reference back to parent
	CurByte   int64         // for when the while file hasn't been hashed and
	Root      string        // the relative root of this file: allows for synch support
	Dir       string        // relative path to parent directory of Fi
	Buf       *bytes.Buffer // Cache read files; trade memory for io
	BufPos    int64         // position in buffer
	Fi        os.FileInfo
}

// Returns a FileData struct for the passed file using the defaults.
// Set any overrides before performing an operation.
func NewFileData(root, dir string, fi os.FileInfo, s *Synch) *FileData {
	if dir == "." {
		dir = ""
	}
	fd := &FileData{synchro: s, Root: root, Dir: dir, Fi: fi, Buf: &bytes.Buffer{}}
	return fd
}

// String is an alias to RelPath
func (fd *FileData) String() string {
	return fd.RelPath()
}

// readfile reads the passed file into the buffer
func (fd *FileData) readfile() (n int64, err error) {
	if fd.loaded { // TODO or should this trigger a reload of the file?
		return fd.Fi.Size(), nil
	}

	f, err := os.Open(fd.FullPath())
	if err != nil {
		log.Printf("FileData.readfile open file error: %s", err)
		return int64(0), err
	}
	n, err = io.Copy(fd.Buf, f)
	if err != nil {
		log.Printf("FileData.readfile copy error: %s", err)
		return n, err
	}
	fd.loaded = true
	err = f.Close()
	return n, err
}

// getHasher returns a file and the hasher to use it on. If error, return that.
// Caller is responsible for closing the file.
func (fd *FileData) getFileHasher() (f *os.File, hasher hash.Hash, err error) {
	f, err = os.Open(fd.RootPath())
	if err != nil {
		log.Printf("FileData.getFileHasher open error: %s", err)
		return
	}
	hasher, err = fd.getHasher()
	return
}

// getHasher returns a hasher or an error
func (fd *FileData) getHasher() (hasher hash.Hash, err error) {
	switch fd.synchro.hashType {
	case SHA256:
		hasher = sha256.New() //
	default:
		err = fmt.Errorf("%s hash type not supported", fd.synchro.hashType.String())
		log.Printf("FileData.getHasher error: %s", err)
		return
	}
	return
}

// hashFile hashes the entire file.
func (fd *FileData) hashFile(f *os.File, hasher hash.Hash) error {
	_, err := io.Copy(hasher, f)
	if err != nil {
		log.Printf("FileData.hashFile copy error: %s/n", err)
		return err
	}
	h := Hash256{}
	copy(h[:], hasher.Sum(nil))
	fd.Digests = append(fd.Digests, Hash256(h))
	return nil
}

// isEqual compares the current file with the passed file and returns
// whether or not they are equal. If the file length is greater than our
// checksum buffer, the rest of the file is read in chunks, until EOF or
// a difference is found, whichever comes first.
//
// If they are of different lengths, we assume they are different
func (fd *FileData) isEqual(compare *FileData) (bool, error) {
	if fd.Fi.Size() != compare.Fi.Size() { // Check to see if size is different first
		return false, nil
	}
	// otherwise, examine the file contents
	fd.readfile()
	compare.readfile()
	switch fd.synchro.equalityType {
	case BasicEquality:
		return fd.byteCompare(compare)
	case DigestEquality, ChunkedEquality:
		return fd.hashCompare(compare)
	}
	return false, fmt.Errorf("error: isEqual encountered an unsupported equality type %d", int(fd.synchro.equalityType))
}

func (fd *FileData) byteCompare(dstFd *FileData) (bool, error) {
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
			log.Printf("FileData.byteCompare error: %s", err)
			return false, err
		}
		dstN, err = dstF.Read(dstBuf)
		if err != nil && err != io.EOF {
			log.Printf("FileData.byteCompare error: %s", err)
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

func (fd *FileData) hashCompare(dstFd *FileData) (bool, error) {
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
	return fd.chunkedHashIsEqual(f, hasher, dstFd)
}

// chunkedHashIsEqual calculates the hash of the file, in chunks.
func (fd *FileData) chunkedHashIsEqual(f *os.File, hasher hash.Hash, dstFd *FileData) (bool, error) {
	// Otherwise check the file from the current point
	dstF, err := os.Open(dstFd.RootPath())
	if err != nil {
		log.Printf("FileData.chunkedHashIsEqual open error: %s", err)
		return false, err
	}
	// TODO write to capture error
	defer dstF.Close()
	dstHasher, err := dstFd.getHasher()
	if err != nil {
		log.Printf("FileData.chunkedHashIsEqual hasher error: %s", err)
		return false, err
	}
	//	dH := Hash256{}
	//	sH := Hash256{}
	// Check until EOF or a difference is found
	// Chunked Hash uses a larger chunk size to improve speed.
	dstReader := bufio.NewReaderSize(dstF, int(fd.synchro.chunkSize))
	srcReader := bufio.NewReaderSize(f, int(fd.synchro.chunkSize))
	for {
		s, err := io.CopyN(hasher, srcReader, fd.synchro.chunkSize)
		if err != nil && err != io.EOF {
			log.Printf("FileData.chunkedHashIsEqual copy src error %s: %s", fd.RootPath(), err)
			return false, err
		}
		d, err := io.CopyN(dstHasher, dstReader, fd.synchro.chunkSize)
		if err != nil && err != io.EOF {
			log.Printf("FileData.chunkedHashIsEqual copy src error %s: %s", dstFd.RootPath(), err)
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

func (fd *FileData) FullPath() string {
	fullroot, _ := filepath.Abs(fd.RootPath())
	return fullroot
}

// FileDatas is used for sorting FileData info
type FileDatas []*FileData

func (s FileDatas) Len() int {
	return len(s)
}

func (s FileDatas) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

// ByPath sorts by RelPath
type ByPath struct {
	FileDatas
}

func (s ByPath) Less(i, j int) bool {
	return s.FileDatas[i].RelPath() < s.FileDatas[j].RelPath()
}

// BySize sorts by filesize
type BySize struct {
	FileDatas
}

func (s BySize) Less(i, j int) bool {
	return s.FileDatas[i].Fi.Size() < s.FileDatas[j].Fi.Size()
}

// func (fd *FileData) WriteFile(w io.WriterCloser) {
//
// }
