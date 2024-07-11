package bagit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
)

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
