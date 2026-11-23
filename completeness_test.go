package bagit_test

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/artefactual-labs/bagit-gython"
	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

type fullValidator interface {
	Validate(string) error
}

var (
	_ fullValidator                        = (*bagit.BagIt)(nil)
	_ fullValidator                        = (*bagit.Validator)(nil)
	_ func(*bagit.Validator, string) error = (*bagit.Validator).Validate
)

func TestCompletenessAndFullValidationShareRunners(t *testing.T) {
	b := setUp(t)
	v, err := bagit.NewValidator(bagit.WithPoolSize(2), bagit.WithDeferredRuntime(), bagit.WithCacheDir(t.TempDir()))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, v.Close()) })

	path := completenessBag(t, "0.97", fileManifest("*data/file with spaces.txt", "good"))
	writeBagFile(t, path, "data/file with spaces.txt", "good")
	assert.NilError(t, b.CheckCompleteness(path))
	assert.NilError(t, v.CheckCompletenessContext(nil, path))
	assert.NilError(t, b.Validate(path))
	assert.NilError(t, v.Validate(path))
	assert.NilError(t, v.ValidateContext(context.Background(), path))
	assert.NilError(t, v.TryValidate(path))

	// Keep the payload's name, count and size unchanged.
	writeBagFile(t, path, "data/file with spaces.txt", "bad!")
	assert.NilError(t, b.CheckCompleteness(path))
	assert.ErrorIs(t, b.Validate(path), bagit.ErrInvalid)
	assert.ErrorIs(t, v.Validate(path), bagit.ErrInvalid)
	assert.ErrorIs(t, v.ValidateContext(context.Background(), path), bagit.ErrInvalid)
	assert.ErrorIs(t, v.TryValidate(path), bagit.ErrInvalid)
	assert.NilError(t, v.CheckCompleteness(path))

	var group errgroup.Group
	for range 3 {
		group.Go(func() error { return v.CheckCompletenessContext(context.Background(), path) })
		group.Go(func() error {
			if err := v.Validate(path); !errors.Is(err, bagit.ErrInvalid) {
				return fmt.Errorf("full validation of changed payload returned %v", err)
			}
			return nil
		})
	}
	assert.NilError(t, group.Wait())
	assert.NilError(t, v.Close())
	assert.ErrorIs(t, v.CheckCompleteness(path), bagit.ErrClosed)
	assert.NilError(t, b.Cleanup())
	assert.ErrorIs(t, b.CheckCompleteness(path), bagit.ErrClosed)
}

func TestCompletenessReportsFileDifferences(t *testing.T) {
	v, err := bagit.NewValidator(bagit.WithCacheDir(t.TempDir()))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, v.Close()) })
	manifest := fileManifest("data/.DS_Store", "x") + fileManifest("data/kept.txt", "ok")

	for _, metadata := range []struct{ name, content string }{
		{"without metadata", ""},
		{"without Oxum", "Source-Organization: Example\n"},
		{"with Oxum", "Payload-Oxum: 3.2\n"},
	} {
		t.Run(metadata.name, func(t *testing.T) {
			path := completenessBag(t, "1.0", manifest)
			writeBagFile(t, path, "data/kept.txt", "ok")
			if metadata.content != "" {
				writeBagFile(t, path, "bag-info.txt", metadata.content)
			}
			writeBagFile(t, path, "manifest-sha512.txt", fmt.Sprintf("%x  data/.DS_Store\n%x  data/kept.txt\n",
				sha512.Sum512([]byte("x")), sha512.Sum512([]byte("ok"))))

			assert.DeepEqual(t, requireIncomplete(t, v.CheckCompleteness(path)).Differences, &bagit.FileDifferences{
				Missing:  1,
				Examples: []bagit.PathDifference{{Kind: bagit.DifferenceMissing, Path: "data/.DS_Store"}},
			})
			blob, err := os.ReadFile(filepath.Join(path, "manifest-sha256.txt"))
			assert.NilError(t, err)
			assert.Equal(t, string(blob), manifest)
			files, err := os.ReadDir(filepath.Join(path, "data"))
			assert.NilError(t, err)
			assert.Equal(t, len(files), 1)
			assert.Equal(t, files[0].Name(), "kept.txt")
		})
	}

	t.Run("rename with unchanged Oxum", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/old name.txt", "ok"))
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 2.1\n")
		writeBagFile(t, path, "data/new name.txt", "ok")
		assert.DeepEqual(t, requireIncomplete(t, v.CheckCompleteness(path)).Differences, &bagit.FileDifferences{
			Missing: 1, Unexpected: 1,
			Examples: []bagit.PathDifference{
				{Kind: bagit.DifferenceMissing, Path: "data/old name.txt"},
				{Kind: bagit.DifferenceUnexpected, Path: "data/new name.txt"},
			},
		})
	})

	t.Run("wrong Oxum without path differences", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/file.txt", "ok"))
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 99.1\n")
		writeBagFile(t, path, "data/file.txt", "ok")
		assert.DeepEqual(t, requireIncomplete(t, v.CheckCompleteness(path)).Differences, &bagit.FileDifferences{
			Examples: []bagit.PathDifference{},
		})
	})

	t.Run("missing referenced tag file", func(t *testing.T) {
		path := completenessBag(t, "1.0", "")
		writeBagFile(t, path, "notes.txt", "unlisted tag file")
		assert.NilError(t, v.CheckCompleteness(path))
		writeBagFile(t, path, "tagmanifest-sha256.txt", fileManifest("other.txt", "missing"))
		assert.DeepEqual(t, requireIncomplete(t, v.CheckCompleteness(path)).Differences, &bagit.FileDifferences{
			Missing:  1,
			Examples: []bagit.PathDifference{{Kind: bagit.DifferenceMissing, Path: "other.txt"}},
		})
	})

	t.Run("comparison blocked after an Oxum failure", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("requires filesystem permissions enforced by chmod")
		}
		path := completenessBag(t, "1.0", "")
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 1.1\n")
		writeBagFile(t, path, "tagmanifest-sha256.txt", fileManifest("tags/required.txt", "x"))
		writeBagFile(t, path, "tags/required.txt", "x")
		tags := filepath.Join(path, "tags")
		assert.NilError(t, os.Chmod(tags, 0))
		t.Cleanup(func() { assert.NilError(t, os.Chmod(tags, 0o700)) })
		if _, err := os.Stat(filepath.Join(tags, "required.txt")); err == nil {
			t.Skip("test process can traverse the directory despite its permissions")
		} else {
			assert.ErrorIs(t, err, os.ErrPermission)
		}
		invalid := requireIncomplete(t, v.CheckCompleteness(path))
		assert.ErrorContains(t, invalid, "Payload-Oxum validation failed")
		assert.Assert(t, invalid.Differences == nil)
	})

	t.Run("Unicode normalization", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/café.txt", "ok"))
		writeBagFile(t, path, "data/cafe\u0301.txt", "ok")
		assert.NilError(t, v.CheckCompleteness(path))
		assert.NilError(t, os.Remove(filepath.Join(path, "data", "cafe\u0301.txt")))
		assert.DeepEqual(t, requireIncomplete(t, v.CheckCompleteness(path)).Differences.Examples,
			[]bagit.PathDifference{{Kind: bagit.DifferenceMissing, Path: "data/café.txt"}})
	})

	t.Run("decomposed missing tag file", func(t *testing.T) {
		const filename = "cafe\u0301.txt"
		path := completenessBag(t, "1.0", "")
		writeBagFile(t, path, "tagmanifest-sha256.txt", fileManifest(filename, "x"))
		want := &bagit.FileDifferences{Missing: 1, Examples: []bagit.PathDifference{
			{Kind: bagit.DifferenceMissing, Path: filename},
		}}
		assert.DeepEqual(t, requireIncomplete(t, v.CheckCompleteness(path)).Differences, want)
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 1.1\n")
		assert.DeepEqual(t, requireIncomplete(t, v.CheckCompleteness(path)).Differences, want)
	})

	assert.NilError(t, v.CheckCompleteness("internal/testdata/valid-bag"))
}

func TestCompletenessChecksManifestMembership(t *testing.T) {
	b := setUp(t)

	t.Run("tag manifest lists payload", func(t *testing.T) {
		path := completenessBag(t, "1.0", "")
		writeBagFile(t, path, "data/file.txt", "x")
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 1.1\n")
		writeBagFile(t, path, "tagmanifest-sha256.txt", fileManifest("data/file.txt", "x"))
		assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "Tag manifest lists a payload path")
	})

	t.Run("payload manifest lists tag file", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("notes.txt", "x"))
		writeBagFile(t, path, "notes.txt", "x")
		assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "Payload manifest lists a tag path")
	})

	t.Run("each BagIt 1.0 payload manifest must cover the payload", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/file.txt", "x"))
		writeBagFile(t, path, "data/file.txt", "x")
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 1.1\n")
		writeBagFile(t, path, "manifest-sha512.txt", "")
		assert.DeepEqual(t, requireIncomplete(t, b.CheckCompleteness(path)).Differences, &bagit.FileDifferences{
			Unexpected: 1,
			Examples:   []bagit.PathDifference{{Kind: bagit.DifferenceUnexpected, Path: "data/file.txt"}},
		})

		writeBagFile(t, path, "bagit.txt", "BagIt-Version: 0.97\nTag-File-Character-Encoding: UTF-8\n")
		assert.NilError(t, b.CheckCompleteness(path))
	})

	t.Run("manifest with an unrecognized algorithm", func(t *testing.T) {
		path := completenessBag(t, "1.0", "")
		writeBagFile(t, path, "manifest-blake3.txt", strings.Repeat("a", 64)+"  data/missing.txt\n")
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 0.0\n")
		assert.DeepEqual(t, requireIncomplete(t, b.CheckCompleteness(path)).Differences, &bagit.FileDifferences{
			Missing:  1,
			Examples: []bagit.PathDifference{{Kind: bagit.DifferenceMissing, Path: "data/missing.txt"}},
		})
	})
}

func TestCompletenessRejectsUnusablePayloadPaths(t *testing.T) {
	b := setUp(t)

	t.Run("dangling symlink", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/link.txt", "x"))
		err := os.Symlink(filepath.Join("..", "target.txt"), filepath.Join(path, "data", "link.txt"))
		if runtime.GOOS == "windows" && err != nil {
			t.Skipf("cannot create symlinks on this system: %v", err)
		}
		assert.NilError(t, err)
		assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "Dangling payload symlink")
		writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: 1.1\n")
		assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "Dangling payload symlink")
		writeBagFile(t, path, "target.txt", "x")
		assert.NilError(t, b.CheckCompleteness(path))
	})

	t.Run("Unicode collision in manifest", func(t *testing.T) {
		manifest := fileManifest("data/café.txt", "x") + fileManifest("data/cafe\u0301.txt", "x")
		path := completenessBag(t, "1.0", manifest)
		writeBagFile(t, path, "data/café.txt", "x")
		assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "Unicode normalization conflict")
	})

	t.Run("Unicode collision on disk", func(t *testing.T) {
		path := completenessBag(t, "1.0", fileManifest("data/café.txt", "x"))
		writeBagFile(t, path, "data/café.txt", "x")
		f, err := os.OpenFile(filepath.Join(path, "data", "cafe\u0301.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			t.Skip("filesystem treats the two spellings as the same name")
		}
		assert.NilError(t, err)
		assert.NilError(t, f.Close())
		assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "Unicode normalization conflict")
	})
}

func TestCompletenessBoundsAndOrdersExamples(t *testing.T) {
	b := setUp(t)
	var manifest strings.Builder
	for i := 11; i >= 0; i-- {
		fmt.Fprint(&manifest, fileManifest(fmt.Sprintf("data/missing-%02d.txt", i), "x"))
	}
	path := completenessBag(t, "1.0", manifest.String())
	for i := 6; i >= 0; i-- {
		writeBagFile(t, path, fmt.Sprintf("data/extra-%02d.txt", i), "x")
	}
	want := &bagit.FileDifferences{Missing: 12, Unexpected: 7}
	for i := range 5 {
		want.Examples = append(want.Examples,
			bagit.PathDifference{Kind: bagit.DifferenceMissing, Path: fmt.Sprintf("data/missing-%02d.txt", i)},
			bagit.PathDifference{Kind: bagit.DifferenceUnexpected, Path: fmt.Sprintf("data/extra-%02d.txt", i)},
		)
	}
	for range 3 {
		assert.DeepEqual(t, requireIncomplete(t, b.CheckCompleteness(path)).Differences, want)
	}

	// JSON escapes this path to more than the entire reply budget.
	longDir := "data/a/" + strings.Repeat(strings.Repeat("😀", 50)+"/", 35)
	manifest.Reset()
	for i := range 11 {
		manifest.WriteString(fileManifest(fmt.Sprintf("%sfile-%02d.txt", longDir, i), "x"))
	}
	manifest.WriteString(fileManifest("data/control%0Aline.txt", "x"))
	if runtime.GOOS != "windows" {
		manifest.WriteString(fileManifest(`data/literal\backslash.txt`, "x"))
	}
	path = completenessBag(t, "1.0", manifest.String())
	err := requireIncomplete(t, b.CheckCompleteness(path))
	want = &bagit.FileDifferences{
		Missing:  12,
		Examples: []bagit.PathDifference{{Kind: bagit.DifferenceMissing, Path: "data/control\nline.txt"}},
	}
	if runtime.GOOS != "windows" {
		want.Missing++
		want.Examples = append(want.Examples, bagit.PathDifference{Kind: bagit.DifferenceMissing, Path: `data/literal\backslash.txt`})
	}
	assert.DeepEqual(t, err.Differences, want)
	assert.Assert(t, !strings.ContainsRune(err.Error(), '\n'))
	assert.ErrorContains(t, err, `data/control\nline.txt`)
}

func TestCompletenessDistinguishesFormatAndIOErrors(t *testing.T) {
	b := setUp(t)
	for _, test := range []struct{ filename, summary string }{
		{"bagit.txt", "Expected bagit.txt"},
		{"data", "Expected data directory"},
		{"manifest-sha256.txt", "No manifest files found"},
	} {
		t.Run(test.filename, func(t *testing.T) {
			path := completenessBag(t, "1.0", "")
			assert.NilError(t, os.Remove(filepath.Join(path, test.filename)))
			err := requireIncomplete(t, b.CheckCompleteness(path))
			assert.Assert(t, strings.Contains(err.Summary, test.summary))
			assert.Assert(t, err.Differences == nil)
		})
	}
	path := completenessBag(t, "1.0", "")
	writeBagFile(t, path, "bag-info.txt", "Payload-Oxum: malformed\n")
	assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "Malformed Payload-Oxum")
	writeBagFile(t, path, "fetch.txt", "malformed\n")
	assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "Malformed fetch.txt")

	path = completenessBag(t, "1.0", fileManifest("data/invalid\x00name.txt", "x"))
	assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "is unsafe")
	path = completenessBag(t, "1.0", "")
	writeBagFile(t, path, "bagit.txt", "BagIt-Version: 1.0\nTag-File-Character-Encoding: base64\n")
	assert.ErrorContains(t, requireIncomplete(t, b.CheckCompleteness(path)), "is not a text encoding")

	assertExecutionError(t, b.CheckCompleteness(filepath.Join(path, "absent")))
	assertExecutionError(t, b.CheckCompleteness(filepath.Join(path, "bagit.txt")))
	// JSON encoding would otherwise substitute U+FFFD and check a different bag.
	parent := t.TempDir()
	writeBagFile(t, parent, "stage-�/bagit.txt", "BagIt-Version: 1.0\nTag-File-Character-Encoding: UTF-8\n")
	writeBagFile(t, parent, "stage-�/manifest-sha256.txt", "")
	assert.NilError(t, os.Mkdir(filepath.Join(parent, "stage-�", "data"), 0o700))
	assert.NilError(t, b.CheckCompleteness(filepath.Join(parent, "stage-�")))
	assertExecutionError(t, b.CheckCompleteness(filepath.Join(parent, "stage-\xff")))

	for _, component := range []string{".", "manifest-sha256.txt", "data/nested"} {
		t.Run("unreadable "+component, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("requires filesystem permissions enforced by chmod")
			}
			path := completenessBag(t, "1.0", fileManifest("data/nested/file.txt", "x"))
			writeBagFile(t, path, "data/nested/file.txt", "x")
			unreadable := filepath.Join(path, component)
			assert.NilError(t, os.Chmod(unreadable, 0))
			t.Cleanup(func() { assert.NilError(t, os.Chmod(unreadable, 0o700)) })
			if f, err := os.Open(unreadable); err == nil {
				assert.NilError(t, f.Close())
				t.Skip("test process can read the path despite its permissions")
			} else {
				assert.ErrorIs(t, err, os.ErrPermission)
			}
			assertExecutionError(t, b.CheckCompleteness(path))
		})
	}
	assert.NilError(t, b.CheckCompleteness("internal/testdata/valid-bag"))
}

func requireIncomplete(t *testing.T, err error) *bagit.CompletenessError {
	t.Helper()
	assert.ErrorIs(t, err, bagit.ErrInvalid)
	var detail *bagit.CompletenessError
	assert.Assert(t, errors.As(err, &detail))
	assert.Assert(t, strings.HasPrefix(err.Error(), "invalid: "))
	return detail
}

func assertExecutionError(t *testing.T, err error) {
	t.Helper()
	assert.Assert(t, err != nil)
	assert.Assert(t, !errors.Is(err, bagit.ErrInvalid), "%v", err)
	var detail *bagit.CompletenessError
	assert.Assert(t, !errors.As(err, &detail), "%v", err)
}

func completenessBag(t *testing.T, version, manifest string) string {
	t.Helper()
	path := t.TempDir()
	assert.NilError(t, os.Mkdir(filepath.Join(path, "data"), 0o700))
	writeBagFile(t, path, "bagit.txt", "BagIt-Version: "+version+"\nTag-File-Character-Encoding: UTF-8\n")
	writeBagFile(t, path, "manifest-sha256.txt", manifest)
	return path
}

func writeBagFile(t *testing.T, bagPath, name, contents string) {
	t.Helper()
	path := filepath.Join(bagPath, filepath.FromSlash(name))
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	assert.NilError(t, os.WriteFile(path, []byte(contents), 0o600))
}

func fileManifest(path, contents string) string {
	return fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(contents)), path)
}
