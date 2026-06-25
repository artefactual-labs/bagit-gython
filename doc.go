// Package bagit wraps bagit-python with an embedded Python runtime.
//
// The package has two levels of API: Validator for shared validation services
// and BagIt for direct access to a single embedded runner.
//
// Validator is the preferred API for long-running processes. It owns a bounded
// pool of BagIt runners, is safe for concurrent use, and avoids creating a new
// embedded Python extraction for each validation:
//
//	validator, err := bagit.NewValidator(bagit.WithPoolSize(4))
//	if err != nil {
//		return err
//	}
//	defer validator.Close()
//
//	if err := validator.Validate("/path/to/bag"); err != nil {
//		return err
//	}
//
// Validator.Validate waits when all runners are busy. Validator.ValidateContext
// lets callers cancel that wait, and Validator.TryValidate returns ErrBusy
// immediately when no runner is available.
//
// By default, Validator caches extracted runtime files below the user's cache
// directory so later validators and process starts can reuse them. Use
// WithCacheDir to choose the cache location, or WithCacheDir("") to use a
// temporary extraction that Close removes.
//
// BagIt is the lower-level single-runner type. It is useful for short-lived
// commands or for callers that want to manage runner lifetimes themselves. A
// BagIt instance is not safe for concurrent operations: sharing one while it is
// processing another command can return ErrBusy. Use one BagIt per concurrent
// caller, serialize access yourself, or use Validator.
//
// Both APIs return ErrInvalid for validation failures. Release resources with
// Validator.Close or BagIt.Cleanup when the runner is no longer needed.
package bagit
