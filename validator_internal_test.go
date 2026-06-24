package bagit

import (
	"context"
	"os"
	"testing"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

func TestValidatorBoundsTempDirectories(t *testing.T) {
	v, err := NewValidator(WithPoolSize(2))
	assert.NilError(t, err)

	dirs := validatorTempDirs(v)
	assert.Equal(t, len(dirs), 2)

	var g errgroup.Group
	for i := 0; i < 6; i++ {
		g.Go(func() error {
			return v.Validate("internal/testdata/valid-bag")
		})
	}
	assert.NilError(t, g.Wait())

	assert.DeepEqual(t, validatorTempDirs(v), dirs)
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

func validatorTempDirs(v *Validator) []string {
	v.mu.Lock()
	defer v.mu.Unlock()

	dirs := make([]string, 0, len(v.pool))
	for _, b := range v.pool {
		dirs = append(dirs, b.tmpDir)
	}

	return dirs
}
