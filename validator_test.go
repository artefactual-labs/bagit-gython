package bagit_test

import (
	"errors"
	"testing"

	"github.com/artefactual-labs/bagit-gython"
	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

func TestValidator(t *testing.T) {
	t.Run("Validates bags concurrently", func(t *testing.T) {
		v, err := bagit.NewValidator(bagit.WithPoolSize(2), bagit.WithCacheDir(""))
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
		v, err := bagit.NewValidator(bagit.WithCacheDir(""))
		assert.NilError(t, err)
		t.Cleanup(func() {
			assert.NilError(t, v.Close())
		})

		err = v.Validate("/tmp/691b8e7f-e6b7-41dd-bc47-868e2ff69333")
		assert.Assert(t, errors.Is(err, bagit.ErrInvalid))
	})

	t.Run("TryValidate validates bag without waiting", func(t *testing.T) {
		v, err := bagit.NewValidator(bagit.WithCacheDir(""))
		assert.NilError(t, err)
		t.Cleanup(func() {
			assert.NilError(t, v.Close())
		})

		err = v.TryValidate("internal/testdata/valid-bag")
		assert.NilError(t, err)
	})

	t.Run("Rejects invalid pool size", func(t *testing.T) {
		_, err := bagit.NewValidator(bagit.WithPoolSize(0))
		assert.Error(t, err, "pool size must be greater than zero")
	})

	t.Run("Returns ErrClosed after close", func(t *testing.T) {
		v, err := bagit.NewValidator(bagit.WithCacheDir(""))
		assert.NilError(t, err)

		assert.NilError(t, v.Close())
		assert.NilError(t, v.Close())

		err = v.Validate("internal/testdata/valid-bag")
		assert.ErrorIs(t, err, bagit.ErrClosed)
	})
}
