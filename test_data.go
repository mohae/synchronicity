// This file generates the test directories and files for various testing.
// This may be refactored to its own package, at some point as it'll
// probably appear in more than one place.
package synchronicity

import (
	"io/ioutil"
	"os"
	_ "path/filepath"
	"time"
)

// Files
var FName, FData []string
var isDst []bool

func init() {
	FName = make([]string, 8, 8)
	FName[0] = "src/rootfile.txt"
	FName[1] = "src/newrootfile.txt"
	FName[2] = "src/a/afile.txt"
	FName[3] = "src/a/aNewFile.txt"
	FName[4] = "src/b/bfile.txt"
	FName[5] = "src/b/bNewFile.txt"
	FName[6] = "dst/rootfile.txt"
	FName[7] = "dst/a/afile.txt"

	FData = make([]string, 8, 8)
	FData[0] = `this is a root file.`
	FData[1] = `newrootfile to see what happens when we add a file to the root.`
	FData[2] = `this is a file in a directory`
	FData[3] = `this is a new file in a directory`
	FData[4] = `this is b file in directory b`
	FData[5] = `this is b new file in directory b`
	FData[6] = FData[0]
	FData[7] = `this is an updated a file in a directory`

	isDst = make([]bool, 8, 8)
	isDst[6] = true
	isDst[7] = true
}

func WriteTestFiles() (dir string, err error) {
	// writes out the test files to a temp directory
	dir, err = ioutil.TempDir("", "synchrotest")
	if err != nil {
		return dir, err
	}
	err = os.Chdir(dir)
	if err != nil {
		return dir, err
	}
	err = os.MkdirAll("src/a", 0755)
	if err != nil {
		return dir, err
	}
	err = os.MkdirAll("src/b", 0755)
	if err != nil {
		return dir, err
	}
	err = os.MkdirAll("dst/a", 0755)
	if err != nil {
		return dir, err
	}
	for i, contents := range FData {
		err = ioutil.WriteFile(FName[i], []byte(contents), 0644)
		if err != nil {
			return dir, err
		}
		if isDst[i] {
			tNew := time.Now().Add(-30 * time.Minute)
			err = os.Chtimes(FName[i], tNew, tNew)
			if err != nil {
				return dir, err
			}
			err = os.Chmod(FName[i], 0777)
			if err != nil {
				return dir, err
			}
		}
	}
	return dir, err
}
