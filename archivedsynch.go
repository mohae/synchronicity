package synchronicity

import
//	"archive/tar"
//	"bytes"

//	"io"

//	"sort"
//	"strconv"
//	"strings"
//	"sync"
"time"

//tomb "gopkg.in/tomb.v2"

// New returns an initialized ArchivedSynch. Any overrides need to be done prior
// to a Synch operation. Archived synchs result in an archive of the files, in the
// destination, that will be updated, modified, or deleted prior to making any
// modifications to the destination directory.
//
// If the archived synch process encounters an error, the process will be aborted.
// This may result in the destination directory being in an unknown state, but it
// should never result in the original destination files being lost or be in an
// unknown state.
//
// TODO:
// Add destination state info to archive so that a rollback process can properly
// restore the destination directory to its pre-synch state using the created
// archive. The complete inventory is needed so that new files can be removed
// during the rollback.
func NewArchivedSynch() *ArchivedSynch {
	ASynch := &ArchivedSynch{
		Synch:           NewSynch(),
		ArchiveFilename: "archive-" + time.Now().Format("2006-01-02:15:04:05-MST") + ".tgz",
		archivedCount:   newCounter("archived"),
	}
	return ASynch
}

// Synchro provides information about a sync operation. This trades memory for
// CPU.
type ArchivedSynch struct {
	Synch           *Synch
	processingSrc   bool
	ArchiveDst      bool
	ArchiveFilename string
	Pipeline
	archivedCount counter
}

/*
// Push pushes the contents of src to dst.
//    * Existing files that are the same are ignored
//    * Modified files are overwritten, even if dst is newer
//    * New files are created.
//    * Files in destination will be archived and deleted.
func (s *ArchivedSynch) Push(src, dst string) (string, error) {
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

	s.Synch.src = src
	s.Synch.fullSrc, err = filepath.Abs(s.Synch.src)
	if err != nil {
		log.Printf("synch.Push absolute path error for %q: %s", s.Synch.src, err)
		return "", err
	}

	s.Synch.dst = dst
	s.Synch.fullDst, err = filepath.Abs(s.Synch.dst)
	if err != nil {
		log.Printf("synch.Push absolute path error for %q: %s", s.Synch.dst, err)
		return "", err
	}

	// We always pre-index dst; at least until lists of files are accepted, e.g.
	// from an archive
	s.filepathWalk()

	//	err := s.archivedProcess() // Takes care of all source processing too.
	//	if err != nil {
	//		log.Printf("Archived Push: synch error: %s", err)
	//		return "", err
	//	}

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

// ArchivedPush pushes the contents of src to dst.
//    * Existing files that are the same are ignored
//    * Modified files are overwritten, even if dst is newer
//    * New files are created.
//    * Files in destination will be archived and deleted.
func ArchivedPush(src, dst string) (string, error) {
	return archSynch.Push(src, dst)
}

// archivedProcess() controls the processing of an archived synch; which requires
// sequential processing due to the archiving.
func (s *ArchivedSynch) archivedProcess() error {
	s.processingSrc = true
	err := s.filepathWalk()
	if err != nil {
		log.Printf("synch.archivedProcess error: %s", err)
		return err
	}
	/*
		err = s.archivedEvaluate()
		if err != nil {
			log.Printf("synch.archivedProcess error: %s", err)
			return err
		}

		err = s.processArchive()
		if err != nil {
			log.Printf("synch.archivedProcess error: %s", err)
			return err
		}

		err = s.archivedPostProcess()
		if err != nil {
			log.Printf("synch.archivedProcess error: %s", err)
			return err
		}
*/
/*
	return nil
}

// filepathWalk is a basic walker that adds the walked FileData to the appropriate
// var. It will walk the src directory if s.processSrc == true, otherwise the dst.
func (s *ArchivedSynch) filepathWalk() error {
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
func (s *ArchivedSynch) addFileData(root, p string, fi os.FileInfo, err error) error {
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
	fd := NewFileData(path, filepath.Dir(relPath), fi, s.synch)
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

func (s *ArchivedSynch) addArchivedStats(fi os.FileInfo) {
	s.counterLock.Lock()
	s.archivedCount.Files++
	s.archivedCount.Bytes += fi.Size()
	s.counterLock.Unlock()
}
*/
