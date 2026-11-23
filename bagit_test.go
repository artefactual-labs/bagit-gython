package bagit_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/artefactual-labs/bagit-gython"
	"golang.org/x/sync/errgroup"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/fs"
)

func setUp(tb testing.TB) *bagit.BagIt {
	tb.Helper()

	b, err := bagit.NewBagIt()
	assert.NilError(tb, err)

	tb.Cleanup(func() {
		assert.NilError(tb, b.Cleanup())
	})

	return b
}

func BenchmarkValidate(b *testing.B) {
	bagit := setUp(b)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = bagit.Validate("internal/testdata/valid-bag")
	}
}

func TestConcurrency(t *testing.T) {
	t.Parallel()

	t.Run("Validates bag", func(t *testing.T) {
		t.Parallel()

		b := setUp(t)

		err := b.Validate("internal/testdata/valid-bag")
		assert.NilError(t, err)
	})

	t.Run("Returns ErrBusy if the resource is busy", func(t *testing.T) {
		t.Parallel()

		b := setUp(t)

		// This test should pass because each call to Validate() creates its own
		// distinct Python interpreter instance.
		var g errgroup.Group
		for range 3 {
			g.Go(func() error {
				return b.Validate("internal/testdata/valid-bag")
			})
		}

		err := g.Wait()
		assert.ErrorIs(t, err, bagit.ErrBusy)
	})

	t.Run("Parallel execution", func(t *testing.T) {
		t.Parallel()

		// *bagit.BagIt is not shareable, each goroutine must create its own.
		var g errgroup.Group
		for range 3 {
			g.Go(func() error {
				b := setUp(t)
				return b.Validate("internal/testdata/valid-bag")
			})
		}

		err := g.Wait()
		assert.NilError(t, err)
	})
}

func TestValidateBag(t *testing.T) {
	t.Parallel()

	t.Run("Fails validation", func(t *testing.T) {
		t.Parallel()

		b := setUp(t)

		err := b.Validate("/tmp/691b8e7f-e6b7-41dd-bc47-868e2ff69333")
		assert.Error(t, err, "invalid: Expected bagit.txt does not exist: /tmp/691b8e7f-e6b7-41dd-bc47-868e2ff69333/bagit.txt")
		assert.Assert(t, errors.Is(err, bagit.ErrInvalid))
	})

	t.Run("Validates bag", func(t *testing.T) {
		t.Parallel()

		b := setUp(t)

		err := b.Validate("internal/testdata/valid-bag")
		assert.NilError(t, err)
	})
}

func TestMakeBag(t *testing.T) {
	t.Parallel()

	t.Run("Creates bag", func(t *testing.T) {
		t.Parallel()

		tmpDir := fs.NewDir(t, "", fs.WithFile("test.txt", "abcd"))

		b := setUp(t)

		err := b.Make(tmpDir.Path())
		assert.NilError(t, err)

		assert.Assert(t, fs.Equal(tmpDir.Path(), fs.Expected(t,
			fs.WithDir("data", fs.WithFile("test.txt", "abcd"), fs.MatchAnyFileMode),
			fs.WithFile("bagit.txt", `BagIt-Version: 1.0
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

		b := setUp(t)

		err := b.Make("non-existent-dir")
		assert.ErrorContains(t, err, "does not exist")
	})
}

func TestMakeWithOptions(t *testing.T) {
	b := setUp(t)
	for _, tc := range []struct {
		name       string
		algorithms []string
		want       []string
	}{
		{"MD5", []string{"md5"}, []string{"md5"}},
		{"SHA-1", []string{"sha1"}, []string{"sha1"}},
		{"SHA-256", []string{"sha256"}, []string{"sha256"}},
		{"SHA-512", []string{"sha512"}, []string{"sha512"}},
		{"Multiple algorithms", []string{"sha512", "sha1"}, []string{"sha1", "sha512"}},
		{"Defaults", []string{}, []string{"sha256", "sha512"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fs.NewDir(t, "", fs.WithFile("payload file.txt", "abcd"))
			options := bagit.MakeOptions{ChecksumAlgorithms: tc.algorithms}
			assert.NilError(t, b.MakeWithOptions(dir.Path(), options))
			assertManifestAlgorithms(t, dir.Path(), tc.want)
			payload, err := os.ReadFile(dir.Join("data", "payload file.txt"))
			assert.NilError(t, err)
			assert.Equal(t, string(payload), "abcd")
			assert.NilError(t, b.Validate(dir.Path()))
		})
	}
	t.Run("Invalid UTF-8 path", func(t *testing.T) {
		dir := fs.NewDir(t, "", fs.WithDir("target-\uFFFD", fs.WithFile("payload.txt", "abcd")))
		options := bagit.MakeOptions{ChecksumAlgorithms: []string{"sha512"}}
		assert.ErrorContains(t, b.MakeWithOptions(dir.Join("target-\xff"), options), "valid UTF-8")
		assert.Assert(t, fs.Equal(dir.Path(), fs.Expected(t,
			fs.WithDir("target-\uFFFD", fs.WithFile("payload.txt", "abcd")),
		)))
	})
}

func TestMakeRejectsInvalidOptionsBeforeMovingPayload(t *testing.T) {
	b := setUp(t)
	for _, tc := range []struct {
		name       string
		algorithms []string
		want       string
	}{
		{"Unknown hash", []string{"sha512", "unknown-hash"}, "Unsupported checksum algorithm"},
		{"Variable-length digest", []string{"shake_128"}, "Unsupported checksum algorithm"},
		{"Duplicate hashes", []string{"sha512", "sha512"}, "Duplicate checksum algorithms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fs.NewDir(t, "", fs.WithFile("payload.txt", "abcd"))
			options := bagit.MakeOptions{ChecksumAlgorithms: tc.algorithms}
			assert.ErrorContains(t, b.MakeWithOptions(dir.Path(), options), tc.want)
			assert.Assert(t, fs.Equal(dir.Path(), fs.Expected(t,
				fs.WithFile("payload.txt", "abcd"),
			)))
			assert.NilError(t, b.Make(dir.Path()))
		})
	}
}

func TestMakeEmptyPayload(t *testing.T) {
	b := setUp(t)
	t.Run("Empty directory with default manifests", func(t *testing.T) {
		dir := fs.NewDir(t, "")
		assert.NilError(t, b.Make(dir.Path()))
		assertEmptyBag(t, b, dir.Path(), []string{"sha256", "sha512"})
	})
	t.Run("Empty subdirectories with selected manifests", func(t *testing.T) {
		dir := fs.NewDir(t, "", fs.WithDir("empty", fs.WithDir("nested")))
		options := bagit.MakeOptions{ChecksumAlgorithms: []string{"sha512"}}
		assert.NilError(t, b.MakeWithOptions(dir.Path(), options))
		empty, err := os.ReadDir(dir.Join("data", "empty", "nested"))
		assert.NilError(t, err)
		assert.Equal(t, len(empty), 0)
		assertEmptyBag(t, b, dir.Path(), []string{"sha512"})
	})
}

func assertEmptyBag(t *testing.T, b *bagit.BagIt, dir string, algorithms []string) {
	t.Helper()
	assertManifestAlgorithms(t, dir, algorithms)
	info, err := os.ReadFile(filepath.Join(dir, "bag-info.txt"))
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(info), "Payload-Oxum: 0.0"))
	for _, algorithm := range algorithms {
		name := "manifest-" + algorithm + ".txt"
		manifest, err := os.ReadFile(filepath.Join(dir, name))
		assert.NilError(t, err)
		assert.Equal(t, len(manifest), 0)
		tags, err := os.ReadFile(filepath.Join(dir, "tag"+name))
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(string(tags), " "+name))
	}
	assert.NilError(t, b.CheckCompleteness(dir))
	assert.NilError(t, b.Validate(dir))
}

func assertManifestAlgorithms(t *testing.T, dir string, algorithms []string) {
	t.Helper()
	want := make([]string, 0, 2*len(algorithms))
	for _, algorithm := range algorithms {
		name := "manifest-" + algorithm + ".txt"
		want = append(want, filepath.Join(dir, name), filepath.Join(dir, "tag"+name))
	}
	slices.Sort(want)
	got, err := filepath.Glob(filepath.Join(dir, "*manifest-*.txt"))
	assert.NilError(t, err)
	assert.DeepEqual(t, got, want)
}

func TestCleanup(t *testing.T) {
	t.Parallel()

	b, err := bagit.NewBagIt()
	assert.NilError(t, err)

	err = b.Cleanup()
	assert.NilError(t, err)

	err = b.Validate("internal/testdata/valid-bag")
	assert.ErrorIs(t, err, bagit.ErrClosed)

	err = b.Make("internal/testdata/valid-bag")
	assert.ErrorIs(t, err, bagit.ErrClosed)
}
