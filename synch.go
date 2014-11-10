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
)

// New returns an initialized Synchro. Any overrides need to be done prior
// to a Synchro operation.
func New() *Synch {
	s := &Synch{
		maxProcs:           maxProcs,
		dstFileData:        map[string]*FileData{},
		srcFileData:        map[string]*FileData{},
		chunkSize:          chunkSize,
		MaxChunks:          MaxChunks,
		equalityType:       defaultEqualityType,
		hashType:           defaultHashType,
		Delete:             true,
		PreserveProperties: true,
		ArchiveDst:         true,
		ReadAll:            ReadAll,
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
	return s
}

// Synchro provides information about a sync operation. This trades memory for
// CPU.
type Synch struct {
	lock         sync.RWMutex // Read/Write lock
	maxProcs     int          // maxProcs for this synchro.
	PreDigest    bool         // precompute digests for files
	chunkSize    int64
	MaxChunks    int          // TODO work this out or remove
	equalityType equalityType // the type of equality comparison between files
	// Sync operation modifiers
	hashType           hashType // Hashing algorithm used for digests
	ArchiveDst         bool     // Archive Destination files that will be modified or deleted
	Delete             bool     // Delete orphaned dst filed (doesn't exist in src)
	PreserveProperties bool     // Preserve file properties(mode, mtime)
	ReadAll            bool     // Read the entire file at once; false == chunked read
	// Filepaths to operate on
	src string // Path of source directory
	dst string // Path of destination directory
	// A map of all the fileInfos by path
	dstFileData   map[string]*FileData // map of destination file info
	srcFileData   map[string]*FileData // map of src file info
	processingSrc bool                 // if false, processing dst. Only used for walking
	// TODO wire up support for attrubute overriding
	Owner int
	Group int
	Mod   int64 // filemode

	workCh chan *FileData // Channel for processing work. This is used in various stages.
	tomb   tomb.Tomb      // Queue management
	wg     sync.WaitGroup
	Pipeline
	// Counters
	counterLock sync.Mutex // lock for updating counters
	newCount    counter    // counter for new files
	copyCount   counter    // counter for copied files; files whose contents have changed
	delCount    counter    // counter for deleted files
	updateCount counter    // counter for updated files; files whose properties have changed
	dupCount    counter    // counter for duplicate files
	skipCount   counter    // counter for skipped files.
	t0          time.Time  // Start time of operation.
	𝛥t          float64    // Change in time between start and end of operation
}

func (s *Synch) SetEqualityType(e equalityType) {
	s.equalityType = e
	if s.equalityType == ChunkedEquality {
		s.SetDigestChunkSize(0)
	}
}

// SetDigestChunkSize either sets the chunkSize, when a value > 0 is received,
// using the recieved int as a multipe of 1024 bytes. If the received value is
// 0, it will use the current chunksize * 4.
func (s *Synch) SetDigestChunkSize(i int) {
	// if its a non-zero value use it
	if i > 0 {
		s.chunkSize = int64(i * 1024)
		return
	}
	s.chunkSize = s.chunkSize * 4
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
	return nSynch.DstFileData()
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
	// Make sure dst directory exists and we can write to it
	fi, err := os.Stat(dst)
	if err != nil {
		if !os.IsExist(err) {
			log.Printf("synchronicity.Push error trying to check %q: %s", dst, err)
			return "", err
		}
		err := os.MkdirAll(dst, 0640)
		if err != nil {
			log.Printf("synchronicity.Push error tyring to make the destination directory %q: %s", dst, err)
			return "", err
		}
	}

	if !fi.IsDir() {
		err := fmt.Errorf("destination, %q, is not a directory", dst)
		log.Println("synchronicity.Push error %s", err)
		return "", err
	}

	_, err = os.Create("test.txt")
	if err != nil {
		log.Printf("synchronicity.Push error trying to create a file in %q: %s", dst, err)
		return "", err
	}
	os.Remove("test.txt")

	s.src = src
	s.dst = dst

	// We preindex everything so that pre-action work, if applicable can be done.
	s.filepathWalk()
	s.processingSrc = true
	s.filepathWalk()

	// Evaluate the files to see what actions should be performed
	// TODO: Add simulate flag
	err = s.evaluate()
	if err != nil {
		log.Printf("Synchronicity.Push error evaluating actions: %s", err)
	}
	/*
		err = s.process()
		if err !- nil {
			log.Printf("synchronicty.Push error processing actions: %s", err)
			return "", err
		}
		err = s.execPipeline() // Build and run the action pipeline
		if err != nil {
			log.Printf("sychronicity.Push error processing %s: %s", s.src, err)
			return "", err
		}
	*/
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

func (s *Synch) filepathWalk() error {
	var fullpath string
	visitor := func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.Mode()&os.ModeType != 0 {
			if err != nil {
				log.Printf("error walking %s: %s", p, err)
			}
			return nil // skip special files
		}
		return s.addFileData(fullpath, p, fi, err)
	}
	var err error
	if s.processingSrc {
		fullpath, err = filepath.Abs(s.src)
	} else {
		fullpath, err = filepath.Abs(s.dst)
	}
	if err != nil {
		var path string
		if s.processingSrc {
			path = s.src
		} else {
			path = s.dst
		}
		log.Printf("synchronicity.filepathWalk error walking %q: %s", path, err)
		return err
	}
	walk.Walk(fullpath, visitor)
	return nil
}

// addFile just adds the info about the destination file
func (s *Synch) addFileData(root, p string, fi os.FileInfo, err error) error {
	Logf("\t%s\n", p)
	// We don't add directories, those are handled by their files.
	if fi.IsDir() {
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
	// Gotten this far, read the file and add it to the dst list
	var path string
	if s.processingSrc {
		path = s.src
	} else {
		path = s.dst
	}
	fd := NewFileData(path, filepath.Dir(relPath), fi, s)
	// Send it to the read channel
	s.lock.Lock()
	if s.processingSrc {
		s.srcFileData[fd.String()] = fd
	} else {
		s.dstFileData[fd.String()] = fd
	}
	s.lock.Unlock()
	return nil
}

// evaluate the FileInfos to determine what action should be performed for each
// file inventoried.
func (s *Synch) evaluate() error {
	//	var dFd *FileData
	//	var ok bool
	//	var archiveBuf bytes.Buffer
	//	var gz *gzip.Writer
	// concurrently evaluate the actions
	//	t tomb.Tomb
	s.tomb.Go(s.eval)
	//
	// send the sources down the channel for evaluation
	for _, fd := range s.srcFileData {
		s.workCh <- fd
	}

	return nil
}

// eval performs the actual evaluations of the src and dst files to determine what
// should be done with them
func (s *Synch) eval() error {
	var fdChan <-chan *FileData
	for {
		select {
		case <-s.tomb.Dying():
			return nil
		case <-s.workCh:
			fdChan = s.workCh
			srcFd := <-fdChan
			if fd == nil {
				s.Stop()
				return nil
			}
			dFd, ok = s.dstFileData[p]
			if !ok {
				fd.actionType = newAction
				s.srcFileData[srcFd.FullPath()] = fd
				s.addNewStats(fd.Fi)
				continue
			}
			// copy if its not the same as dest
			equal, err := fd.isEqual(dFd)
			if err != nil {
				log.Printf("an error occurred while checking equality for %s: %s", srcFd.String(), err)
				return nilAction, err
			}
			if !equal {
				srcFd.taskType = copyAction
				s.addCopyStats(dFd, fi)
				s.addCopyStats(fd.Fi)
				goto SetFd
			}
			// update if the properties are different
			if srcFd.Fi.Mode() != fd.Fi.Mode() || srcFd.Fi.ModTime() != fd.Fi.ModTime() {
				fd.taskType = updateAction
				s.addUpdateStats(dFd.fi)
				s.addUpdateStats(fd.Fi)
			}

			// Set the processed flag on existing dest file, for delete processing,
			// if applicable.
		SetFd:
			dFd.Processed = true
			s.dstFileData[p] = dFd
			s.srcFileData[srcFd.FullPath] = srcFd
		}
		// Otherwise everything is the same, its a duplicate: do nothing
		s.addDupStats(fd.Fi)
		s.addDupStats(dFd.Fi)
	}
	return nil
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
	/*
		_, err = s.setAction(relPath, fi)
		if err != nil {
			log.Printf("an error occurred while setting the task for %s: %s", filepath.Join(relPath, fi.Name()), err)
			return err
		}
		return nil
	*/
	return nil
}

// copyFile copies the file.
// TODO should a copy of the file be made in a tmp directory while its hash
// is being computed, or in memory. Source would not need to be read and
// processed twice this way. If a copy operation is to occur, the tmp file
// gets renamed to the destination, otherwise the tmp directory is cleaned up
// at the end of the run.
func (s *Synch) copyFile(fd *FileData) error {
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

// deleteOrphans delete any files in the destination that were not in the
// source. This only happens if a file wasn't processed
func (s *Synch) deleteOrphans() error {
	for _, fd := range s.dstFileData {
		if fd.Processed {
			continue // processed files aren't orphaned
		}
		s.workCh <- fd
		Logf("Delete %q\n", fd.RelPath())
	}
	return nil
}

func (s *Synch) setDelta() {
	s.𝛥t = float64(time.Since(s.t0)) / 1e9
}

// deleteFile deletes any file for which it receives.
func (s *Synch) deleteFile(fd *FileData) error {
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
func (s *Synch) updateFile(fd *FileData) error {
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

/*
// See if the file should be filtered
func (s *Synch) filterFileInfo(fi os.FileInfo) (bool, error) {
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

func (s *Synch) filterPath(root, p string) (bool, error) {
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

func (s *Synch) includeFile(root, p string) (bool, error) {
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

func (s *Synch) excludeFile(root, p string) (bool, error) {
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
*/

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

/*
type Archive struct{}

func (s *Synch) Process(in chan *FileData) chan *FileData {
	out := make(chan *FileData)
	go func() {
		for fd := range in {
			out <- *FileData
		}
		close(out) // close when drained
	}()
	return out
}

/*
type Action struct{}

func (s *Synch) Process(in chan *FileData) chan *FileData {
	out := make(chan *FileData)
	go func() {
		for fd := range in {
			out <- *FileData
		}
		close(out) // close when drained
	}()
	return out
}
*/

/*
// execPipeline creates a pipeline with the appropriate elements and manages it
func (s *Synch) execPipeline() error {
	var pipe Pipeline
	stages := &[]Pipe{} // pipelines have stages. These stages are processed in order.
	if s.ArchiveDst {
		stages = append(stages, Archive{})
	}
	stages = append(stages, Action{})
	pipe = NewPipeline(stages...)

	return nil
}

func (s *Synch) Stop() error {
	s.tomb.Kill(nil)
	return s.tomb.Wait()
}
*/

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

// increments the file count.
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
