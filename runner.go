package bagit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/kluctl/go-embed-python/python"
)

var ErrBusy = errors.New("runner is busy")

// pyRunner manages the execution of the Python script wrapping bagit-python.
// It ensures that only one command is executed at a time and provides
// mechanisms to send commands and receive responses.
type pyRunner struct {
	py           *python.EmbeddedPython
	entryPoint   string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stdoutReader *bufio.Reader
	mu           sync.Mutex
}

func createRunner(py *python.EmbeddedPython, entryPoint string) *pyRunner {
	return &pyRunner{
		py:         py,
		entryPoint: entryPoint,
	}
}

// exited determines whether the process has exited.
func (r *pyRunner) exited() bool {
	if r.cmd == nil || r.cmd.ProcessState == nil {
		return true
	}

	return r.cmd.ProcessState.Exited()
}

// ensure that the process is running.
func (r *pyRunner) ensure() error {
	if !r.exited() {
		return nil
	}

	var err error
	r.cmd, err = r.py.PythonCmd(r.entryPoint)
	if err != nil {
		return fmt.Errorf("start runner: %v", err)
	}

	// r.cmd.Stderr = os.Stderr

	r.stdin, err = r.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %v", err)
	}

	r.stdout, err = r.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %v", err)
	}
	r.stdoutReader = bufio.NewReader(r.stdout)

	err = r.cmd.Start()
	if err != nil {
		return fmt.Errorf("start cmd: %v", err)
	}

	return nil
}

type args struct {
	Cmd  string `json:"cmd"`
	Opts any    `json:"opts"`
}

// send a command to the runner.
func (r *pyRunner) send(args args) ([]byte, error) {
	if ok := r.mu.TryLock(); !ok {
		return nil, ErrBusy
	}
	defer r.mu.Unlock()

	if err := r.ensure(); err != nil {
		return nil, err
	}

	blob, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encode args: %v", err)
	}
	blob = append(blob, '\n')

	_, err = r.stdin.Write(blob)
	if err != nil {
		return nil, fmt.Errorf("write blob: %v", err)
	}

	line := bytes.NewBuffer(nil)
	for {
		l, p, err := r.stdoutReader.ReadLine()
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("read line: %v", err)
		}
		line.Write(l)
		if !p {
			break
		}
	}
	if line.Len() < 1 {
		return nil, fmt.Errorf("response not received")
	}

	return line.Bytes(), nil
}

// quit requests the runner to exit gracefully.
func (r *pyRunner) quit() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.exited() {
		return nil
	}

	_, err := r.stdin.Write([]byte(`{"name": "exit"}`))

	return err
}

func (i *pyRunner) stop() error {
	var e error

	if err := i.quit(); err != nil {
		e = errors.Join(e, err)
	}

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
