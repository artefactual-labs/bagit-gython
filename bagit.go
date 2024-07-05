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

// BagIt is an abstraction to work with BagIt packages that embeds Python and
// the bagit-python.
type BagIt struct {
	tmpDir string                    // Top-level container for embedded files.
	ep     *python.EmbeddedPython    // Python files.
	lib    *embed_util.EmbeddedFiles // bagit-python library files.
	runner *embed_util.EmbeddedFiles // bagit-python wrapper files (runner).
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
	b.ep = ep

	b.lib, err = embed_util.NewEmbeddedFilesWithTmpDir(data.Data, filepath.Join(b.tmpDir, "bagit-lib"), true)
	if err != nil {
		return nil, fmt.Errorf("embed bagit: %v", err)
	}
	b.ep.AddPythonPath(b.lib.GetExtractedPath())

	b.runner, err = embed_util.NewEmbeddedFilesWithTmpDir(runner.Source, filepath.Join(b.tmpDir, "bagit-runner"), true)
	if err != nil {
		return nil, fmt.Errorf("embed runner: %v", err)
	}

	return b, nil
}

// create a Python intepreter running the bagit-python wrapper.
func (b *BagIt) create() (_ *runnerInstance, err error) {
	i := &runnerInstance{}

	cmd, err := b.ep.PythonCmd(filepath.Join(b.runner.GetExtractedPath(), "main.py"))
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
		return fmt.Errorf("invalid: %s", r.Err)
	}
	if !r.Valid {
		return fmt.Errorf("invalid: %s", r.Err)
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

	if err := b.runner.Cleanup(); err != nil {
		e = errors.Join(e, fmt.Errorf("clean up runner: %v", err))
	}

	if err := b.lib.Cleanup(); err != nil {
		e = errors.Join(e, fmt.Errorf("clean up bagit: %v", err))
	}

	if err := b.ep.Cleanup(); err != nil {
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
