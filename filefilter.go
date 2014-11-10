package synchronicity

import (
	"time"
)

// StringFilter defines the string filters used on files.
type StringFilter struct {
	typ      int
	Prefix   string
	Ext      []string
	ExtCount int
	Anchored string
}

// TimeFilter defines the time filters used on files.
type TimeFilter struct {
	Newer      string
	NewerMTime time.Time
	NewerFile  string
}

/*
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

*/

/*
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
*/
