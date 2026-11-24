package bagit

import (
	"bufio"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestDecodeCompletenessResponse(t *testing.T) {
	assert.NilError(t, decodeCheckResponse(checkCompletenessCommand, []byte(`{"status":"complete","future":true}`)))
	for _, test := range []struct{ name, reply, want string }{
		{"old runner", `{"err":"'check_completeness' is not a valid command, use: validate, make, exit","type":"UnknownCommandError"}`, "incompatible Python runner"},
		{"old success", `{"valid":true}`, "missing status"},
		{"unknown status", `{"status":"valid"}`, "unknown status"},
		{"checksum result", `{"status":"checksum_mismatch","message":"Mismatch"}`, "unknown status"},
		{"truncated JSON", `{"status":"complete"`, "decode completeness response"},
		{"wrong status type", `{"status":true}`, "decode completeness response"},
		{"invalid without message", `{"status":"invalid"}`, "requires a message"},
		{"wrong message type", `{"status":"invalid","message":5}`, "requires a message"},
		{"success with failure details", `{"status":"complete","differences":null}`, "complete status with failure details"},
		{"execution failure", `{"status":"error","message":"Could not read staged directory"}`, "check completeness: Could not read staged directory"},
		{"negative count", `{"status":"invalid","message":"Incomplete","differences":{"missing":-1,"unexpected":0,"examples":[]}}`, "invalid path difference counts"},
		{"missing count", `{"status":"invalid","message":"Incomplete","differences":{"unexpected":0,"examples":[]}}`, "require both counts"},
		{"noninteger count", `{"status":"invalid","message":"Incomplete","differences":{"missing":1.1,"unexpected":0,"examples":[]}}`, "decode completeness response"},
		{"null examples", `{"status":"invalid","message":"Incomplete","differences":{"missing":1,"unexpected":0,"examples":null}}`, "require both counts"},
		{"too many missing examples", `{"status":"invalid","message":"Incomplete","differences":{"missing":1,"unexpected":0,"examples":[{"kind":"missing","path":"x"},{"kind":"missing","path":"y"}]}}`, "inconsistent path difference"},
		{"unknown kind", `{"status":"invalid","message":"Incomplete","differences":{"missing":1,"unexpected":0,"examples":[{"kind":"extra","path":"x"}]}}`, "inconsistent path difference"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := decodeCheckResponse(checkCompletenessCommand, []byte(test.reply))
			assert.ErrorContains(t, err, test.want)
			assert.Assert(t, !errors.Is(err, ErrInvalid))
		})
	}
}

func TestCompletenessReplyFraming(t *testing.T) {
	replies := strings.Repeat("x", maxCompletenessReplyBytes) + "\n" + `{"status":"complete"}` + "\r\n"
	reader := bufio.NewReader(strings.NewReader(replies))
	_, err := readBoundedReply(reader, maxCompletenessReplyBytes)
	assert.ErrorContains(t, err, "reply exceeds")
	blob, err := readBoundedReply(reader, maxCompletenessReplyBytes)
	assert.NilError(t, err)
	assert.NilError(t, decodeCheckResponse(checkCompletenessCommand, blob))

	_, err = readBoundedReply(bufio.NewReader(strings.NewReader(`{"status":"complete"}`)), maxCompletenessReplyBytes)
	assert.ErrorContains(t, err, "EOF")
}

func TestStrictReplyFramingPreservesLongChecksumPath(t *testing.T) {
	const prefix = "sha512 checksum mismatch for '"
	const filename = "file.txt"
	const depth = 150
	directory := strings.Repeat("界", 200) + `\\`
	escapedDirectory := strings.Repeat(`\u754c`, 200) + `\\\\`
	message := prefix + strings.Repeat(escapedDirectory, depth) + filename + "'"
	mismatch := `{"status":"checksum_mismatch","message":"` + message + `"}`
	invalid := `{"status":"invalid","message":"` + message + `"}`
	replies := strings.Repeat("x", maxStrictReplyBytes) + "\n" + mismatch + "\r\n" + invalid + "\r\n"
	reader := bufio.NewReader(strings.NewReader(replies))
	_, err := readBoundedReply(reader, maxStrictReplyBytes)
	assert.ErrorContains(t, err, "reply exceeds")

	blob, err := readBoundedReply(reader, maxStrictReplyBytes)
	assert.NilError(t, err)
	err = decodeCheckResponse(validateStrictCommand, blob)
	assert.ErrorIs(t, err, ErrInvalid)
	want := "invalid: " + prefix + strings.Repeat(directory, depth) + filename + "'"
	if got := err.Error(); got != want {
		t.Fatalf("checksum mismatch altered the path, got %d bytes, want %d", len(got), len(want))
	}

	blob, err = readBoundedReply(reader, maxStrictReplyBytes)
	assert.NilError(t, err)
	err = decodeCheckResponse(validateStrictCommand, blob)
	assert.ErrorContains(t, err, "reply exceeds 16384 bytes")
	assert.Assert(t, !errors.Is(err, ErrInvalid))
}

func TestCompletenessRunnerStartupFailureReleasesSlot(t *testing.T) {
	v, err := NewValidator(WithCacheDir(t.TempDir()))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, v.Close()) })
	b := v.pool[0]
	entryPoint := b.runner.entryPoint
	b.runner.entryPoint = filepath.Join(t.TempDir(), "missing-runner.py")

	err = v.CheckCompleteness("internal/testdata/valid-bag")
	assert.Assert(t, err != nil && !errors.Is(err, ErrInvalid), "%v", err)
	// The pipe can report EOF before the process monitor records the exit.
	assert.Assert(t, wait(&b.runner.wg, 5*time.Second))
	b.runner.entryPoint = entryPoint
	assert.NilError(t, v.CheckCompleteness("internal/testdata/valid-bag"))
}

func TestCompletenessContextWaitsForPool(t *testing.T) {
	v, err := NewValidator(WithDeferredRuntime(), WithCacheDir(t.TempDir()))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, v.Close()) })
	assert.Assert(t, v.sem.TryAcquire(1))
	defer v.sem.Release(1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, v.CheckCompletenessContext(ctx, "internal/testdata/valid-bag"), context.DeadlineExceeded)
	assert.ErrorIs(t, v.TryValidate("internal/testdata/valid-bag"), ErrBusy)
	assert.Assert(t, v.runtime == nil)
}

func TestCompletenessKeepsAcquiredRunnerThroughCancellationAndClose(t *testing.T) {
	const path = "internal/testdata/valid-bag"
	v, err := NewValidator(WithCacheDir(t.TempDir()))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, v.Close()) })
	assert.NilError(t, v.CheckCompleteness(path))

	r := v.pool[0].runner
	gate := &gatedRunnerInput{WriteCloser: r.stdin, started: make(chan struct{}), resume: make(chan struct{})}
	r.stdin = gate
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate.resume) }) }
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checked := make(chan error, 1)
	go func() { checked <- v.CheckCompletenessContext(ctx, path) }()
	select {
	case <-gate.started:
	case err := <-checked:
		t.Fatalf("check completed before sending the request: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("check did not reach the runner")
	}
	cancel()
	assert.ErrorIs(t, v.TryValidate(path), ErrBusy)

	closed := make(chan error, 1)
	go func() { closed <- v.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before the runner finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	assert.NilError(t, <-checked)
	assert.NilError(t, <-closed)
	assert.ErrorIs(t, v.CheckCompleteness(path), ErrClosed)
}

type gatedRunnerInput struct {
	io.WriteCloser
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (w *gatedRunnerInput) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.started)
		<-w.resume
	})
	return w.WriteCloser.Write(p)
}
