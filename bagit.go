package bagit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

	ep, err := python.NewEmbeddedPythonWithTmpDir(filepath.Join(b.tmpDir, "python"), true)
	if err != nil {
		return nil, fmt.Errorf("embed python: %v", err)
	}
	b.embedPython = ep

	b.embedBagit, err = embed_util.NewEmbeddedFilesWithTmpDir(data.Data, filepath.Join(b.tmpDir, "bagit-lib"), true)
	if err != nil {
		return nil, fmt.Errorf("embed bagit: %v", err)
	}
	b.embedPython.AddPythonPath(b.embedBagit.GetExtractedPath())

	b.embedRunner, err = embed_util.NewEmbeddedFilesWithTmpDir(runner.Source, filepath.Join(b.tmpDir, "bagit-runner"), true)
	if err != nil {
		return nil, fmt.Errorf("embed runner: %v", err)
	}

	return b, nil
}

// create a Python intepreter running the bagit-python wrapper.
func (b *BagIt) create() (*runnerInstance, error) {
	i := &runnerInstance{}

	cmd, err := b.embedPython.PythonCmd(filepath.Join(b.embedRunner.GetExtractedPath(), "main.py"))
	if err != nil {
		return nil, fmt.Errorf("create command: %v", err)
	}
	i.cmd = cmd

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %v", err)
	}
	i.stdin = stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %v", err)
	}
	i.stdout = stdout

	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("cmd: %v", err)
	}

	return i, nil
}

type validateRequest struct {
	Path string `json:"path"`
}

type validateResponse struct {
	Valid bool   `json:"valid"`
	Err   string `json:"err"`
}

func (b *BagIt) Validate(path string) error {
	i, err := b.create()
	if err != nil {
		return fmt.Errorf("run python: %v", err)
	}
	defer i.stop()

	reader := bufio.NewReader(i.stdout)

	if err := i.send(args{
		Cmd: "validate",
		Opts: &validateRequest{
			Path: path,
		},
	}); err != nil {
		return err
	}

	line := bytes.NewBuffer(nil)
	for {
		l, p, err := reader.ReadLine()
		if err != nil && err != io.EOF {
			return fmt.Errorf("read line: %v", err)
		}
		line.Write(l)
		if !p {
			break
		}
	}

	if line.Len() < 1 {
		return fmt.Errorf("response not received")
	}

	r := validateResponse{}
	err = json.Unmarshal(line.Bytes(), &r)
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
	i, err := b.create()
	if err != nil {
		return fmt.Errorf("run python: %v", err)
	}
	defer i.stop()

	reader := bufio.NewReader(i.stdout)

	if err := i.send(args{
		Cmd: "make",
		Opts: &makeRequest{
			Path: path,
		},
	}); err != nil {
		return err
	}

	line := bytes.NewBuffer(nil)
	for {
		l, p, err := reader.ReadLine()
		if err != nil && err != io.EOF {
			return fmt.Errorf("read line: %v", err)
		}
		line.Write(l)
		if !p {
			break
		}
	}

	if line.Len() < 1 {
		return fmt.Errorf("response not received")
	}

	r := makeResponse{}
	err = json.Unmarshal(line.Bytes(), &r)
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

type args struct {
	Cmd  string `json:"cmd"`
	Opts any    `json:"opts"`
}

// runnerInstance is an instance of a Python interpreter executing the
// bagit-python runner.
type runnerInstance struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

// send a command to the runner.
func (i *runnerInstance) send(args args) error {
	blob, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("encode args: %v", err)
	}
	blob = append(blob, '\n')

	_, err = i.stdin.Write(blob)
	if err != nil {
		return fmt.Errorf("write blob: %v", err)
	}

	return nil
}

func (i *runnerInstance) stop() error {
	var e error

	if err := i.stdin.Close(); err != nil {
		e = errors.Join(e, err)
	}

	if err := i.stdout.Close(); err != nil {
		e = errors.Join(e, err)
	}

	if err := i.cmd.Process.Kill(); err != nil {
		e = errors.Join(e, err)
	}

	if _, err := i.cmd.Process.Wait(); err != nil {
		e = errors.Join(e, err)
	}

	return e
}
