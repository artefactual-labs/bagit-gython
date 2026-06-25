package bagit

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

func TestValidatorSharesRuntimeRootAcrossPool(t *testing.T) {
	v, err := NewValidator(WithPoolSize(4), WithCacheDir(""))
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

func TestValidatorUsesPersistentCacheDir(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")

	v, err := NewValidator(WithPoolSize(4), WithCacheDir(cacheDir))
	assert.NilError(t, err)

	dirs := validatorRuntimeDirs(v)
	assert.DeepEqual(t, dirs, []string{cacheDir})
	assert.Equal(t, v.PoolSize(), 4)

	paths := validatorRuntimeExtractedPaths(v)
	assert.Equal(t, len(paths), 3)
	for _, path := range paths {
		assertPathInDir(t, path, cacheDir)
		assertPathExists(t, path)
	}

	assert.NilError(t, v.Close())
	assertPathExists(t, cacheDir)
	for _, path := range paths {
		assertPathExists(t, path)
	}

	v, err = NewValidator(WithPoolSize(4), WithCacheDir(cacheDir))
	assert.NilError(t, err)
	assert.DeepEqual(t, validatorRuntimeDirs(v), []string{cacheDir})
	assert.DeepEqual(t, validatorRuntimeExtractedPaths(v), paths)
	assert.NilError(t, v.Close())
}

func TestValidatorRejectsUnsafeCacheDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix mode bits are not reliable on windows")
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	assert.NilError(t, os.Mkdir(cacheDir, 0o700))
	assert.NilError(t, os.Chmod(cacheDir, 0o777))

	v, err := NewValidator(WithCacheDir(cacheDir))
	if v != nil {
		t.Cleanup(func() {
			assert.NilError(t, v.Close())
		})
	}
	assert.ErrorContains(t, err, "unsafe")
}

func TestValidatorValidateContextHonorsWaitCancellation(t *testing.T) {
	v, err := NewValidator(WithPoolSize(1), WithCacheDir(""))
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
	v, err := NewValidator(WithPoolSize(1), WithCacheDir(""))
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
		dir := b.runtime.rootDir
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}

	return dirs
}

func validatorRuntimeExtractedPaths(v *Validator) []string {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.runtime == nil {
		return nil
	}

	return []string{
		v.runtime.embedPython.GetExtractedPath(),
		v.runtime.embedBagit.GetExtractedPath(),
		v.runtime.embedRunner.GetExtractedPath(),
	}
}

func assertPathInDir(t *testing.T, path, dir string) {
	t.Helper()

	rel, err := filepath.Rel(dir, path)
	assert.NilError(t, err)
	assert.Assert(t, rel != ".")
	assert.Assert(t, !strings.HasPrefix(rel, ".."))
	assert.Assert(t, !filepath.IsAbs(rel))
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()

	_, err := os.Stat(path)
	assert.NilError(t, err)
}
