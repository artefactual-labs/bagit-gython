# bagit-gython

[![Go Reference](https://pkg.go.dev/badge/github.com/artefactual-labs/bagit-gython.svg)](https://pkg.go.dev/github.com/artefactual-labs/bagit-gython)

bagit-gython is an experimental library that wraps [bagit-python] using an
embedded modern Python interpreter. Its goal is to make Python's battle-tested
implementation available to the Go ecosystem, avoiding the need to reimplement
it from scratch. By leveraging Python's well-tested version, bagit-gython
benefits from the extensive community of users and the robustness of the
existing implementation.

It depends on [go-embed-python], a CGO-free library that provides an embedded
distribution of Python compatible with a number of architecture and operative
systems.

## Installing

Using bagit-python is easy. First, use `go get` to install the latest version
of the library.

    go get -u github.com/artefactual-labs/bagit-gython

Next, include bagit-gython in your application:

```go
import "github.com/artefactual-labs/bagit-gython"
```

## Usage

Check out [`example`], a small program that validates a bag with a pooled
validator:

    $ go run ./example/ -validate /tmp/invalid-bag/
    Validation failed: invalid: Payload-Oxum validation failed. Expected 1 files and 0 bytes but found 2 files and 0 bytes

    $ go run ./example/ -validate /tmp/valid-bag/ -pool-size 2
    Valid!

For long-running applications, create one `Validator` at process startup and
reuse it:

```go
validator, err := bagit.NewValidator(bagit.WithPoolSize(4))
if err != nil {
    return err
}
defer func() {
    if err := validator.Close(); err != nil {
        // Handle cleanup error.
    }
}()

if err := validator.Validate("/tmp/valid-bag"); err != nil {
    return err
}
```

`Validator` owns a bounded pool of embedded BagIt runners. At most `poolSize`
validations run at once; additional calls wait for a runner instead of creating
new temporary Python extractions. With `WithPoolSize(4)`, a process creates at
most four `bagit-gython-*` temporary roots for that validator lifecycle.

Use `ValidateContext` when waiting for an available runner should respect
caller cancellation or deadlines. Use `TryValidate` when the caller should get
`ErrBusy` immediately instead of waiting for a runner.

This is the preferred API for worker processes and Temporal activities. For
example, a Temporal worker can create the validator during startup, pass it to
an activity that accepts a `Validate(path string) error` interface, and close it
during worker shutdown:

```go
validator, err := bagit.NewValidator(bagit.WithPoolSize(4))
if err != nil {
    return err
}
defer func() {
    if err := validator.Close(); err != nil {
        // Handle cleanup error.
    }
}()

tw.RegisterActivityWithOptions(
    bagvalidate.New(validator).Execute,
    activity.RegisterOptions{Name: bagvalidate.Name},
)
```

`BagIt` is still available as a lower-level single-runner API, but it is not
safe for concurrent operations. Prefer `Validator` unless you are deliberately
managing one `BagIt` instance per caller.

## Supported architectures

- darwin-amd64
- darwin-arm64
- linux-amd64
- linux-arm64
- windows-amd64

These are the platform-architecture combinations for which [go-embed-python]
provides compatibility.

## Version of bagit-python

The specific version of bagit-python used by this project is specified in the
[`internal/dist/requirements.txt`] file. Instead of using the latest official
release, we are using a commit from the main branch that includes compatibility
fixes for recent Python releases. This commit has not yet been included in an
official release.

## Acknowledgement

* bagit-python project: https://github.com/LibraryOfCongress/bagit-python
* go-embed-python project: https://github.com/kluctl/go-embed-python

## License

Apache 2.0. See [LICENSE](LICENSE).


[bagit-python]: https://github.com/LibraryOfCongress/bagit-python
[go-embed-python]: https://github.com/kluctl/go-embed-python
[`example`]: ./example/main.go
[`internal/dist/requirements.txt`]: ./internal/dist/requirements.txt
