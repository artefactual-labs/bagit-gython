package bagit_test

import (
	"crypto/sha512"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/artefactual-labs/bagit-gython"
	"gotest.tools/v3/assert"
)

func TestValidateStrict(t *testing.T) {
	v, err := bagit.NewValidator(bagit.WithCacheDir(t.TempDir()))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, v.Close()) })

	t.Run("verifies both payload and tag algorithms", func(t *testing.T) {
		b := setUp(t)
		path := filepath.Join(t.TempDir(), "tagmanifest-folder")
		writeBagFile(t, path, "file.txt", "good")
		assert.NilError(t, b.Make(path))
		assert.NilError(t, b.ValidateStrict(path))
		assert.NilError(t, v.ValidateStrictContext(nil, path))
	})

	t.Run("requires each BagIt 1.0 payload manifest to cover the payload", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/one.txt", "first")+fileManifest("data/two.txt", "second"))
		writeBagFile(t, path, "data/one.txt", "first")
		writeBagFile(t, path, "data/two.txt", "second")
		writeBagFile(t, path, "manifest-sha512.txt", fmt.Sprintf("%x  data/one.txt\n", sha512.Sum512([]byte("first"))))
		assert.NilError(t, v.Validate(path))
		want := requireIncomplete(t, v.CheckCompleteness(path))
		assert.DeepEqual(t, want.Differences, &bagit.FileDifferences{
			Unexpected: 1,
			Examples:   []bagit.PathDifference{{Kind: bagit.DifferenceUnexpected, Path: "data/two.txt"}},
		})
		assert.DeepEqual(t, requireIncomplete(t, v.ValidateStrict(path)), want)

		writeBagFile(t, path, "bagit.txt", "BagIt-Version: 0.97\nTag-File-Character-Encoding: UTF-8\n")
		assert.NilError(t, v.ValidateStrict(path))
	})

	t.Run("rejects a manifest checksum without a filename", func(t *testing.T) {
		const file = "data/file.txt"
		payloadManifest := fileManifest(file, "good")
		for _, tc := range []struct{ name, entries string }{
			{"manifest-sha256.txt", payloadManifest},
			{"tagmanifest-sha256.txt", fileManifest("manifest-sha256.txt", payloadManifest)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				path := completenessBag(t, "1.0", payloadManifest)
				writeBagFile(t, path, file, "good")
				contents := "# comment\n\n" + tc.entries
				writeBagFile(t, path, tc.name, contents)
				assert.NilError(t, v.ValidateStrict(path))

				writeBagFile(t, path, tc.name, contents+strings.Repeat("a", 64)+"\n")
				invalid := requireIncomplete(t, v.ValidateStrict(path))
				assert.ErrorContains(t, invalid, tc.name)
				assert.ErrorContains(t, invalid, "filename")
				assert.Assert(t, invalid.Differences == nil)
				assert.NilError(t, v.CheckCompleteness(path))
				assert.NilError(t, v.Validate(path))
			})
		}
	})

	t.Run("detects a changed payload file with the same size", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/file.txt", "good"))
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 4.1\n")
		writeBagFile(t, path, "data/file.txt", "bad!")
		assert.NilError(t, v.CheckCompleteness(path))
		assertChecksumMismatch(t, v.ValidateStrict(path), "file.txt", "sha256")
	})

	t.Run("uses the filesystem spelling of a payload name", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/café.txt", "good"))
		writeBagFile(t, path, "data/cafe\u0301.txt", "good")
		assert.NilError(t, v.ValidateStrict(path))
	})

	t.Run("rejects a bad SHA-512 digest with a correct SHA-256 digest", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/file.txt", "good"))
		writeBagFile(t, path, "data/file.txt", "good")
		writeBagFile(t, path, "manifest-sha512.txt", fmt.Sprintf("%x  data/file.txt\n", sha512.Sum512([]byte("bad!"))))
		assertChecksumMismatch(t, v.ValidateStrict(path), "file.txt", "sha512")
	})

	for _, version := range []string{"1.0", "0.96"} {
		t.Run("detects a changed listed tag in BagIt "+version, func(t *testing.T) {
			path := completenessBag(t, version, "")
			writeBagFile(t, path, "tagmanifest-sha256.txt", fileManifest("manifest-sha256.txt", "")+fileManifest("note.txt", "good"))
			writeBagFile(t, path, "note.txt", "good")
			assert.NilError(t, v.ValidateStrict(path))
			writeBagFile(t, path, "note.txt", "bad!")
			assert.NilError(t, v.CheckCompleteness(path))
			assertChecksumMismatch(t, v.ValidateStrict(path), "note.txt", "sha256")
		})
	}

	t.Run("requires every listed algorithm to be verifiable", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/file.txt", "good"))
		writeBagFile(t, path, "manifest-unavailable.txt", fileManifest("data/file.txt", "good"))
		// Completeness fails before evaluating the unsupported algorithm.
		assert.DeepEqual(t, requireIncomplete(t, v.ValidateStrict(path)), requireIncomplete(t, v.CheckCompleteness(path)))

		writeBagFile(t, path, "data/file.txt", "good")
		assert.NilError(t, v.CheckCompleteness(path))
		err := v.ValidateStrict(path)
		assertExecutionError(t, err)
		assert.ErrorContains(t, err, "unavailable")
		assert.ErrorContains(t, err, "file.txt")
	})

	t.Run("distinguishes a checksum read failure from a mismatch", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("requires filesystem permissions enforced by chmod")
		}
		path := completenessBag(t, "1.0", fileManifest("data/file.txt", "good"))
		writeBagFile(t, path, "data/file.txt", "good")
		file := filepath.Join(path, "data", "file.txt")
		assert.NilError(t, os.Chmod(file, 0))
		t.Cleanup(func() { assert.NilError(t, os.Chmod(file, 0o600)) })
		if f, err := os.Open(file); err == nil {
			assert.NilError(t, f.Close())
			t.Skip("test process can read the file despite its permissions")
		} else {
			assert.ErrorIs(t, err, os.ErrPermission)
		}
		assert.NilError(t, v.CheckCompleteness(path))
		assertExecutionError(t, v.ValidateStrict(path))
	})

	t.Run("reuses the pool after a staging error", func(t *testing.T) {
		assertExecutionError(t, v.ValidateStrict(filepath.Join(t.TempDir(), "absent")))
		assert.NilError(t, v.ValidateStrict("internal/testdata/valid-bag"))
	})
}

func assertChecksumMismatch(t *testing.T, err error, path, algorithm string) {
	t.Helper()
	assert.ErrorIs(t, err, bagit.ErrInvalid)
	var completenessErr *bagit.CompletenessError
	assert.Assert(t, !errors.As(err, &completenessErr), "%v", err)
	assert.ErrorContains(t, err, algorithm+" checksum mismatch")
	assert.ErrorContains(t, err, path)
}
