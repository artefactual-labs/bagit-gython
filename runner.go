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
	"sync/atomic"

	"github.com/kluctl/go-embed-python/python"
)

var ErrBusy = errors.New("runner is busy")

// pyRunner manages the execution of the Python script wrapping bagit-python.
// It ensures that only one command is executed at a time and provides
// mechanisms to send commands and receive responses.
type pyRunner struct {
	py           *python.EmbeddedPython // Instance of EmbeddedPython.
	entryPoint   string                 // Path to the runner wrapper entry point.
	cmd          *exec.Cmd              // Command running Python interpreter.
	running      atomic.Bool            // Tracks whether the command is still running.
	wg           sync.WaitGroup         // Tracks the cmd monitor goroutine.
	stdin        io.WriteCloser         // Standard input stream.
	stdout       io.ReadCloser          // Standard output stream.
	stdoutReader *bufio.Reader          // Standard output stream (buffered reader).
	mu           sync.Mutex             // Prevents sharing the command (see ErrBusy).
}

func createRunner(py *python.EmbeddedPython, entryPoint string) *pyRunner {
	return &pyRunner{
		py:         py,
		entryPoint: entryPoint,
	}
}

// ensure that the process is running.
func (r *pyRunner) ensure() error {
	if r.running.Load() {
		return nil
	}

	var err error
	r.cmd, err = r.py.PythonCmd(r.entryPoint)
	if err != nil {
		return fmt.Errorf("start runner: %v", err)
	}

	// Useful for debugging the Python application.
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

	r.running.Store(true)

	// Monitor the command from a dedicated goroutine.
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		_ = r.cmd.Wait()
		r.running.Store(false)
	}()

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
	if r.running.Load() {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

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

	i.wg.Wait()

	return e
}
