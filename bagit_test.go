package bagit_test

import (
	"testing"

	"github.com/artefactual-labs/bagit-gython"
	"golang.org/x/sync/errgroup"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/fs"
)

func TestValidateBag(t *testing.T) {
	t.Parallel()

	t.Run("Fails validation", func(t *testing.T) {
		t.Parallel()

		b, err := bagit.NewBagIt()
		assert.NilError(t, err)

		err = b.Validate("/tmp/691b8e7f-e6b7-41dd-bc47-868e2ff69333")
		assert.Error(t, err, "invalid: Expected bagit.txt does not exist: /tmp/691b8e7f-e6b7-41dd-bc47-868e2ff69333/bagit.txt")

		err = b.Cleanup()
		assert.NilError(t, err)
	})

	t.Run("Validates bag", func(t *testing.T) {
		t.Parallel()

		b, err := bagit.NewBagIt()
		assert.NilError(t, err)

		err = b.Validate("internal/testdata/valid-bag")
		assert.NilError(t, err)

		err = b.Cleanup()
		assert.NilError(t, err)
	})

	t.Run("Validates bag concurrently", func(t *testing.T) {
		t.Parallel()

		b, err := bagit.NewBagIt()
		assert.NilError(t, err)
		t.Cleanup(func() { b.Cleanup() })

		// This test should pass because each call to Validate() creates its own
		// distinct Python interpreter instance.
		var g errgroup.Group
		for i := 0; i < 10; i++ {
			g.Go(func() error {
				return b.Validate("internal/testdata/valid-bag")
			})
		}

		err = g.Wait()
		assert.NilError(t, err)
	})

	t.Run("Creates bag", func(t *testing.T) {
		t.Parallel()

		tmpDir := fs.NewDir(t, "", fs.WithFile("test.txt", "abcd"))

		b, err := bagit.NewBagIt()
		assert.NilError(t, err)
		t.Cleanup(func() { b.Cleanup() })

		err = b.Make(tmpDir.Path())
		assert.NilError(t, err)

		assert.Assert(t, fs.Equal(tmpDir.Path(), fs.Expected(t,
			fs.WithDir("data", fs.WithFile("test.txt", "abcd"), fs.MatchAnyFileMode),
			fs.WithFile("bagit.txt", `BagIt-Version: 0.97
Tag-File-Character-Encoding: UTF-8
`, fs.MatchAnyFileMode),
			fs.WithFile("bag-info.txt", "", fs.MatchAnyFileContent, fs.MatchAnyFileMode),
			fs.WithFile("manifest-sha256.txt", "", fs.MatchAnyFileContent, fs.MatchAnyFileMode),
			fs.WithFile("manifest-sha512.txt", "", fs.MatchAnyFileContent, fs.MatchAnyFileMode),
			fs.WithFile("tagmanifest-sha256.txt", "", fs.MatchAnyFileContent, fs.MatchAnyFileMode),
			fs.WithFile("tagmanifest-sha512.txt", "", fs.MatchAnyFileContent, fs.MatchAnyFileMode),
		)))
	})

	t.Run("Reports creation failures", func(t *testing.T) {
		t.Parallel()

		b, err := bagit.NewBagIt()
		assert.NilError(t, err)

		err = b.Make("non-existent-dir")
		assert.ErrorContains(t, err, "does not exist")
	})
}
