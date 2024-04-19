package bagit_test

import (
	"testing"

	"github.com/artefactual-labs/bagit-gython"

	"gotest.tools/v3/assert"
)

func TestBagit(t *testing.T) {
	b, err := bagit.NewBagIt()
	assert.NilError(t, err)

	err = b.Validate("/tmp/691b8e7f-e6b7-41dd-bc47-868e2ff69333")
	assert.Error(t, err, "invalid: Expected bagit.txt does not exist: /tmp/691b8e7f-e6b7-41dd-bc47-868e2ff69333/bagit.txt")

	err = b.Validate("internal/testdata/valid-bag")
	assert.NilError(t, err)

	err = b.Cleanup()
	assert.NilError(t, err)
}
