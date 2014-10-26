package synchronicity

import (
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)


func TestSetHash(t *testing.T) {
	fi, _ := os.Stat("LICENSE")
	fd := FileData{Hash: make([]byte,0,32), Dir: "", Fi: fi}
	err := fd.SetHash(0)
	assert.NotNil(t, err)
	assert.Equal(t, "0", hex.EncodeToString(fd.Hash))
}
