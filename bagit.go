package bagit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/artefactual-labs/bagit-gython/internal/dist/data"
	"github.com/artefactual-labs/bagit-gython/internal/runner"
	"github.com/kluctl/go-embed-python/embed_util"
	"github.com/kluctl/go-embed-python/python"
)

// ErrInvalid indicates that bag validation failed. If there is a validation
// error message, ErrInvalid will be wrapped so make sure to use
// `errors.Is(err, ErrInvalid)` to test equivalency.
var ErrInvalid = errors.New("invalid")

// BagIt is an abstraction to work with BagIt packages that embeds Python and
// the bagit-python.
type BagIt struct {
	tmpDir      string                    // Top-level container for embedded files.
	embedPython *python.EmbeddedPython    // Python files.
	embedBagit  *embed_util.EmbeddedFiles // bagit-python library files.
	embedRunner *embed_util.EmbeddedFiles // bagit-python wrapper files (runner).
	runner      *pyRunner
}

// NewBagIt creates and initializes a new BagIt instance. This constructor is
// computationally expensive as it sets up an embedded Python environment and
// extracts necessary libraries. It's recommended to create a single instance
// and share it across your application to avoid repeatedly installing Python.
func NewBagIt() (*BagIt, error) {
	b := &BagIt{}

	var err error
	b.tmpDir, err = os.MkdirTemp("", "bagit-gython-*")
	if err != nil {
		return nil, fmt.Errorf("make tmpDir: %v", err)
	}

	b.embedPython, err = python.NewEmbeddedPythonWithTmpDir(filepath.Join(b.tmpDir, "python"), true)
	if err != nil {
		return nil, fmt.Errorf("embed python: %v", err)
	}

	b.embedBagit, err = embed_util.NewEmbeddedFilesWithTmpDir(data.Data, filepath.Join(b.tmpDir, "bagit-lib"), true)
	if err != nil {
		return nil, fmt.Errorf("embed bagit: %v", err)
	}
	b.embedPython.AddPythonPath(b.embedBagit.GetExtractedPath())

	b.embedRunner, err = embed_util.NewEmbeddedFilesWithTmpDir(runner.Source, filepath.Join(b.tmpDir, "bagit-runner"), true)
	if err != nil {
		return nil, fmt.Errorf("embed runner: %v", err)
	}

	b.runner = createRunner(
		b.embedPython,
		filepath.Join(b.embedRunner.GetExtractedPath(), "main.py"),
	)

	return b, nil
}

type validateRequest struct {
	Path string `json:"path"`
}

type validateResponse struct {
	Valid bool   `json:"valid"`
	Err   string `json:"err"`
}

func (b *BagIt) Validate(path string) error {
	blob, err := b.runner.send("validate", &validateRequest{
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
	blob, err := b.runner.send("make", &makeRequest{
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

func (b *BagIt) Cleanup() error {
	var e error

	if err := b.runner.stop(); err != nil {
		e = errors.Join(e, fmt.Errorf("stop runner: %v", err))
	}

	if err := b.embedRunner.Cleanup(); err != nil {
		e = errors.Join(e, fmt.Errorf("clean up runner: %v", err))
	}

	if err := b.embedBagit.Cleanup(); err != nil {
		e = errors.Join(e, fmt.Errorf("clean up bagit: %v", err))
	}

	if err := b.embedPython.Cleanup(); err != nil {
		e = errors.Join(e, fmt.Errorf("clean up python: %v", err))
	}

	if err := os.RemoveAll(b.tmpDir); err != nil {
		e = errors.Join(e, fmt.Errorf("clean up tmpDir: %v", err))
	}

	return e
}
