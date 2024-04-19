# bagit-gython

[![Go Reference](https://pkg.go.dev/badge/github.com/artefactual-labs/bagit-gython.svg)](https://pkg.go.dev/artefactual-labs/bagit-gython)

bagit-gython is an experimental library that wraps bagit-python using an
embedded Python 3.12 interpreter.

It depends on [go-embed-python], a CGO-free library that provides an embedded
distribution of Python compatible with a number of architecture and operative
systems.

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
