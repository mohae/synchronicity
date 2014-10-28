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
)

const (
	actionNone   actionType = iota
	actionNew               // creates new file in dst; doesn't exist in dst
	actionCopy              // copy file from src to dst; contents are different.
	actionDelete            // delete file from dst; doesn't exist in source
	actionUpdate            // update file properties in dst; contents same but properties diff.
)

type actionType int

func (a actionType) String() string {
	switch a {
	case actionNone:
		return "duplicate"
	case actionNew:
		return "new"
	case actionCopy:
		return "copy"
	case actionDelete:
		return "delete"
	case actionUpdate:
		return "update"
	}
	return "unknown"
}

// Defaults for new Synchro objects.

// Whether or not orphaned files should be deleted. Orphaned files are files
// that exist in the destination but not in the source.
//
// Sync() ignores this bool.
var Delete bool
var TimeLayout string // a valid time.Time layout string
var cpuMultiplier int // 0 == 1, default == 2
var maxProcs int      // 0 == 1; default == runtime.NumCPU * cpuMultiplier
var cpu int = runtime.NumCPU()

func init() {
	TimeLayout = "2006-01-02:15:04:05MST"
	SetCPUMultiplier(2)

}

// SetCPUMultiplier sets both the multipler and the maxProcs.
// If the multiplier is <= 0, 0 is used
func SetCPUMultiplier(i int) {
	if cpuMultiplier <= 0 {
		cpuMultiplier = 1
		maxProcs = cpu
	} else {
		cpuMultiplier = i
		maxProcs = cpu * i
	}
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
	// This lock structure is not used for walk/file channel related things.
	lock             sync.Mutex
	UseFullpath      bool
	Delete           bool // mutually exclusive with synch
	PreserveFileProp bool // Preserve file properties(uid, gid, mode)
	// Filepaths to operate on
	src     string
	srcFull string // the fullpath version of src
	dst     string
	dstFull string // the dstFull version of dst
	// A map of all the fileInfos by path
	dstFileData map[string]FileData
	srcFileData map[string]FileData
	// Sync operation modifiers
	// TODO wire up support for attrubute overriding
	Owner int
	Group int
	Mode  os.FileMode
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
	// TODO File time based filtering
	Newer      string
	NewerMTime time.Time
	NewerFile  string
	// Output layout for time
	OutputTimeLayout string
	// Processing queues
	copyCh   chan FileData
	delCh    chan string
	updateCh chan FileData
	// Other Counters
	newCount    counter
	copyCount   counter
	delCount    counter
	updateCount counter
	dupCount    counter
	skipCount   counter
	t0          time.Time
	𝛥t          float64
}

var unsetTime time.Time

// New returns an initialized Synchro. Any overrides need to be done prior
// to a Synchro operation.
func New() *Synchro {
	return &Synchro{
		dstFileData:      map[string]FileData{},
		Delete:           Delete,
		ExcludeExt:       []string{},
		IncludeExt:       []string{},
		OutputTimeLayout: TimeLayout,
		newCount:         newCounter("created"),
		copyCount:        newCounter("copied"),
		delCount:         newCounter("deleted"),
		updateCount:      newCounter("updated"),
		dupCount:         newCounter("duplicates and not updated"),
		skipCount:        newCounter("skipped"),
	}
}

func (s *Synchro) setDelta() {
	s.𝛥t = float64(time.Since(s.t0)) / 1e9
}

func (s *Synchro) Delta() float64 {
	return s.𝛥t
}

func (s *Synchro) Message() string {
	var msg bytes.Buffer
	s.setDelta()
	msg.WriteString(s.src)
	msg.WriteString(" was pushed to ")
	msg.WriteString(s.dst)
	msg.WriteString(" in ")
	msg.WriteString(strconv.FormatFloat(s.𝛥t, 'f', 4, 64))
	msg.WriteString(" seconds\n")
	msg.WriteString(s.newCount.String())
	msg.WriteString("\n")
	msg.WriteString(s.copyCount.String())
	msg.WriteString("\n")
	msg.WriteString(s.updateCount.String())
	msg.WriteString("\n")
	msg.WriteString(s.dupCount.String())
	msg.WriteString("\n")
	msg.WriteString(s.delCount.String())
	msg.WriteString("\n")
	msg.WriteString(s.skipCount.String())
	msg.WriteString("\n")
	return msg.String()
}

// Push pushes the contents of src to dst.
//    * Existing files that are the same are ignored
//    * Modified files are overwritten, even if dst is newer
//    * New files are created.
//    * Files in destination not in source are deleted.
func (s *Synchro) Push(src, dst string) (string, error) {
	s.t0 = time.Now()
	fmt.Printf("Start push of %q to %q\n", src, dst)
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
		log.Printf("%s\n", err)
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
		return "", err
	}
	return s.Message(), nil
}

// Pull is just a Push from dst to src
func (s *Synchro) Pull(src, dst string) (string, error) {
	return s.Push(dst, src)
}

func (s *Synchro) filepathWalkDst() error {
	var fullpath string
	visitor := func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.Mode()&os.ModeType != 0 {
			return nil // skip special files
		}
		return s.addDstFile(fullpath, p, fi, err)
	}
	fullpath, err := filepath.Abs(s.dst)
	if err != nil {
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
		return err
	}
	if !process {
		s.addSkipStats(fi)
		return nil
	}
	// Check path information to see if this should be processed.
	process, err = s.filterPath(root, p)
	if err != nil {
		return err
	}
	if !process {
		s.addSkipStats(fi)
		return nil
	}
	var relPath string
	relPath, err = filepath.Rel(root, p)
	if err != nil {
		log.Printf("%s\n", err)
		return err
	}
	if relPath == "." {
		s.addSkipStats(fi)
		return nil
	}
	// Gotten this far, hash it and add it to the dst list
	fd := NewFileData(s.src, filepath.Dir(relPath), fi)
	fd.SetHash()
	s.lock.Lock()
	s.dstFileData[fd.String()] = fd
	s.lock.Unlock()
	return nil
}

// procesSrc indexes the source directory, figures out what's new and what's
// changed, and triggering the appropriate action. If an error is encountered,
// it is returned.
func (s *Synchro) processSrc() error {
	// Push source to dest
	s.copyCh = make(chan FileData)
	s.delCh = make(chan string)
	s.updateCh = make(chan FileData)
	// Start the channel for copying
	copyWait, err := s.copy()
	if err != nil {
		return err
	}
	// Start the channel for delete
	delWait, err := s.delete()
	if err != nil {
		return err
	}
	// Start the channel for update
	updateWait, err := s.update()
	if err != nil {
		return err
	}
	var fullpath string
	visitor := func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.Mode()&os.ModeType != 0 {
			return nil // skip special files
		}
		return s.addSrcFile(fullpath, p, fi, err)
	}
	fullpath, err = filepath.Abs(s.src)
	if err != nil {
		return err
	}
	err = walk.Walk(fullpath, visitor)
	if s.Delete {
		err = s.deleteOrphans()
		if err != nil {
			log.Printf("%s\n", err)
			return err
		}
	}
	close(s.copyCh)
	copyWait.Wait()
	close(s.delCh)
	delWait.Wait()
	close(s.updateCh)
	updateWait.Wait()
	return err
}

// addSrcFile adds the info about the source file, then calls setAction to
// determine what action should be done, if any.
func (s *Synchro) addSrcFile(root, p string, fi os.FileInfo, err error) error {
	// We don't add directories, they are handled by the mkdir process
	if fi.IsDir() {
		return nil
	}
	// Check fileInfo to see if this should be added to archive
	process, err := s.filterFileInfo(fi)
	if err != nil {
		return err
	}
	if !process {
		return nil
	}
	// Check path information to see if this should be added.
	process, err = s.filterPath(root, p)
	if err != nil {
		return err
	}
	if !process {
		s.addSkipStats(fi)
		return nil
	}
	var relPath string
	relPath, err = filepath.Rel(root, p)
	if err != nil {
		log.Printf("%s\n", err)
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
	typ, err := s.setAction(relPath, fi)
	if err != nil {
		return err
	}
	switch typ {
	case actionNew:
		s.addNewStats(fi)
	case actionCopy:
		s.addCopyStats(fi)
	case actionUpdate:
		s.addUpdateStats(fi)
	}
	// otherwise its assumed to be actionNone
	return nil
}

// setAction examines the src/dst to determine what should be done.
// Possible action outcomes are:
//    Do nothing (file contents and properties are the same)
//    Update (file contents are the same, but properties are diff)
//    Copy (file contents are different)
//    New (new file)
func (s *Synchro) setAction(relPath string, fi os.FileInfo) (actionType, error) {
	newFd := NewFileData(s.src, relPath, fi)
	fd, ok := s.dstFileData[newFd.String()]
	if !ok {
		s.copyCh <- newFd
		return actionNew, nil
	}
	// See the processed flag on existing dest file, for delete processing,
	// if applicable.
	fd.Processed = true
	s.dstFileData[newFd.String()] = fd
	// copy if its not the same as dest
	𝛥, err := newFd.isEqual(fd)
	if err != nil {
		return actionNone, err
	}
	if 𝛥 {
		s.copyCh <- newFd
		return actionCopy, nil
	}
	// update if the properties are different
	if newFd.Fi.Mode() != fd.Fi.Mode() || newFd.Fi.ModTime() != fd.Fi.ModTime() {
		s.updateCh <- newFd
		return actionUpdate, nil
	}
	// Otherwise everything is the same, its a duplicate: do nothing
	return actionNone, nil
}

func (s *Synchro) copy() (*sync.WaitGroup, error) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() error {
		defer wg.Done()
		// process channel as we get items
		for fd := range s.copyCh {
			// make any directories that are missing from the path
			err := s.mkDirTree(fd.Dir)
			if err != nil {
				log.Printf("%s\n", err)
				return err
			}
			r, err := os.Open(filepath.Join(s.src, fd.Dir, fd.Fi.Name()))
			if err != nil {
				log.Printf("%s\n", err)
				return err
			}
			defer r.Close()
			dst := filepath.Join(s.dst, fd.Dir, fd.Fi.Name())
			var w *os.File
			w, err = os.Create(dst)
			if err != nil {
				log.Printf("%s\n", err)
				return err
			}
			defer w.Close()
			_, err = io.Copy(w, r)
			if err != nil {
				log.Printf("%s\n", err)
				return err
			}
		}
		return nil
	}()
	return &wg, nil
}

func (s *Synchro) delete() (*sync.WaitGroup, error) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() error {
		defer wg.Done()
		for fname := range s.delCh {
			err := os.Remove(fname)
			if err != nil {
				log.Printf("%s\n", err)
				return err
			}
		}
		return nil
	}()
	return &wg, nil
}

// update updates the fi of a file: currently mode, mdate, and atime
// this is done on files whose contents haven't changes (hashes are equal) but
// their properties have.
// TODO add support for uid, gid
func (s *Synchro) update() (*sync.WaitGroup, error) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() error {
		defer wg.Done()
		for fd := range s.updateCh {
			p := filepath.Join(s.dst, fd.Fi.Name())
			os.Chmod(p, fd.Fi.Mode())
			os.Chtimes(p, fd.Fi.ModTime(), fd.Fi.ModTime())
		}
		return nil
	}()
	return &wg, nil
}

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
		return false, err
	}
	if !b {
		return false, nil
	}
	b, err = s.excludeFile(root, p)
	if err != nil {
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
			log.Printf("%s\n", err)
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

//func formattedNow() string {
//	return time.Now().Local().Format()
//}

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

// mkDirTree takes a directory path and makes sure it exists. If it doesn't
// exist it will create it, this includes any parent directories that don't
// already exist. This is needed because we process requests as we get them.
// This means we can encounter a child, or later descendent, before its
// ancestor.
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
		err := fmt.Errorf("mkdirTree: %s not a directory", filepath.Join(s.dst, p))
		log.Printf("%s\n", err)
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
			log.Printf("%s\n", err)
			return err
		}
		err = os.Mkdir(dstP, fi.Mode())
		if err != nil {
			log.Printf("%s\n", err)
			return err
		}
		err = os.Chtimes(dstP, fi.ModTime(), fi.ModTime())
		if err != nil {
			log.Printf("%s\n", err)
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
		s.addDelStats(fd.Fi)
		s.delCh <- filepath.Join(s.dst, fd.String())
	}
	return nil
}

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
