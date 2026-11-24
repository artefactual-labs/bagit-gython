package bagit_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/artefactual-labs/bagit-gython"
	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

func TestPooledValidationChecksEveryChecksum(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	v, err := bagit.NewValidator(bagit.WithPoolSize(2), bagit.WithCacheDir(t.TempDir()))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, v.Close()) })

	const badFile = "data/file-19"
	contents := strings.Repeat("x", 4096)
	var manifest strings.Builder
	for i := range 20 {
		manifest.WriteString(fileManifest(fmt.Sprintf("data/file-%02d", i), contents))
	}
	good := completenessBag(t, "1.0", manifest.String())
	bad := completenessBag(t, "1.0", manifest.String())
	for i := range 20 {
		name := fmt.Sprintf("data/file-%02d", i)
		writeBagFile(t, good, name, contents)
		writeBagFile(t, bad, name, contents)
	}
	writeBagFile(t, bad, badFile, strings.Repeat("y", len(contents)))

	var g errgroup.Group
	for range 3 {
		g.Go(func() error { return v.Validate(good) })
		g.Go(func() error {
			err := v.Validate(bad)
			if !errors.Is(err, bagit.ErrInvalid) || !strings.Contains(err.Error(), filepath.FromSlash(badFile)+" sha256 validation failed") {
				return fmt.Errorf("expected a checksum mismatch for %s, got %v", badFile, err)
			}
			return nil
		})
	}
	assert.NilError(t, g.Wait())
}

func TestValidator(t *testing.T) {
	t.Run("Validates bags concurrently", func(t *testing.T) {
		v, err := bagit.NewValidator(bagit.WithPoolSize(2), bagit.WithTempCacheDir())
		assert.NilError(t, err)
		t.Cleanup(func() {
			assert.NilError(t, v.Close())
		})

		assert.Equal(t, v.PoolSize(), 2)

		var g errgroup.Group
		for range 6 {
			g.Go(func() error {
				return v.Validate("internal/testdata/valid-bag")
			})
		}

		assert.NilError(t, g.Wait())
	})

	t.Run("Reports invalid bags", func(t *testing.T) {
		v, err := bagit.NewValidator(bagit.WithTempCacheDir())
		assert.NilError(t, err)
		t.Cleanup(func() {
			assert.NilError(t, v.Close())
		})

		err = v.Validate("/tmp/691b8e7f-e6b7-41dd-bc47-868e2ff69333")
		assert.Assert(t, errors.Is(err, bagit.ErrInvalid))
	})

	t.Run("TryValidate validates bag without waiting", func(t *testing.T) {
		v, err := bagit.NewValidator(bagit.WithTempCacheDir())
		assert.NilError(t, err)
		t.Cleanup(func() {
			assert.NilError(t, v.Close())
		})

		err = v.TryValidate("internal/testdata/valid-bag")
		assert.NilError(t, err)
	})

	t.Run("Deferred runtime validates bag", func(t *testing.T) {
		v, err := bagit.NewValidator(bagit.WithDeferredRuntime(), bagit.WithTempCacheDir())
		assert.NilError(t, err)
		t.Cleanup(func() {
			assert.NilError(t, v.Close())
		})

		err = v.Validate("internal/testdata/valid-bag")
		assert.NilError(t, err)
	})

	t.Run("Rejects invalid pool size", func(t *testing.T) {
		_, err := bagit.NewValidator(bagit.WithPoolSize(0))
		assert.Error(t, err, "pool size must be greater than zero")
	})

	t.Run("Returns ErrClosed after close", func(t *testing.T) {
		v, err := bagit.NewValidator(bagit.WithTempCacheDir())
		assert.NilError(t, err)

		assert.NilError(t, v.Close())
		assert.NilError(t, v.Close())

		err = v.Validate("internal/testdata/valid-bag")
		assert.ErrorIs(t, err, bagit.ErrClosed)
	})
}
