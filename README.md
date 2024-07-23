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

Check out [`example`], a small program that imports bagit-python to validate a
bag:

    $ go run . -validate /tmp/invalid-bag/
    Validation failed: invalid: Payload-Oxum validation failed. Expected 1 files and 0 bytes but found 2 files and 0 bytesexit status 1

    $ go run . -validate /tmp/valid-bag/
    Valid!

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
