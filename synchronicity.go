// Copyright 2014 Joel Scoble (github.com/mohae) All rights reserved.
// Use of this source code is governed by a BSD-style license that
// can be found in the LICENSE file.
package synchronicity

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MichaelTJones/walk"
)

// Defaults for new Synchro objects.
var Delete bool
var TimeLayout string
var cpuMultiplier int
var maxProcs int
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

func newCounter(n string) *counter {
	return &counter{Name: n}
}

// Synchro provides information about a sync operation.
type Synchro struct {
	// This lock structure is not used for walk/file channel related things.
	lock        sync.Mutex
	UseFullpath bool
	Delete      bool // mutually exclusive with synch
	// Filepaths to operate on
	src         string
	srcFull     string // the fullpath version of src
	dst         string
	dstFull     string // the dstFull version of dst
	dstFileInfo map[string]fileInfo
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
	// File time based filtering
	Newer      string
	NewerMTime time.Time
	NewerFile  string
	// Output layout for time
	OutputTimeLayout string
	// Processing queues
	copyCh chan string
	delCh  chan string
	// Other Counters
	newCount  *counter
	delCount  *counter
	modCount  *counter
	dupCount  *counter
	skipCount *counter
	t0        time.Time
	𝛥t        float64
}

type fileInfo struct {
	Processed bool
	Fi        os.FileInfo
}

var unsetTime time.Time

// New returns an initialized Synchro. Any overrides need to be done prior
// to a Synchro operation.
func New() *Synchro {
	return &Synchro{
		dstFileInfo:      map[string]fileInfo{},
		Delete:           Delete,
		ExcludeExt:       []string{},
		IncludeExt:       []string{},
		OutputTimeLayout: TimeLayout,
		newCount:         newCounter("new"),
		delCount:         newCounter("deleted"),
		modCount:         newCounter("modified"),
		dupCount:         newCounter("duplicate"),
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
	s.setDelta()
	return fmt.Sprintf("%q was pushed to %q in %4f seconds\n%d new files were copied\n%d files were deleted\n%d files were updated\n%d files were skipped\n%d files were duplicates and not synched\n", s.src, s.dst, s.𝛥t, s.newCount.Files, s.delCount.Files, s.modCount.Files, s.skipCount.Files, s.dupCount.Files)
}

// Push pushes the contents of src to dst.
//    * Existing files that are the same are ignored
//    * Modified files are overwritten, even if dst is newer
//    * New files are created.
//    * Files in destination not in source are deleted.
func (s *Synchro) Push(src, dst string) (string, error) {
	s.t0 = time.Now()
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
		logger.Error(err)
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
		logger.Error(err)
		return err
	}
	if relPath == "." {
		s.addSkipStats(fi)
		return nil
	}
	// Gotten this far, add it to dest list
	s.lock.Lock()
	s.dstFileInfo[filepath.Join(s.dst, relPath)] = fileInfo{Fi: fi}
	s.lock.Unlock()
	return nil
}

func (s *Synchro) processSrc() error {
	// Push source to dest
	s.copyCh = make(chan string)
	s.delCh = make(chan string)
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
			logger.Error(err)
			return err
		}
	}
	close(s.copyCh)
	copyWait.Wait()
	close(s.delCh)
	delWait.Wait()
	return err
}

// addSrcFile just adds the info about the destination file
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
		logger.Error(err)
		return err
	}
	if relPath == "." {
		return nil
	}
	// determine if it should be copied
	if s.shouldCopy(p, fi) {
		tmpP := filepath.Join(s.dst, relPath)
		inf, ok := s.dstFileInfo[tmpP]
		if ok {
			inf.Processed = true
			s.dstFileInfo[tmpP] = inf
			s.addModStats(fi)
		} else {
			s.addNewStats(fi)
		}
		s.copyCh <- relPath
	}
	return nil
}

func (s *Synchro) copy() (*sync.WaitGroup, error) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() error {
		defer wg.Done()
		// process channel as we get items
		for relPath := range s.copyCh {
			// make any directories that are missing from the path
			err := s.mkDirTree(filepath.Dir(relPath))
			if err != nil {
				return err
			}
			r, err := os.Open(filepath.Join(s.src, relPath))
			if err != nil {
				logger.Debug(err)
				return err
			}
			defer r.Close()
			dst := filepath.Join(s.dst, relPath)
			var w *os.File
			w, err = os.Create(dst)
			if err != nil {
				logger.Debug(err)
				return err
			}
			defer w.Close()
			_, err = io.Copy(w, r)
			if err != nil {
				logger.Error(err)
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
				logger.Error(err)
				return err
			}
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

func getFileParts(s string) (dir, file, ext string, err error) {
	// see if there is path involved, if there is, get the last part of it
	dir, filename := filepath.Split(s)
	parts := strings.Split(filename, ".")
	l := len(parts)
	switch l {
	case 2:
		file := parts[0]
		ext := parts[1]
		return dir, file, ext, nil
	case 1:
		file := parts[0]
		return dir, file, ext, nil
	case 0:
		err := fmt.Errorf("no destination filename found in %s", s)
		return dir, file, ext, err
	default:
		// join all but the last parts together with a "."
		file := strings.Join(parts[0:l-1], ".")
		ext := parts[l-1]
		return dir, file, ext, nil
	}
	err = fmt.Errorf("unable to determine destination filename and extension")
	return dir, file, ext, err
}

// mkDirTree takes a directory path and makes sure it exists. If it doesn't
// exist it will create it, this includes any parent directories that don't
// already exist. This is needed because we process requests as we get them.
// This means we can encounter a child, or later descendent, before its
// ancestor.
//
// All evaluation is done relative to the src and dst dirs, those are
// considered the root and are expected to already exist.
func (s *Synchro) mkDirTree(p string) error {
	if p == "" {
		return nil
	}
	_, isD, err := s.isDir(filepath.Join(s.dst, p))
	if err != nil {
		logger.Error(err)
		return err
	}
	if isD { // if the parent already exists
		return nil
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
		st, err := os.Stat(srcP)
		if err != nil {
			logger.Debug(err)
			return err
		}
		err = os.Mkdir(dstP, st.Mode())
		if err != nil {
			logger.Debug(err)
			return err
		}
		// TODO mode, owner, group setting
	}
	return nil
}

//func (s *Synchro)
func (s *Synchro) shouldCopy(p string, fi os.FileInfo) bool {
	// copy if its not in destination

	// copy if its not the same as dest

	//
	return true
}

// deleteOrphans delete any files in the destination that were not in the
// source. This only happens if a file wasn't processed
func (s *Synchro) deleteOrphans() error {
	for p, fi := range s.dstFileInfo {
		if fi.Processed {
			continue // processed files aren't orphaned
		}
		s.addDelStats(fi.Fi)
		s.delCh <- p
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

func (s *Synchro) addModStats(fi os.FileInfo) {
	s.lock.Lock()
	s.modCount.Files++
	s.modCount.Bytes += fi.Size()
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

// checks to see if it is a directory, returns the info, truthiness, and error
func (s *Synchro) isDir(p string) (fi os.FileInfo, dir bool, err error) {
	fi, err = os.Stat(p)
	if err != nil {
		if err.Error() == fmt.Sprintf("stat %s: no such file or directory", p) {
			return fi, false, nil
		}
		return fi, false, err
	}
	if fi.IsDir() {
		return fi, true, nil
	}
	return fi, false, nil
}
