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
	v, err := NewValidator(WithPoolSize(4), WithTempCacheDir())
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

func TestValidatorEmptyCacheDirUsesDefault(t *testing.T) {
	cacheHome := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", cacheHome)
	} else {
		t.Setenv("HOME", cacheHome)
		t.Setenv("XDG_CACHE_HOME", filepath.Join(cacheHome, ".cache"))
	}

	cacheDir := defaultValidatorCacheDir()
	assert.Assert(t, cacheDir != "")

	v, err := NewValidator(WithCacheDir(""))
	assert.NilError(t, err)

	dirs := validatorRuntimeDirs(v)
	assert.DeepEqual(t, dirs, []string{cacheDir})

	assert.NilError(t, v.Close())
	assertPathExists(t, cacheDir)
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

func TestValidatorWithDeferredRuntimeBootstrapsOnFirstValidate(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")

	v, err := NewValidator(WithDeferredRuntime(), WithCacheDir(cacheDir))
	assert.NilError(t, err)
	t.Cleanup(func() {
		assert.NilError(t, v.Close())
	})

	assert.DeepEqual(t, validatorRuntimeDirs(v), []string{})
	assert.DeepEqual(t, validatorRuntimeExtractedPaths(v), []string(nil))
	_, err = os.Stat(cacheDir)
	assert.Assert(t, os.IsNotExist(err))

	assert.NilError(t, v.Validate("internal/testdata/valid-bag"))

	assert.DeepEqual(t, validatorRuntimeDirs(v), []string{cacheDir})
	paths := validatorRuntimeExtractedPaths(v)
	assert.Equal(t, len(paths), 3)
	for _, path := range paths {
		assertPathInDir(t, path, cacheDir)
		assertPathExists(t, path)
	}
}

func TestValidatorWithDeferredRuntimeBootstrapsOnceForConcurrentRequests(t *testing.T) {
	v, err := NewValidator(WithDeferredRuntime(), WithPoolSize(4), WithTempCacheDir())
	assert.NilError(t, err)

	assert.DeepEqual(t, validatorRuntimeDirs(v), []string{})
	assert.Equal(t, v.PoolSize(), 4)

	var g errgroup.Group
	for range 8 {
		g.Go(func() error {
			return v.Validate("internal/testdata/valid-bag")
		})
	}
	assert.NilError(t, g.Wait())

	dirs := validatorRuntimeDirs(v)
	assert.Equal(t, len(dirs), 1)
	assert.NilError(t, v.Close())

	for _, dir := range dirs {
		_, err := os.Stat(dir)
		assert.Assert(t, os.IsNotExist(err))
	}
}

func TestValidatorWithDeferredRuntimeCloseBeforeFirstValidate(t *testing.T) {
	v, err := NewValidator(WithDeferredRuntime(), WithTempCacheDir())
	assert.NilError(t, err)

	assert.DeepEqual(t, validatorRuntimeDirs(v), []string{})
	assert.NilError(t, v.Close())
	assert.NilError(t, v.Close())

	err = v.Validate("internal/testdata/valid-bag")
	assert.ErrorIs(t, err, ErrClosed)
}

func TestValidatorWithDeferredRuntimeRejectsUnsafeCacheDirOnFirstValidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix mode bits are not reliable on windows")
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	assert.NilError(t, os.Mkdir(cacheDir, 0o700))
	assert.NilError(t, os.Chmod(cacheDir, 0o777))

	v, err := NewValidator(WithDeferredRuntime(), WithCacheDir(cacheDir))
	assert.NilError(t, err)
	t.Cleanup(func() {
		assert.NilError(t, v.Close())
	})

	err = v.Validate("internal/testdata/valid-bag")
	assert.ErrorContains(t, err, "unsafe")

	err = v.Validate("internal/testdata/valid-bag")
	assert.ErrorContains(t, err, "unsafe")
}

func TestValidatorWithDeferredRuntimeTryValidateBootstraps(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")

	v, err := NewValidator(WithDeferredRuntime(), WithCacheDir(cacheDir))
	assert.NilError(t, err)
	t.Cleanup(func() {
		assert.NilError(t, v.Close())
	})

	assert.DeepEqual(t, validatorRuntimeDirs(v), []string{})

	err = v.TryValidate("internal/testdata/valid-bag")
	assert.NilError(t, err)
	assert.DeepEqual(t, validatorRuntimeDirs(v), []string{cacheDir})
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
	v, err := NewValidator(WithPoolSize(1), WithTempCacheDir())
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
	v, err := NewValidator(WithPoolSize(1), WithTempCacheDir())
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
