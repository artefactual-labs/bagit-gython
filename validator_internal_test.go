package bagit

import (
	"context"
	"os"
	"testing"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

func TestValidatorSharesRuntimeRootAcrossPool(t *testing.T) {
	v, err := NewValidator(WithPoolSize(4))
	assert.NilError(t, err)

	dirs := validatorRuntimeDirs(v)
	assert.Equal(t, len(dirs), 1)
	assert.Equal(t, v.PoolSize(), 4)

	var g errgroup.Group
	for range 8 {
		g.Go(func() error {
			return v.Validate("internal/testdata/valid-bag")
		})
	}
	assert.NilError(t, g.Wait())

	assert.DeepEqual(t, validatorRuntimeDirs(v), dirs)
	assert.NilError(t, v.Close())

	for _, dir := range dirs {
		_, err := os.Stat(dir)
		assert.Assert(t, os.IsNotExist(err))
	}
}

func TestValidatorValidateContextHonorsWaitCancellation(t *testing.T) {
	v, err := NewValidator(WithPoolSize(1))
	assert.NilError(t, err)
	t.Cleanup(func() {
		assert.NilError(t, v.Close())
	})

	assert.NilError(t, v.sem.Acquire(context.Background(), 1))
	defer v.sem.Release(1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = v.ValidateContext(ctx, "internal/testdata/valid-bag")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestValidatorTryValidateReturnsErrBusyWhenPoolBusy(t *testing.T) {
	v, err := NewValidator(WithPoolSize(1))
	assert.NilError(t, err)
	t.Cleanup(func() {
		assert.NilError(t, v.Close())
	})

	assert.Assert(t, v.sem.TryAcquire(1))
	defer v.sem.Release(1)

	err = v.TryValidate("internal/testdata/valid-bag")
	assert.ErrorIs(t, err, ErrBusy)
}

func validatorRuntimeDirs(v *Validator) []string {
	v.mu.Lock()
	defer v.mu.Unlock()

	dirs := make([]string, 0, 1)
	seen := make(map[string]struct{})
	for _, b := range v.pool {
		if b.runtime == nil {
			continue
		}
		dir := b.runtime.tmpDir
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}

	return dirs
}
