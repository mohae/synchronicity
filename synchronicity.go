// Copyright 2014 Joel Scoble (github.com/mohae) All rights reserved.
// Use of this source code is governed by a BSD-style license that
// can be found in the LICENSE file.
package synchronicity

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelTJones/walk"
	"gopkg.in/tomb.v2"
)

type equalityType int

const (
	UnknownEquality         equalityType = iota 
	BasicEquality				// compare bytes for equality check
	DigestEquality                            // compare digests for equality check: digest entire file at once
	ChunkedEquality		                  // compare digests for equality check digest using chunks
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

type taskType int

const (
	nilTask   taskType = iota
	newTask               // creates new file in dst; doesn't exist in dst
	copyTask              // copy file from src to dst; contents are different.
	deleteTask            // delete file from dst; doesn't exist in source
	updateTask            // update file properties in dst; contents same but properties diff.
)

func (a taskType) String() string {
	switch a {
	case nilTask:
		return "duplicate"
	case newTask:
		return "new"
	case copyTask:
		return "copy"
	case deleteTask:
		return "delete"
	case updateTask:
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
var MaxChunks = 4                // Modify directly to change buffered hashes
var chunkSize = int64(2 * 1024) // use 16k chunks as default; cuts down on garbage
var maxProcs int
var cpuMultiplier int // 0 == 1, default == 2
var cpu int = runtime.NumCPU()
// SetChunkSize sets the chunkSize as 1k * i, i.e. 4 == 4k chunkSize
// If the multiplier, i, is <= 0, the default is used, 4.
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
	mainSynchro = New()
	defaultEqualityType = BasicEquality
}

var mainSynchro *Synchro

// SetMaxProcs sets the maxProcs to the passed value, or 1 for <= 0.
func (s *Synchro) SetMaxProcs(i int) {
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

// Synchro provides information about a sync operation. This trades memory for
// CPU.
type Synchro struct {
	maxProcs int // maxProcs for this synchro.
	// This lock structure is not used for walk/file channel related things.
	PreDigest          bool // precompute digests for files
	equalityType       equalityType
	Delete             bool     // mutually exclusive with synch
	PreserveProperties bool     // Preserve file properties(uid, gid, mode)
	hashType           hashType // Hashing algorithm used for digests
	chunkSize          int64
	MaxChunks          int
	// Filepaths to operate on
	src     string
	dst     string
	// A map of all the fileInfos by path
	dstFileData map[string]*FileData
	//	srcFileData map[string]FileData
	// Sync operation modifiers
	// TODO wire up support for attrubute overriding
	Owner int
	Group int
	Mod   int64
	// Exclude file processing
	Exclude         string
	ExcludeExt      []string
	ExcludeExtCount int
	ExcludeAnchored string
	// Include file processing
	Include         string
	IncludeExt      []string
	IncludeExtCount int
	IncludeAnchored string
	// time based filtering
	Newer      string
	NewerMTime time.Time
	NewerFile  string
	// Processing queue
	taskCh   chan *FileData
	// Queue management
	tomb tomb.Tomb
	wg sync.WaitGroup
	// Counters and update lock
	lock        sync.Mutex
	newCount    counter
	copyCount   counter
	delCount    counter
	updateCount counter
	dupCount    counter
	skipCount   counter
	// timer
	t0 time.Time
	𝛥t float64
	tombstone	tomb.Tomb
}

var unsetTime time.Time

// New returns an initialized Synchro. Any overrides need to be done prior
// to a Synchro operation.
func New() *Synchro {
	s := &Synchro{
		maxProcs:           maxProcs,
		dstFileData:        map[string]*FileData{},
		chunkSize:          chunkSize,
		MaxChunks:          MaxChunks,
		equalityType:       defaultEqualityType,
		hashType:	    defaultHashType,
		Delete:             true,
		PreserveProperties: true,
		ExcludeExt:         []string{},
		IncludeExt:         []string{},
		newCount:           newCounter("created"),
		copyCount:          newCounter("copied"),
		delCount:           newCounter("deleted"),
		updateCount:        newCounter("updated"),
		dupCount:           newCounter("duplicates and not updated"),
		skipCount:          newCounter("skipped"),
	}
	if s.equalityType == ChunkedEquality {
		s.SetDigestChunkSize(0)
	}
	s.tomb.Go(s.doWork)
	return s
}

func (s *Synchro) SetEqualityType(e equalityType) {
	s.equalityType = e
	if s.equalityType == ChunkedEquality {
		s.SetDigestChunkSize(0)
	}
}

// SetDigestChunkSize either sets the chunkSize, when a value > 0 is received, 
// using the recieved int as a multipe of 1024 bytes. If the received value is
// 0, it will use the current chunksize * 4.
func (s *Synchro) SetDigestChunkSize(i int) {
	// if its a non-zero value use it
	if i > 0 {
		s.chunkSize = int64(i * 1024)
		return
	}
	s.chunkSize = s.chunkSize * 4
}

func SetEqualityType(e equalityType) {
	mainSynchro.SetEqualityType(e)
}

// DstFileData returns the map of FileData accumulated during the walk of the
// destination.
func (s *Synchro) DstFileData() map[string]*FileData {
	return s.dstFileData
}

// DstFileData returns the map of FileData accumulated during the walk of the
// destination.
func DstFileData() map[string]*FileData {
	return mainSynchro.DstFileData()
}

func (s *Synchro) setDelta() {
	s.𝛥t = float64(time.Since(s.t0)) / 1e9
}

// Delta returns the 𝛥 between the start and end of an operation/
func (s *Synchro) Delta() float64 {
	return s.𝛥t
}

// Delta returns the 𝛥 between the start and end of an operation/
func Delta() float64 {
	return mainSynchro.Delta()
}

// SetDelete is used to set the mainSynchro's delete flag. When working
// directly with a Synchro object, just set it, Synchro.Delete, instead of
// calling this function.
func SetDelete(b bool) {
	mainSynchro.Delete = b
}

// Message returns stats about the last Synch.
func (s *Synchro) Message() string {
	var msg bytes.Buffer
	s.setDelta()
	msg.WriteString(s.src)
	msg.WriteString(" was pushed to ")
	msg.WriteString(s.dst)
	msg.WriteString(" in ")
	msg.WriteString(strconv.FormatFloat(s.𝛥t, 'f', 4, 64))
	msg.WriteString(" seconds\n")
	if s.newCount.Files > 0 {
		msg.WriteString(s.newCount.String())
		msg.WriteString("\n")
	}
	if s.copyCount.Files > 0 {
		msg.WriteString(s.copyCount.String())
		msg.WriteString("\n")
	}
	if s.updateCount.Files > 0 {
		msg.WriteString(s.updateCount.String())
		msg.WriteString("\n")
	}
	if s.dupCount.Files > 0 {
		msg.WriteString(s.dupCount.String())
		msg.WriteString("\n")
	}
	if s.delCount.Files > 0 {
		msg.WriteString(s.delCount.String())
		msg.WriteString("\n")
	}
	if s.skipCount.Files > 0 {
		msg.WriteString(s.skipCount.String())
		msg.WriteString("\n")
	}
	return msg.String()
}

// Message returns stats about the last Synch.
func Message() string {
	return mainSynchro.Message()
}

// Push pushes the contents of src to dst.
//    * Existing files that are the same are ignored
//    * Modified files are overwritten, even if dst is newer
//    * New files are created.
//    * Files in destination not in source may be deleted.
func (s *Synchro) Push(src, dst string) (string, error) {
	s.t0 = time.Now()
	Logf("Start push of %q to %q\n", src, dst)
	// check to see if something was passed
	if src == "" {
		return "", fmt.Errorf("source not set")
	}
	if dst == "" {
		return "", fmt.Errorf("destination not set")
	}
	// Check for existence of src
	_, err := os.Stat(src)
	if err != nil {
		log.Printf("error stat of %s: %s\n", src, err)
		return "", err
	}
	// TODO check that destination is writable instead of relying on a later error

	// Now get to work
	s.src = src
	s.dst = dst
	// walk destination first
	s.filepathWalkDst()

	// walk source: this does all of the sync evaluations
	err = s.processSrc()
	if err != nil {
		log.Printf("error processing %s: %s", s.src, err)
		return "", err
	}
	return s.Message(), nil
}

// Push pushes the contents of src to dst.
//    * Existing files that are the same are ignored
//    * Modified files are overwritten, even if dst is newer
//    * New files are created.
//    * Files in destination not in source may be deleted.
func Push(src, dst string) (string, error) {
	return mainSynchro.Push(src, dst)
}

// Pull is just a Push from dst to src
func (s *Synchro) Pull(src, dst string) (string, error) {
	return s.Push(dst, src)
}

// Pull is just a Push from dst to src
func Pull(src, dst string) (string, error) {
	return mainSynchro.Pull(src, dst)
}

func (s *Synchro) filepathWalkDst() error {
	var fullpath string
	visitor := func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.Mode()&os.ModeType != 0 {
			if err != nil {
				log.Printf("error walking %s: %s", p, err)
			}
			return nil // skip special files
		}
		return s.addDstFile(fullpath, p, fi, err)
	}
	fullpath, err := filepath.Abs(s.dst)
	if err != nil {
		log.Printf("an error occurred while getting absolute path for %q: %s", s.dst, err)
		return err
	}
	walk.Walk(fullpath, visitor)
	return nil
}

// addDstFile just adds the info about the destination file
func (s *Synchro) addDstFile(root, p string, fi os.FileInfo, err error) error {
	// We don't add directories, those are handled by their files.
	if fi.IsDir() {
		return nil
	}
	// Check fileInfo to see if this should be added to archive
	process, err := s.filterFileInfo(fi)
	if err != nil {
		log.Printf("an error occurred while filtering file info: %s", err)
		return err
	}
	if !process {
		return nil
	}
	// Check path information to see if this should be processed.
	process, err = s.filterPath(root, p)
	if err != nil {
		log.Printf("an error occurred while filtering path: %q %s", p, err)
		return err
	}
	if !process {
		return nil
	}
	var relPath string
	relPath, err = filepath.Rel(root, p)
	if err != nil {
		log.Printf("an error occurred while getting relative path for %s: %s", p, err)
		return err
	}
	if relPath == "." {
		return nil
	}
	// Gotten this far, hash it and add it to the dst list
	fd := NewFileData(s.src, filepath.Dir(relPath), fi, s)

	if s.PreDigest {
		fd.SetHash()
	}

	s.lock.Lock()
	s.dstFileData[fd.String()] = fd
	s.lock.Unlock()
	return nil
}

func (s *Synchro) exec() error {
	s.wg.Add(s.maxProcs)
	for i := 0; i < 10; i++ {
		s.tomb.Go(s.doWork)
	}
	return nil
}

func (s *Synchro) Stop() error {
	s.tomb.Kill(nil)
	return s.tomb.Wait()
}

func (s *Synchro) doWork() error {
	var inTask <-chan *FileData
	for {
		select {
		case <-s.tomb.Dying():
			return nil
		case <- s.taskCh:
			inTask = s.taskCh
			fd := <-inTask
			switch fd.taskType {
			case copyTask:
				err := s.copyFile(fd)
				if err != nil {
					return err
				}
			case updateTask:
				err := s.updateFile(fd)
				if err != nil {
					return err
				}
			case deleteTask:
				err := s.deleteFile(fd)
				if err != nil {
					return err
				}
			default:
				err := fmt.Errorf("unsupported task type for %s", fd.RelPath())
				close(s.taskCh)
				return err
			}
		}
	}
	return nil
}	
	
// procesSrc indexes the source directory, figures out what's new and what's
// changed, and triggering the appropriate task. If an error is encountered,
// it is returned. The tomb is to manage the processes
func (s *Synchro) processSrc() error {
	s.taskCh = make(chan *FileData, 1)
	err := s.exec()
	// Push source to dest
	if err != nil {
		log.Printf("an error occurred while processing files: %s", err)
		return err
	}
	var fullpath string
	visitor := func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.Mode()&os.ModeType != 0 {
			if err != nil {
				log.Printf("error walking %s: %s", p, err)
			}
			return nil // skip special files
		}
		return s.addSrcFile(fullpath, p, fi, err)
	}
	fullpath, err = filepath.Abs(s.src)
	if err != nil {
		log.Printf("an error occurred while getting absolute path for %q: %s", s.src, err)
		return err
	}
	err = walk.Walk(fullpath, visitor)
	if err != nil {
		log.Printf("synchronicity received a walk error: %s\n", err)
		return err
	}
	close(s.taskCh)
	return err
}

// addSrcFile adds the info about the source file, then calls setTast to
// determine what task should be done, if any.
func (s *Synchro) addSrcFile(root, p string, fi os.FileInfo, err error) error {
	// We don't add directories, they are handled by the mkdir process
	if fi.IsDir() {
		return nil
	}
	// Check fileInfo to see if this should be added to archive
	process, err := s.filterFileInfo(fi)
	if err != nil {
		log.Printf("an error occurred while filtering src file info %s: %s", fi.Name(), err)
		return err
	}
	if !process {
		return nil
	}
	// Check path information to see if this should be added.
	process, err = s.filterPath(root, p)
	if err != nil {
		log.Printf("an error occurred while filtering src path info %s: %s", p, err)
		return err
	}
	if !process {
		return nil
	}
	var relPath string
	relPath, err = filepath.Rel(root, p)
	if err != nil {
		log.Printf("an error occurred while generating the relative path for %q: %s\n", p, err)
		return err
	}
	if relPath == "." { // don't do current dir, this shouldn't occur
		return nil
	}
	if relPath == fi.Name() { // if we end up with the filename, use nothing
		relPath = ""
	} else {
		// extract the directory
		relPath = filepath.Dir(relPath)
		if relPath == "." {
			relPath = ""
		}
	}
	// determine if it should be copied
	task, err := s.setTask(relPath, fi)
	if err != nil {
		log.Printf("an error occurred while setting the task for %s: %s", filepath.Join(relPath, fi.Name()), err)
		return err
	}
	// Add stats to the appropriate accumulater.
	switch task {
	case newTask:
		s.addNewStats(fi)
	case copyTask:
		s.addCopyStats(fi)
	case updateTask:
		s.addUpdateStats(fi)
	case nilTask:
		s.addDupStats(fi)
	}
	return nil
}

// setTask examines the src/dst to determine what should be done.
// Possible task outcomes are:
//    Do nothing (file contents and properties are the same)
//    Update (file contents are the same, but properties are diff)
//    Copy (file contents are different)
//    New (new file)
func (s *Synchro) setTask(relPath string, fi os.FileInfo) (taskType, error) {
	srcFd := NewFileData(s.src, relPath, fi, s)
	fd, ok := s.dstFileData[srcFd.String()]
	if !ok {
		srcFd.taskType = newTask
		s.taskCh <- srcFd
		return srcFd.taskType, nil
	}
	// See the processed flag on existing dest file, for delete processing,
	// if applicable.
	fd.Processed = true
	s.dstFileData[srcFd.String()] = fd
	// copy if its not the same as dest
	Equal, err := srcFd.isEqual(fd)
	if err != nil {
		log.Printf("an error occurred while checking equality for %s: %s", srcFd.String(), err)
		return nilTask, err
	}
	if !Equal {
		srcFd.taskType = copyTask
		s.taskCh <- srcFd
		return srcFd.taskType, nil
	}
	// update if the properties are different
	if srcFd.Fi.Mode() != fd.Fi.Mode() || srcFd.Fi.ModTime() != fd.Fi.ModTime() {
		srcFd.taskType = updateTask
		s.taskCh <- srcFd
		return srcFd.taskType, nil
	}
	// Otherwise everything is the same, its a duplicate: do nothing
	return nilTask, nil
}

// copyFile copies the file.
// TODO should a copy of the file be made in a tmp directory while its hash
// is being computed, or in memory. Source would not need to be read and
// processed twice this way. If a copy operation is to occur, the tmp file
// gets renamed to the destination, otherwise the tmp directory is cleaned up
// at the end of the run.
func (s *Synchro) copyFile(fd *FileData) error {
	// make any directories that are missing from the path
	err := s.mkDirTree(fd.Dir)
	if err != nil {
		log.Printf("error making the directories for %s: %s\n", fd.Dir, err)
		return err
	}
	r, err := os.Open(filepath.Join(s.src, fd.Dir, fd.Fi.Name()))
	if err != nil {
		log.Printf("error opening %s: %s\n", filepath.Join(s.src, fd.Dir, fd.Fi.Name()), err)
		return err
	}
	dst := filepath.Join(s.dst, fd.Dir, fd.Fi.Name())
	var w *os.File
	w, err = os.Create(dst)
	if err != nil {
		log.Printf("error creating %s: %s\n", dst, err)
		r.Close()
		return err
	}
	_, err = io.Copy(w, r)
	r.Close()
	w.Close()
	if err != nil {
		log.Printf("error copying %s to %s: %s\n", filepath.Join(s.src, fd.Dir, fd.Fi.Name()), dst, err)
		return err
	}
	return nil
}

// deleteFile deletes any file for which it receives.
func (s *Synchro) deleteFile(fd *FileData) error {
	err := os.Remove(fd.RootPath())
	if err != nil {
		log.Printf("error deleting %s: %s\n", fd.RootPath(), err)
		return err
	}
	return nil
}

// update updates the fi of a file: currently mode, mdate, and atime
// this is done on files whose contents haven't changes (Digests are equal) but
// their properties have.
// TODO add supportE for uid, gid
func (s *Synchro) updateFile(fd *FileData) error {
	p := filepath.Join(s.dst, fd.String())
	err := os.Chmod(p, fd.Fi.Mode())
	if err != nil {
		log.Printf("error updating mod of %s: %s", p, err)
		return err
	}
	err = os.Chtimes(p, fd.Fi.ModTime(), fd.Fi.ModTime())
	if err != nil {
		log.Printf("error updating mtime of %s: %s", p, err)
		return err
	}
	return nil
}

// See if the file should be filtered
func (s *Synchro) filterFileInfo(fi os.FileInfo) (bool, error) {
	// Don't add symlinks, otherwise would have to code some cycle
	// detection amongst other stuff.
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		return false, nil
	}
	if s.NewerMTime != unsetTime {
		if !fi.ModTime().After(s.NewerMTime) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Synchro) filterPath(root, p string) (bool, error) {
	if strings.HasSuffix(root, p) {
		return false, nil
	}
	b, err := s.includeFile(root, p)
	if err != nil {
		log.Printf("error: include file %s: %s", p, err)
		return false, err
	}
	if !b {
		return false, nil
	}
	b, err = s.excludeFile(root, p)
	if err != nil {
		log.Printf("error: exclude files %s: %s", p, err)
		return false, err
	}
	if b {
		return false, nil
	}
	return true, nil
}

func (s *Synchro) includeFile(root, p string) (bool, error) {
	if s.IncludeAnchored != "" {
		if strings.HasPrefix(filepath.Base(s.IncludeAnchored), p) {
			return true, nil
		}
	}
	// since we are just evaluating a file, we use match and look at the
	// fullpath
	if s.Include != "" {
		matches, err := filepath.Match(s.Include, filepath.Join(root, p))
		if err != nil {
			log.Printf("error checking for includeFile match %s and %s: %s", root, p, err)
			return false, err
		}

		if matches {
			return true, nil
		}
	}
	if s.IncludeExtCount > 0 {
		for _, ext := range s.IncludeExt {
			if strings.HasSuffix(filepath.Base(p), "."+ext) {
				return true, nil
			}
		}
		return false, nil
	}
	return true, nil
}

func (s *Synchro) excludeFile(root, p string) (bool, error) {
	if s.ExcludeAnchored != "" {
		if strings.HasPrefix(filepath.Base(p), s.ExcludeAnchored) {
			return true, nil
		}
	}
	// since we are just evaluating a file, we use match and look at the
	// fullpath
	if s.Exclude != "" {
		matches, err := filepath.Match(s.Exclude, filepath.Join(root, p))
		if err != nil {
			log.Printf("error checking for excludeFile match: %s and %s: %s", root, p, err)
			return false, err
		}

		if matches {
			return true, nil
		}
	}
	if s.ExcludeExtCount != 0 {
		for _, ext := range s.ExcludeExt {
			if strings.HasSuffix(filepath.Base(p), "."+ext) {
				return true, nil
			}
		}
	}
	return false, nil
}

// mkDirTree takes a directory path and makes sure it exists. If it doesn't
// exist it will create it, this includes any parent directories that don't
// already exist. This is needed because we process requests as we get them.
// This means we can encounter a child, or later descendent, before its
// ancestor. We also want to preserve as many properties as we can.
//
func (s *Synchro) mkDirTree(p string) error {
	if p == "" {
		return nil
	}
	fi, err := os.Stat(filepath.Join(s.dst, p))
	if err == nil {
		if fi.IsDir() { // if the parent already exists
			return nil
		}
		err := fmt.Errorf("%s not a directory", filepath.Join(s.dst, p))
		log.Printf("error: mkDirTree: %s\n", err)
		return err
	}
	pieces := strings.Split(p, "/")
	dstP := s.dst
	srcP := s.src
	// from the root (our src, dst) make sure the directory exists create it
	// if it doesn't. This is done until the last element in the path has
	// been checked, this is because the path can be missing at any point
	// along the requested path.
	for _, piece := range pieces {
		// keep in synch, simplifies not found processing
		dstP = filepath.Join(dstP, piece)
		srcP = filepath.Join(srcP, piece)
		// see if dst exiists
		_, err = os.Stat(dstP)
		if err == nil { // exists, move on
			continue
		}
		fi, err := os.Stat(srcP)
		if err != nil {
			log.Printf("error mkDirTree Stat %s: %s\n", srcP, err)
			return err
		}
		err = os.Mkdir(dstP, fi.Mode())
		if err != nil {
			log.Printf("error mkDirTree Mkdir %s: %s\n", dstP, err)
			return err
		}
		err = os.Chtimes(dstP, fi.ModTime(), fi.ModTime())
		if err != nil {
			log.Printf("error mkDirTree Chtimes %s: %s\n", dstP, err)
			return err
		}
		// TODO owner, group setting
	}
	return nil
}

// deleteOrphans delete any files in the destination that were not in the
// source. This only happens if a file wasn't processed
func (s *Synchro) deleteOrphans() error {
	for _, fd := range s.dstFileData {
		if fd.Processed {
			continue // processed files aren't orphaned
		}
		s.taskCh <- fd
	}
	return nil
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

// increments the file count.
func (s *Synchro) addNewStats(fi os.FileInfo) {
	s.lock.Lock()
	s.newCount.Files++
	s.newCount.Bytes += fi.Size()
	s.lock.Unlock()
}

func (s *Synchro) addDelStats(fi os.FileInfo) {
	s.lock.Lock()
	s.delCount.Files++
	s.delCount.Bytes += fi.Size()
	s.lock.Unlock()
}

func (s *Synchro) addCopyStats(fi os.FileInfo) {
	s.lock.Lock()
	s.copyCount.Files++
	s.copyCount.Bytes += fi.Size()
	s.lock.Unlock()
}

func (s *Synchro) addUpdateStats(fi os.FileInfo) {
	s.lock.Lock()
	s.updateCount.Files++
	s.updateCount.Bytes += fi.Size()
	s.lock.Unlock()
}

func (s *Synchro) addDupStats(fi os.FileInfo) {
	s.lock.Lock()
	s.dupCount.Files++
	s.dupCount.Bytes += fi.Size()
	s.lock.Unlock()
}

func (s *Synchro) addSkipStats(fi os.FileInfo) {
	s.lock.Lock()
	s.skipCount.Files++
	s.skipCount.Bytes += fi.Size()
	s.lock.Unlock()
}
