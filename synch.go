package synchronicity

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelTJones/walk"
	tomb "gopkg.in/tomb.v2"
)

// New returns an initialized Synchro. Any overrides need to be done prior
// to a Synchro operation.
func NewSynch() *Synch {
	s := &Synch{
		maxProcs:           maxProcs,
		dstFileData:        map[string]*FileData{},
		srcFileData:        map[string]*FileData{},
		chunkSize:          chunkSize,
		equalityType:       defaultEqualityType,
		hashType:           defaultHashType,
		Delete:             true,
		PreserveProperties: true,
		//		ArchiveDst:         true,
		//		ArchiveFilename:    "archive-" + time.Now().Format("2006-01-02:15:04:05-MST") + ".tgz",
		ReadAll:     ReadAll,
		srcCount:    newCounter("in source were indexed"),
		dstCount:    newCounter("in destination were indexed"),
		newCount:    newCounter("created"),
		copyCount:   newCounter("copied"),
		delCount:    newCounter("deleted"),
		updateCount: newCounter("updated"),
		dupCount:    newCounter("duplicates and not updated"),
		skipCount:   newCounter("skipped"),
	}
	if s.equalityType == ChunkedEquality {
		s.SetHashChunkSize(0)
	}
	return s
}

// Synchro provides information about a sync operation. This trades memory for
// CPU.
type Synch struct {
	lock     sync.RWMutex // Read/Write lock
	maxProcs int          // maxProcs for this synchro.
	//	PreDigest    bool         // precompute digests for files
	chunkSize    int64        // chink size for regular compare reads.
	equalityType equalityType // the type of equality comparison between files
	// Sync operation modifiers
	hashType hashType // Hashing algorithm used for digests
	//	ArchiveDst         bool     // Archive Destination files that will be modified or deleted
	//	ArchiveFilename    string   // ArchiveFilename defaults to: archive-2006-01-02:15:04:05-MST.tgz
	Delete             bool // Delete orphaned dst filed (doesn't exist in src)
	PreserveProperties bool // Preserve file properties(mode, mtime)
	ReadAll            bool // Read the entire file at once; false == chunked read
	// Filepaths to operate on
	src     string // Path of source directory
	fullSrc string // Full path of source directory
	dst     string // Path of destination directory
	fullDst string // Full path of destination directory
	// A map of all the fileInfos by path
	dstFileData map[string]*FileData // map of destination file info
	srcFileData map[string]*FileData // map of src file info
	//	processingSrc bool                 // if false, processing dst. Only used for walking
	Mod         int64          // filemode
	workCh      chan *FileData // Channel for processing work. This is used in various stages.
	tomb        tomb.Tomb      // Queue management
	counterLock sync.Mutex     // lock for updating counters
	dstCount    counter        // counter for destination
	srcCount    counter        // counter for source
	newCount    counter        // counter for new files
	copyCount   counter        // counter for copied files; files whose contents have changed
	delCount    counter        // counter for deleted files
	updateCount counter        // counter for updated files; files whose properties have changed
	dupCount    counter        // counter for duplicate files
	skipCount   counter        // counter for skipped files.
	t0          time.Time      // Start time of operation.
	𝛥t          float64        // Change in time between start and end of operation
}

func (s *Synch) SetEqualityType(e equalityType) {
	s.equalityType = e
	if s.equalityType == ChunkedEquality {
		s.SetHashChunkSize(0)
	}
}

// SetDigestChunkSize either sets the chunkSize, when a value > 0 is received,
// using the recieved int as a multipe of 1024 bytes. If the received value is
// 0, it will use the current chunksize * 4.
func (s *Synch) SetHashChunkSize(i int) {
	// if its a non-zero value use it
	if i > 0 {
		s.chunkSize = int64(i * 1024)
		return
	}
	s.chunkSize = s.chunkSize * 8
}

func SetEqualityType(e equalityType) {
	nSynch.SetEqualityType(e)
}

// DstFileData returns the map of FileData accumulated during the walk of the
// destination.
func (s *Synch) DstFileData() map[string]*FileData {
	return s.dstFileData
}

// DstFileData returns the map of FileData accumulated during the walk of the
// destination.
func DstFileData() map[string]*FileData {
	return nSynch.dstFileData
}

// Delta returns the 𝛥 between the start and end of an operation/
func (s *Synch) Delta() float64 {
	return s.𝛥t
}

// Delta returns the 𝛥 between the start and end of an operation/
func Delta() float64 {
	return nSynch.Delta()
}

// SetDelete is used to set the mainSynchro's delete flag. When working
// directly with a Synchro object, just set it, Synchro.Delete, instead of
// calling this function.
func SetDelete(b bool) {
	nSynch.Delete = b
}

// Message returns stats about the last Synch.
func (s *Synch) Message() string {
	var msg bytes.Buffer
	s.setDelta()
	msg.WriteString(s.src)
	msg.WriteString(" was pushed to ")
	msg.WriteString(s.dst)
	msg.WriteString(" in ")
	msg.WriteString(strconv.FormatFloat(s.𝛥t, 'f', 4, 64))
	msg.WriteString(" seconds\n")
	msg.WriteString(s.dstCount.String())
	msg.WriteString("\n")
	msg.WriteString(s.srcCount.String())
	msg.WriteString("\n\n")
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
	return nSynch.Message()
}

// Push pushes the contents of src to dst.
//    * Existing files that are the same are ignored
//    * Modified files are overwritten, even if dst is newer
//    * New files are created.
//    * Files in destination not in source may be deleted.
func (s *Synch) Push(src, dst string) (string, error) {
	s.t0 = time.Now()
	Logf("Start push of %q to %q\n", src, dst)
	// check to see if something was passed
	if src == "" {
		err := fmt.Errorf("synch.Push error: source not set")
		log.Println(err)
		return "", err
	}

	if dst == "" {
		err := fmt.Errorf("synch.Push error: destination not set")
		log.Println(err)
		return "", err
	}

	// Check for existence of src
	_, err := os.Stat(src)
	if err != nil {
		log.Printf("synch.Push error: %s\n", err)
		return "", err
	}

	err = s.ensureDirPath(dst) // ensure this is something we can work with
	if err != nil {
		log.Printf("synch.Push error: %s\n", err)
		return "", err
	}

	s.src = src
	s.fullSrc, err = filepath.Abs(s.src)
	if err != nil {
		log.Printf("synch.Push absolute path error for %q: %s", s.src, err)
		return "", err
	}

	s.dst = dst
	s.fullDst, err = filepath.Abs(s.dst)
	if err != nil {
		log.Printf("synch.Push absolute path error for %q: %s", s.dst, err)
		return "", err
	}

	// Dst walk needs to be completed before we can compare walk source
	err = s.filepathWalkDst()
	if err != nil {
		Log(err)
		log.Printf("synch.Push walk error %q: %s", s.dst, err)
		return "", err
	}

	Logf("%q indexed %d files", s.dst, len(s.dstFileData))
	err = s.processSrc()
	if err != nil {
		log.Printf("synch.Push process error: %s", err)
		return "", err
	}

	dstInfo := s.dstListByRelPath()
	df, err := os.Create("dstList.txt")
	if err != nil {
		return "", err
	}
	defer df.Close()
	df.Write(dstInfo)
	srcInfo := s.srcListByRelPath()
	sf, err := os.Create("srcList.txt")
	if err != nil {
		return "", err
	}
	defer sf.Close()
	sf.Write(srcInfo)
	return s.Message(), nil
}

// Push pushes the contents of src to dst.
//    * Existing files that are the same are ignored
//    * Modified files are overwritten, even if dst is newer
//    * New files are created.
//    * Files in destination not in source may be deleted.
func Push(src, dst string) (string, error) {
	return nSynch.Push(src, dst)
}

// Pull is just a Push from dst to src
func (s *Synch) Pull(src, dst string) (string, error) {
	return s.Push(dst, src)
}

// Pull is just a Push from dst to src
func Pull(src, dst string) (string, error) {
	return nSynch.Pull(src, dst)
}

// process() controls the processing of a non-archived synch.
func (s *Synch) process() error {
	return nil
}

// filepathWalk is a basic walker that adds the walked FileData to the appropriate
// var. It will walk the src directory if s.processSrc == true, otherwise the dst.
func (s *Synch) filepathWalkDst() error {
	var fullpath string
	visitor := func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.Mode()&os.ModeType != 0 {
			if err != nil {
				log.Printf("error walking %s: %s", p, err)
			}
			return nil // skip special files
		}
		return s.addDstFileData(fullpath, p, fi, err)
	}
	var err error
	fullpath, err = filepath.Abs(s.dst)
	if err != nil {
		log.Printf("synch.filepathWalkDst error generating fullpath for %q: %s", s.dst, err)
		return err
	}
	err = walk.Walk(fullpath, visitor)
	if err != nil {
		log.Printf("synch.filepathWalkDst error walking %q: %s", s.fullDst, err)
		return err
	}
	return nil
}

// addDstFileData just adds the info about the destination file
func (s *Synch) addDstFileData(root, p string, fi os.FileInfo, err error) error {
	// We don't add directories, those are handled by their files.
	if fi.IsDir() {
		return nil
	}
	var relPath string
	relPath, err = filepath.Rel(root, p)
	if err != nil {
		log.Printf("synch.addDstFileData error getting relative path for %q: %s", p, err)
		return err
	}
	if relPath == "." {
		return nil
	}
	// Gotten this far, read the file and add it to the dst list
	fd := NewFileData(s.dst, filepath.Dir(relPath), fi, s)
	// Send it to the read channel
	s.lock.Lock()
	s.dstFileData[fd.RelPath()] = fd
	s.lock.Unlock()
	s.addDstStats(fi)
	return nil
}

func (s *Synch) exec() {
	for i := 0; i < s.maxProcs; i++ {
		s.tomb.Go(s.doWork)
	}
}

func (s *Synch) Stop() error {
	s.tomb.Kill(nil)
	return s.tomb.Wait()
}

func (s *Synch) doWork() error {
	for {
		select {
		case fd := <-s.workCh:
			if fd == nil {
				return nil
			}
			switch fd.actionType {
			case newAction:
				err := s.copyFile(fd)
				if err != nil {
					log.Printf("synch.doWork NEW error: %s", err)
					s.tomb.Kill(err)
					return err
				}
				s.addNewStats(fd.Fi)
			case copyAction:
				err := s.copyFile(fd)
				if err != nil {
					log.Printf("synch.doWork COPY error: %s", err)
					s.tomb.Kill(err)
					return err
				}
				s.addCopyStats(fd.Fi)
			case updateAction:
				err := s.updateFile(fd)
				if err != nil {
					log.Printf("synch.doWork UPDATE error: %s", err)
					s.tomb.Kill(err)
					return err
				}
				s.addUpdateStats(fd.Fi)
			case deleteAction:
				err := s.deleteFile(fd)
				if err != nil {
					log.Printf("synch.doWork DELETE error: %s", err)
					s.tomb.Kill(err)
					return err
				}
				s.addDelStats(fd.Fi)
			default:
				err := fmt.Errorf("unsupported task type for %s", fd.RelPath())
				log.Printf("synch.doWork unknown action error: %s", err)
				s.tomb.Kill(err)
				return err
			}
		case <-s.tomb.Dying():
			return nil
		}
	}
	return nil
}

// procesSrc indexes the source directory, figures out what's new and what's
// changed, and triggering the appropriate task. If an error is encountered,
// it is returned. The tomb is to manage the processes
func (s *Synch) processSrc() error {
	var err error
	var fullpath string
	s.workCh = make(chan *FileData, 1024)
	s.exec()
	visitor := func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.Mode()&os.ModeType != 0 {
			if err != nil {
				log.Printf("synch.processSrc error walking %q: %s\n", p, err)
			}
			return nil // skip special files
		}
		return s.addSrcFile(fullpath, p, fi, err)
	}
	fullpath, err = filepath.Abs(s.src)
	if err != nil {
		log.Printf("synch.processSrc error getting absolute path for %q: %s\n", s.src, err)
		return err
	}
	Logf("Walk source %q...\n", fullpath)
	err = walk.Walk(fullpath, visitor)
	if err != nil {
		log.Printf("synch.processSrc  walk error: %s\n", err)
		return err
	}

	if s.Delete {
		err := s.deleteOrphans()
		if err != nil {
			return err
		}
	}

	close(s.workCh)
	err = s.tomb.Wait()
	if err != nil {
		Logf("Error tomb wait: %s", err)
	}

	return err
}

// addSrcFile adds the info about the source file, then calls setTast to
// determine what task should be done, if any.
func (s *Synch) addSrcFile(root, p string, fi os.FileInfo, err error) error {
	// We don't add directories, they are handled by the mkdir process
	if fi.IsDir() {
		return nil
	}
	var relPath string
	relPath, err = filepath.Rel(root, p)
	if err != nil {
		log.Printf("synch.addSrcFile error generating the relative path for %q: %s\n", p, err)
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
	// set the fileData info
	srcFd := NewFileData(s.src, relPath, fi, s)
	s.lock.Lock()
	s.srcFileData[srcFd.RelPath()] = srcFd
	s.lock.Unlock()
	s.addSrcStats(fi)
	// determine if it should be copied
	_, err = s.setAction(relPath, srcFd)
	if err != nil {
		log.Printf("synch.addSrcFile error setting the task for %q: %s", filepath.Join(relPath, fi.Name()), err)
		return err
	}
	return nil
}

// setAction examines the src/dst to determine what should be done.
// Possible task outcomes are:
//    Do nothing (file contents and properties are the same)
//    Update (file contents are the same, but properties are diff)
//    Copy (file contents are different)
//    New (new file)
func (s *Synch) setAction(relPath string, srcFd *FileData) (actionType, error) {
	fd, ok := s.dstFileData[srcFd.RelPath()]
	if !ok {
		srcFd.actionType = newAction
		s.workCh <- srcFd
		return newAction, nil
	}
	// See the processed flag on existing dest file, for delete processing,
	// if applicable.
	fd.Processed = true
	s.dstFileData[srcFd.RelPath()] = fd
	// copy if its not the same as dest
	Equal, err := srcFd.isEqual(fd)
	if err != nil {
		log.Printf("an error occurred while checking equality for %s: %s", srcFd.String(), err)
		return nilAction, err
	}
	if !Equal {
		srcFd.actionType = copyAction
		s.workCh <- srcFd
		return copyAction, nil
	}
	// update if the properties are different
	if srcFd.Fi.Mode() != fd.Fi.Mode() || srcFd.Fi.ModTime() != fd.Fi.ModTime() {
		srcFd.actionType = updateAction
		s.workCh <- srcFd
		return updateAction, nil
	}
	// Otherwise everything is the same, its a duplicate: do nothing
	s.addDupStats(fd.Fi)
	return nilAction, nil
}

// copyFile copies the file.
func (s *Synch) copyFile(fd *FileData) error {
	// make any directories that are missing from the path
	Logf("COPY:\t%s", filepath.Dir(filepath.Join(s.dst, fd.RelPath())))
	err := s.mkDirTree(filepath.Dir(fd.RelPath()))
	if err != nil {
		log.Printf("synch.copyFile make directory tree error: %s\n", err)
		return err
	}
	r, err := os.Open(fd.FullPath())
	if err != nil {
		log.Printf("synch.copyFile error opening %s: %s\n", fd.FullPath(), err)
		return err
	}
	dst := filepath.Join(s.dst, fd.RelPath())
	Logf("Synch.copyFile %q to %q\n", fd.RelPath(), dst)
	var w *os.File
	w, err = os.Create(dst)
	if err != nil {
		log.Printf("synch.copyFile error creating %s: %s\n", dst, err)
		r.Close()
		return err
	}
	_, err = io.Copy(w, r)
	r.Close()
	w.Close()
	if err != nil {
		log.Printf("synch.copyFile error copying %s to %s: %s\n", fd.FullPath(), dst, err)
		Logf("error copying %s to %s: %s\n", fd.FullPath(), dst, err)
		return err
	}
	fInfo, err := os.Stat(dst)
	if err != nil {
		log.Printf("synch.copyFile error getting FileInfo for %s\n", fd.FullPath(), dst, err)
		return err
	}
	return s.updateFi(dst, fInfo)
}

// deleteOrphans delete any files in the destination that were not in the
// source. This only happens if a file wasn't processed
func (s *Synch) deleteOrphans() error {
	for _, fd := range s.dstFileData {
		if fd.Processed {
			continue // processed files aren't orphaned
		}
		fd.actionType = deleteAction
		s.workCh <- fd
	}
	return nil
}

func (s *Synch) setDelta() {
	s.𝛥t = float64(time.Since(s.t0)) / 1e9
}

// deleteFile deletes any file for which it receives.
func (s *Synch) deleteFile(fd *FileData) error {
	err := os.Remove(fd.FullPath())
	if err != nil {
		log.Printf("synch.deleteFile error: %s\n", err)
		return err
	}
	Logf("DELETE %q\n", fd.RelPath())
	return nil
}

// update updates the fi of a file: currently mode, mdate, and atime
// this is done on files whose contents haven't changes (Digests are equal) but
// their properties have.
// TODO add supportE for uid, gid
func (s *Synch) updateFile(fd *FileData) error {
	p := filepath.Join(s.dst, fd.RelPath())
	Logf("UPDATE: %s\n", p)
	return s.updateFi(p, fd.Fi)
}

func (s *Synch) updateFi(p string, fi os.FileInfo) error {
	err := os.Chmod(p, fi.Mode())
	if err != nil {
		log.Printf("synch.updateFi mod error for %q: %s", p, err)
		Logf("synch.updateFile mod error %q: %s", p, err)
		return err
	}
	err = os.Chtimes(p, fi.ModTime(), fi.ModTime())
	if err != nil {
		log.Printf("synch.updateFi mtime error for %q: %s", p, err)
		Logf("synch.updateFile mtime error %q: %s", p, err)
		return err
	}
	return nil
}

func (s *Synch) dstListByRelPath() []byte {
	return s.listByRelPaths(s.dstFileData)
}

func (s *Synch) srcListByRelPath() []byte {
	return s.listByRelPaths(s.srcFileData)
}

// list returns a sorted list of filepaths and names as a []byte
func (s *Synch) listByRelPaths(filesD map[string]*FileData) []byte {
	var fDatas []*FileData
	for _, fd := range filesD {
		fDatas = append(fDatas, fd)
	}
	// get a sorted list of relative paths and filenames
	sort.Sort(ByPath{fDatas})

	// generate the []bytes
	inv := make([]byte, 0)
	for _, fd := range fDatas {
		inv = append(inv, []byte(fd.RelPath()+string('\n'))...)
	}
	return inv
}

// mkDirTree takes a directory path and makes sure it exists. If it doesn't
// exist it will create it, this includes any parent directories that don't
// already exist. This is needed because we process requests as we get them.
// This means we can encounter a child, or later descendent, before its
// ancestor. We also want to preserve as many properties as we can.
//
func (s *Synch) mkDirTree(p string) error {
	if p == "" {
		return nil
	}

	fi, err := os.Stat(filepath.Join(s.dst, p))
	if err == nil {
		if fi.IsDir() { // if the parent already exists
			return nil
		}
		err := fmt.Errorf("%s not a directory", filepath.Join(s.dst, p))
		log.Printf("synch.mkDirTree error: %s\n", err)
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
		// see if dst exists
		_, err = os.Stat(dstP)
		if err == nil { // exists, move on
			continue
		}
		fi, err := os.Stat(srcP)
		if err != nil {
			log.Printf("synch.mkDirTree error: %s\n", srcP, err)
			return err
		}
		err = os.Mkdir(dstP, fi.Mode())
		if err != nil {
			log.Printf("synch.mkDirTree error: %s\n", dstP, err)
			return err
		}
		err = os.Chtimes(dstP, fi.ModTime(), fi.ModTime())
		if err != nil {
			log.Printf("error mkDirTree error: %s\n", dstP, err)
			return err
		}
	}
	return nil
}

// ensurePath makes sure the passed path exists, is a directory, and is writeable.
// If it has all those properties a nil is returned, otherwise it returns an error.
func (s *Synch) ensureDirPath(p string) error {
	// Make sure dst directory exists and we can write to it
	fi, err := os.Stat(p)
	if err != nil {
		if !os.IsExist(err) {
			log.Printf("synch.ensureDirPath error checking %q: %s", p, err)
			return err
		}
		err := os.MkdirAll(p, 0640)
		if err != nil {
			log.Printf("synch.ensureDirPath error: %s", err)
			return err
		}
	}

	if !fi.IsDir() {
		err := fmt.Errorf("destination, %q, is not a directory", p)
		log.Printf("synch.ensureDirPath error %s", err)
		return err
	}

	_, err = os.Create("test.txt")
	if err != nil {
		log.Printf("synch.ensureDirPath error trying to create a file in %q: %s", p, err)
		return err
	}
	os.Remove("test.txt")
	return nil
}

// increments the file count.
func (s *Synch) addDstStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.dstCount.Files++
	s.dstCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}

func (s *Synch) addSrcStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.srcCount.Files++
	s.srcCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}

func (s *Synch) addNewStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.newCount.Files++
	s.newCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}

func (s *Synch) addDelStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.delCount.Files++
	s.delCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}

func (s *Synch) addCopyStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.copyCount.Files++
	s.copyCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}

func (s *Synch) addUpdateStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.updateCount.Files++
	s.updateCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}

func (s *Synch) addDupStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.dupCount.Files++
	s.dupCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}

func (s *Synch) addSkipStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.skipCount.Files++
	s.skipCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}
