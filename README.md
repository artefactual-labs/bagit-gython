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

`Validator` owns one shared embedded Python extraction and a bounded pool of
BagIt runners. At most `poolSize` validations run at once; additional calls wait
for a runner instead of creating new Python extractions. By default, the
extracted runtime is cached under the user's cache directory in `bagit-gython`.
With `WithPoolSize(4)`, a process uses one runtime cache root and up to four
runner processes for that validator lifecycle.

Full validation hashes files in parallel within each runner. The pool divides
Go's `GOMAXPROCS` by the configured pool size to budget checksum workers, with
at least one per runner. Each file is read once for all its checksums.

Runtime cache configuration is explicit:

| Configuration | Result |
| --- | --- |
| Omit `WithCacheDir` | Use the default persistent cache, `os.UserCacheDir()/bagit-gython`. |
| `WithCacheDir("")` | Use the default persistent cache, `os.UserCacheDir()/bagit-gython`. |
| `WithCacheDir("/path/to/cache")` | Use that persistent cache directory. |
| `WithTempCacheDir()` | Disable the persistent cache and use a temporary runtime root that `Close` removes. |
| `WithDeferredRuntime()` | Delay runtime extraction and runner pool creation until the first validation request. |

If cache configuration comes from a config file, treat the field as optional:
when it is unset or empty, omit `WithCacheDir` or pass `WithCacheDir("")` to use
the default cache; when it is set to a non-empty path, pass that path. Use
`WithTempCacheDir()` only when the operator explicitly wants a temporary runtime
root. Cached runtime files are content-hash scoped, so changes to the embedded
Python, bagit-python, or runner files extract into new cache directories while
warm starts can reuse existing files.

By default, `NewValidator` extracts the embedded runtime and creates the runner
pool before returning. Use `WithDeferredRuntime()` when startup should avoid
that work until the validator is actually used. The first validation request
performs setup synchronously; concurrent first requests wait behind that setup
and then share the initialized pool. `TryValidate` can also pay this setup cost
after it acquires a runner slot.

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

The [`example`] directory contains a small command-line program that validates a
bag with either API:

    $ cd example
    $ go run . -api validator -validate ../internal/testdata/valid-bag -pool-size 2
    Valid!

    $ go run . -api validator -validate ../internal/testdata/valid-bag -cache-dir /tmp/bagit-cache
    Valid!

    $ go run . -api bagit -validate ../internal/testdata/valid-bag
    Valid!

## Completeness checks

Use `Validate` when you need to verify checksums. Use `CheckCompleteness` on a
`BagIt` or `Validator` to check a staged bag's structure and manifest membership
without reading payload contents or changing files. The check also verifies
`Payload-Oxum` when present. A content change that preserves the filename and
size can pass completeness and fail checksum verification.

Use `CheckCompletenessContext` on a pool when the wait for a runner should
respect cancellation or a deadline. Once a runner is acquired, the check runs to
completion. The same pool can handle both kinds of validation.

`errors.Is(err, bagit.ErrInvalid)` identifies an invalid bag. Use `errors.As` to
read the summary and any missing or unexpected paths. I/O and runner failures
are returned as execution errors.

```go
if err := validator.CheckCompletenessContext(ctx, stagedPath); err != nil {
    var invalid *bagit.CompletenessError
    if errors.As(err, &invalid) && invalid.Differences != nil {
        diff := invalid.Differences
        log.Printf("%d missing, %d unexpected", diff.Missing, diff.Unexpected)
        for _, example := range diff.Examples {
            log.Printf("%s %q", example.Kind, example.Path)
        }
    }
    return err
}
```

The counts are exact when a comparison is available. A failure includes at most
ten example paths. A missing bag declaration, for example, can prevent a
comparison, in which case `Differences` is `nil`.

For BagIt 1.0, a payload file omitted from any payload manifest is reported as
unexpected. Older bags can list a payload file in any payload manifest.

The runner keeps manifest and filesystem names in memory to compare membership.
Memory use grows with the number of paths and the number of checks running
concurrently. Run the check after staging finishes so the payload stays stable
during the check.

## Creating bags

`BagIt.Make` creates a bag in place with SHA-256 and SHA-512 manifests. Use
`MakeWithOptions` to choose the checksum algorithms for both the payload and tag
manifests. Use lowercase names such as `sha512`, `sha256`, `sha1`, and `md5`.

```go
err := b.MakeWithOptions("/path/to/transfer", bagit.MakeOptions{
    ChecksumAlgorithms: []string{"sha512"},
})
```

Creation moves the directory contents into `data/`. A directory with no payload
files gets an empty payload manifest for each selected algorithm and matching
tag manifests.

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

## Performance and compatibility

These are medians of seven runs on an Apple M4 Max with local APFS, one warm
Python runner, `GOMAXPROCS=4`, and SHA-256 and SHA-512. `Make` starts with a
populated source directory. The Go reference reads the same files and compares
checksums against digests already in memory.

| Payload                               | `Make` | `CheckCompleteness` | `Validate` | Go checksum only |
| ------------------------------------- | -----: | ------------------: | ---------: | ---------------: |
| 20,000 × 128 B across 200 directories | 1.97 s |              729 ms |     1.40 s |           108 ms |
| 1 × 256 MiB                           | 232 ms |             0.60 ms |     231 ms |           234 ms |
| 4 × 64 MiB                            | 233 ms |             0.66 ms |      66 ms |            66 ms |

Full validation was close to the Go checksum reference on the large-file bags.
For checksum verification, bagit-python reads each file once and uses native
hash implementations. It can hash multiple files in parallel during validation.
`Make` hashes files serially and reloads the manifests after writing them. On
the tiny-file bag, a separate profile measured about 650 ms to load manifests in
either check and 1.2 s to reload them in `Make`.

Bags with many tiny files are the strongest case for a Go port because manifest
parsing, path resolution, and other per-file work add up. For now, wrapping
[bagit-python] keeps creation, completeness checks, and validation close to the
behavior Artefactual already relies on in several projects, including how
bagit-python handles legacy bags and filenames.

## Acknowledgement

* bagit-python project: https://github.com/LibraryOfCongress/bagit-python
* go-embed-python project: https://github.com/kluctl/go-embed-python

## License

Apache 2.0. See [LICENSE](LICENSE).


[bagit-python]: https://github.com/LibraryOfCongress/bagit-python
[go-embed-python]: https://github.com/kluctl/go-embed-python
[`example`]: ./example/main.go
[`internal/dist/requirements.txt`]: ./internal/dist/requirements.txt
