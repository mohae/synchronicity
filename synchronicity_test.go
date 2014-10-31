package synchronicity

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	_"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSynchroMessage(t *testing.T) {
	s := New()
	msg := s.Message()
	_ = s.Delta()
	expected := fmt.Sprintln("'' was pushed to '' in 9223372036.855 seconds")
	assert.Equal(t, msg, expected)
}

func TestFilePathWalkDst(t *testing.T) {
	expectedFile := make([]string, 2, 2)
	expectedFile[0] = "rootfile.txt"
	expectedFile[1] = "afile.txt"
	expectedDir := make([]string, 2, 2)
	expectedDir[0] = ""
	expectedDir[1] = "a"
	expectedByte := make([]int64, 2, 2)
	expectedByte[0] = 20
	expectedByte[1] = 40
	expectedHash := make([]string, 2, 2)
	expectedHash[0] = ""
	expectedHash[1] = ""
	dir, _ := WriteTestFiles()
	s := New()
	s.dst = filepath.Join(dir, "dst")
	err := s.filepathWalkDst()
	assert.Nil(t, err)
	var i int
	for p, fd := range s.dstFileData {
		assert.Equal(t, filepath.Join(expectedDir[i], expectedFile[i]), p)
		assert.Equal(t, expectedDir[i], fd.Dir)
		assert.Equal(t, expectedFile[i], fd.Fi.Name())
		assert.Equal(t, expectedByte[i], fd.Fi.Size())
		i++
	}
}

func TestFileAddDstFile(t *testing.T) {
	dir, _ := WriteTestFiles()
	s := New()
	s.dst = filepath.Join(dir, "dst")
	var err error
	p := "afile.txt"
	pdir := "a"
	fi, err := os.Stat(filepath.Join(s.dst, pdir, p))
	assert.Nil(t, err)
	err = s.addDstFile(s.dst, filepath.Join(s.dst, pdir, p), fi, err)
	assert.Nil(t, err)
	fd, ok := s.dstFileData[filepath.Join(pdir, p)]
	assert.Equal(t, true, ok)
	assert.Equal(t, "a/afile.txt", fd.String())
}

func TestGetFileParts(t *testing.T) {
	tests := []struct {
		path         string
		expectedDir  string
		expectedFile string
		expectedExt  string
	}{
		{"/test/foo/bar/afile.txt", "/test/foo/bar/", "afile", "txt"},
		{"test.txt", "", "test", "txt"},
		{"test/foo/bar/afile.txt", "test/foo/bar/", "afile", "txt"},
		{"test/foo/bar/afile.txt.z", "test/foo/bar/", "afile.txt", "z"},
	}

	for _, test := range tests {
		dir, file, ext := getFileParts(test.path)
		assert.Equal(t, test.expectedDir, dir)
		assert.Equal(t, test.expectedFile, file)
		assert.Equal(t, test.expectedExt, ext)
	}
}

func TestMkDirTree(t *testing.T) {
	tests := []struct {
		path string
		Err  string
	}{
		{"a/", ""},
		{"b/c/", ""},
		{"a/", ""},
		{"", ""},
	}

	dir, _ := ioutil.TempDir("", "synchrotest")
	s := New()
	s.src = filepath.Join(dir, "src")
	os.MkdirAll(filepath.Join(s.src, "a"), 0777)
	os.MkdirAll(filepath.Join(s.src, "b", "c"), 0777)
	s.dst = filepath.Join(dir, "dst")
	os.Mkdir(s.dst, 0777)
	for _, test := range tests {
		err := s.mkDirTree(test.path)
		if test.Err != "" {
			assert.NotNil(t, err)
			assert.Equal(t, test.Err, err.Error())
			continue
		}
		assert.Nil(t, err)
		// check that everything in the dirpath exists
		dirs := strings.Split(test.path, "/")
		checkDstDir := s.dst
		for _, d := range dirs {
			checkDstDir = filepath.Join(checkDstDir, d)
			fi, err := os.Stat(checkDstDir)
			assert.Nil(t, err)
			assert.Equal(t, true, fi.IsDir())
		}
	}
}

func TestAddStats(t *testing.T) {
	dir, _ := ioutil.TempDir("", "synchrotest")
	data := []byte(`Since I have commenced I would not leave any of these Lays untold. The stories that I know I would tell you forthwith. My hope is now to rehearse to you the story of Yonec, the son of Eudemarec, his mother's first born child. 
`)
	ioutil.WriteFile(filepath.Join(dir, "yonec"), data, 0644)
	fi, err := os.Stat(filepath.Join(dir, "yonec"))
	assert.Nil(t, err)

	s := New()
	s.addNewStats(fi)
	assert.Equal(t, 1, s.newCount.Files)
	assert.Equal(t, 227, s.newCount.Bytes)
	s.addDelStats(fi)
	assert.Equal(t, 1, s.delCount.Files)
	assert.Equal(t, 227, s.delCount.Bytes)
	s.addCopyStats(fi)
	assert.Equal(t, 1, s.copyCount.Files)
	assert.Equal(t, 227, s.copyCount.Bytes)
	s.addUpdateStats(fi)
	assert.Equal(t, 1, s.updateCount.Files)
	assert.Equal(t, 227, s.updateCount.Bytes)
	s.addDupStats(fi)
	assert.Equal(t, 1, s.dupCount.Files)
	assert.Equal(t, 227, s.dupCount.Bytes)
	s.addSkipStats(fi)
	assert.Equal(t, 1, s.skipCount.Files)
	assert.Equal(t, 227, s.skipCount.Bytes)

}
