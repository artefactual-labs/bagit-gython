package bagit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/artefactual-labs/bagit-gython/internal/dist/data"
	"github.com/artefactual-labs/bagit-gython/internal/runner"
	"github.com/kluctl/go-embed-python/embed_util"
	"github.com/kluctl/go-embed-python/python"
)

var (
	// ErrInvalid indicates that bag validation failed. If there is a validation
	// error message, ErrInvalid will be wrapped so make sure to use
	// `errors.Is(err, ErrInvalid)` to test equivalency.
	ErrInvalid = errors.New("invalid")

	// ErrBusy is returned when an operation is attempted on BagIt while it is
	// already processing another command. This ensures that only one command is
	// processed at a time, preventing race conditions and ensuring the
	// integrity of the shared resources.
	ErrBusy = errors.New("runner is busy")

	// ErrClosed is returned when an operation is attempted on a closed BagIt or
	// Validator.
	ErrClosed = errors.New("validator is closed")
)

// BagIt is an abstraction to work with BagIt packages that embeds Python and
// the bagit-python.
type BagIt struct {
	runtime     *bagItRuntime
	ownsRuntime bool
	runner      *pyRunner
}

type bagItRuntimeConfig struct {
	cacheDir string
}

type bagItRuntime struct {
	rootDir     string                    // Top-level container for embedded files.
	persistent  bool                      // Keep embedded files after cleanup.
	embedPython *python.EmbeddedPython    // Python files.
	embedBagit  *embed_util.EmbeddedFiles // bagit-python library files.
	embedRunner *embed_util.EmbeddedFiles // bagit-python wrapper files (runner).
}

// NewBagIt creates and initializes a new BagIt instance. This constructor is
// computationally expensive as it sets up an embedded Python environment and
// extracts necessary libraries. Reuse an instance for serial operations when
// possible, but do not use the same BagIt concurrently. Use Validator when a
// shared concurrency-safe validator is needed.
func NewBagIt() (_ *BagIt, err error) {
	runtime, err := newBagItRuntime(bagItRuntimeConfig{})
	if err != nil {
		return nil, err
	}

	return newBagIt(runtime, true), nil
}

func newBagItRuntime(cfg bagItRuntimeConfig) (_ *bagItRuntime, err error) {
	runtime := &bagItRuntime{}
	ok := false
	defer func() {
		if !ok && !runtime.persistent {
			if cleanupErr := runtime.cleanup(); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("clean up failed initialization: %v", cleanupErr))
			}
		}
	}()

	if cfg.cacheDir == "" {
		runtime.rootDir, err = os.MkdirTemp("", "bagit-gython-*")
		if err != nil {
			return nil, fmt.Errorf("make runtime root: %v", err)
		}
	} else {
		runtime.rootDir = cfg.cacheDir
		runtime.persistent = true
		if err := prepareRuntimeCacheDir(runtime.rootDir); err != nil {
			return nil, err
		}
	}

	runtime.embedPython, err = python.NewEmbeddedPythonWithTmpDir(filepath.Join(runtime.rootDir, "python"), true)
	if err != nil {
		return nil, fmt.Errorf("embed python: %v", err)
	}

	runtime.embedBagit, err = embed_util.NewEmbeddedFilesWithTmpDir(data.Data, filepath.Join(runtime.rootDir, "bagit-lib"), true)
	if err != nil {
		return nil, fmt.Errorf("embed bagit: %v", err)
	}
	runtime.embedPython.AddPythonPath(runtime.embedBagit.GetExtractedPath())

	runtime.embedRunner, err = embed_util.NewEmbeddedFilesWithTmpDir(runner.Source, filepath.Join(runtime.rootDir, "bagit-runner"), true)
	if err != nil {
		return nil, fmt.Errorf("embed runner: %v", err)
	}

	ok = true
	return runtime, nil
}

func prepareRuntimeCacheDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("make runtime cache dir: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat runtime cache dir: %v", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("runtime cache path %q is not a directory", path)
	}

	if runtime.GOOS != "windows" && st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf(
			"runtime cache dir %q is unsafe: mode %04o allows group or other writes",
			path,
			st.Mode().Perm(),
		)
	}

	return nil
}

func newBagIt(runtime *bagItRuntime, ownsRuntime bool) *BagIt {
	return &BagIt{
		runtime:     runtime,
		ownsRuntime: ownsRuntime,
		runner: createRunner(
			runtime.embedPython,
			filepath.Join(runtime.embedRunner.GetExtractedPath(), "main.py"),
		),
	}
}

type validateRequest struct {
	Path string `json:"path"`
}

type validateResponse struct {
	Valid bool   `json:"valid"`
	Err   string `json:"err"`
}

func (b *BagIt) Validate(path string) error {
	blob, err := b.send("validate", &validateRequest{
		Path: path,
	})
	if err != nil {
		return err
	}

	r := validateResponse{}
	err = json.Unmarshal(blob, &r)
	if err != nil {
		return fmt.Errorf("decode response: %v", err)
	}
	if r.Err != "" {
		return fmt.Errorf("%w: %s", ErrInvalid, r.Err)
	}
	if !r.Valid {
		return ErrInvalid
	}

	return nil
}

type makeRequest struct {
	Path string `json:"path"`
}

type makeResponse struct {
	Version string `json:"version"`
	Err     string `json:"err"`
}

func (b *BagIt) Make(path string) error {
	blob, err := b.send("make", &makeRequest{
		Path: path,
	})
	if err != nil {
		return err
	}

	r := makeResponse{}
	err = json.Unmarshal(blob, &r)
	if err != nil {
		return fmt.Errorf("decode response: %v", err)
	}
	if r.Err != "" {
		return fmt.Errorf("make: %s", r.Err)
	}

	return nil
}

func (b *BagIt) send(name string, args any) ([]byte, error) {
	if b == nil || b.runner == nil {
		return nil, ErrClosed
	}

	return b.runner.send(name, args)
}

func (b *BagIt) Cleanup() error {
	var e error

	if b.runner != nil {
		if err := b.runner.stop(); err != nil {
			e = errors.Join(e, fmt.Errorf("stop runner: %v", err))
		}
		b.runner = nil
	}

	if b.ownsRuntime && b.runtime != nil {
		if err := b.runtime.cleanup(); err != nil {
			e = errors.Join(e, err)
		}
		b.runtime = nil
	}

	return e
}

func (r *bagItRuntime) cleanup() error {
	if r == nil || r.persistent {
		return nil
	}

	var e error

	if r.embedRunner != nil {
		if err := r.embedRunner.Cleanup(); err != nil {
			e = errors.Join(e, fmt.Errorf("clean up runner: %v", err))
		}
	}

	if r.embedBagit != nil {
		if err := r.embedBagit.Cleanup(); err != nil {
			e = errors.Join(e, fmt.Errorf("clean up bagit: %v", err))
		}
	}

	if r.embedPython != nil {
		if err := r.embedPython.Cleanup(); err != nil {
			e = errors.Join(e, fmt.Errorf("clean up python: %v", err))
		}
	}

	if r.rootDir != "" {
		if err := os.RemoveAll(r.rootDir); err != nil {
			e = errors.Join(e, fmt.Errorf("clean up runtime root: %v", err))
		}
		r.rootDir = ""
	}

	return e
}
