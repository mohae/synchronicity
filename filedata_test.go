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
	assert.Nil(t, err)
	assert.Equal(t, "cf574d4ef67305ad4febe985ec92acc9ce5bc5aedeb11af09c323e41a78348cf", hex.EncodeToString(fd.Hashes[0][0:32]))
}
