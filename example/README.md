# bagit-gython example

This example shows the two public API layers:

- `validator`: the pooled, concurrency-safe `Validator` API.
- `bagit`: the lower-level single-runner `BagIt` API.

Run it from this directory:

```sh
go run . -api validator -validate ../internal/testdata/valid-bag -pool-size 2
```

Use a dedicated runtime cache path for the pooled validator:

```sh
go run . -api validator -validate ../internal/testdata/valid-bag -cache-dir /tmp/bagit-cache
```

```sh
go run . -api bagit -validate ../internal/testdata/valid-bag
```

Use an absolute path when validating a bag outside the repository:

```sh
go run . -api validator -validate /tmp/valid-bag -pool-size 2
```
