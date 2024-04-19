# bagit-gython

[![Go Reference](https://pkg.go.dev/badge/github.com/artefactual-labs/bagit-gython.svg)](https://pkg.go.dev/artefactual-labs/bagit-gython)

bagit-gython is an experimental library that wraps bagit-python using an
embedded Python 3.12 interpreter.

It depends on [go-embed-python], a CGO-free library that provides an embedded
distribution of Python compatible with a number of architecture and operative
systems.

## Usage

Check out [`example`], a small program that imports bagit-python to validate a
bag:

    $ go run ./example/ -validate /tmp/invalid-bag/
    Validation failed: invalid: Payload-Oxum validation failed. Expected 1 files and 0 bytes but found 2 files and 0 bytesexit status 1

    $ go run ./example/ -validate /tmp/valid-bag/
    Valid!

## Supported architectures

- darwin-amd64
- darwin-arm64
- linux-amd64
- linux-arm64
- windows-amd64

## Acknowledgement

* bagit-python project: https://github.com/LibraryOfCongress/bagit-python
* go-embed-python project: https://github.com/kluctl/go-embed-python

## License

Apache 2.0. See [LICENSE](LICENSE).

[go-embed-python]: https://github.com/kluctl/go-embed-python
[`example`]: ./example/main.go
