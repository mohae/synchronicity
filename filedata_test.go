package synchronicity

import (
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)


func TestSetHash(t *testing.T) {
	fi, _ := os.Stat("LICENSE")
	fd := NewFileData("", ".", fi)
	fd.ChunkSize = 0
	err := fd.SetHash()
	assert.NotNil(t, err)
	assert.Equal(t, "", hex.EncodeToString(fd.Hash))
}
