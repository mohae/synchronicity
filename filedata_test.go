package synchronicity

import (
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetHash(t *testing.T) {
	s := New()
	s.hashType = SHA256
	s.equalityType = EqualityDigest
	s.PreDigest = true
	fi, _ := os.Stat("LICENSE")
	fd := NewFileData("", ".", fi, s)
	err := fd.SetHash()
	assert.Nil(t, err)
	assert.Equal(t, "10c2b84254c07bc8c430b00a4fcb55bd95f32101a1bcf5249f5c242da0be7bd0", hex.EncodeToString(fd.Digests[0][0:32]))
}
