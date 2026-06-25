package runner

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"testing"

	"gotest.tools/v3/assert"
)

type fileList struct {
	ContentHash string          `json:"contentHash"`
	Files       []fileListEntry `json:"files"`
}

type fileListEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mode uint32 `json:"perm"`
}

func TestFilesJSONContentHashMatchesRunnerSource(t *testing.T) {
	blob, err := Source.ReadFile("files.json")
	assert.NilError(t, err)

	fl := fileList{}
	assert.NilError(t, json.Unmarshal(blob, &fl))
	assert.Assert(t, fl.ContentHash != "")

	hash := sha256.New()
	for _, file := range fl.Files {
		blob, err := Source.ReadFile(file.Name)
		assert.NilError(t, err)
		assert.Equal(t, int64(len(blob)), file.Size)

		_ = binary.Write(hash, binary.LittleEndian, "regular")
		_ = binary.Write(hash, binary.LittleEndian, file.Name)
		_ = binary.Write(hash, binary.LittleEndian, blob)
	}

	assert.Equal(t, fl.ContentHash, hex.EncodeToString(hash.Sum(nil)))
}
