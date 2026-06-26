package bagit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sync/semaphore"
)

const (
	defaultValidatorPoolSize     = 1
	defaultValidatorCacheDirName = "bagit-gython"
)

// ValidatorOption configures a Validator.
type ValidatorOption func(*validatorConfig)

type validatorConfig struct {
	poolSize int
	cacheDir string
}

// WithPoolSize sets the number of BagIt runners owned by a Validator.
//
// A larger pool allows more validations to run in parallel, at the cost of
// creating more embedded Python runner processes.
func WithPoolSize(size int) ValidatorOption {
	return func(cfg *validatorConfig) {
		cfg.poolSize = size
	}
}

// WithCacheDir sets the directory used to cache embedded runtime files.
//
// Validators use os.UserCacheDir()/bagit-gython by default. The cache stores
// content-hash-scoped embedded Python, bagit-python, and runner files so later
// validators can reuse the same extraction. Omit WithCacheDir to use the
// default. Pass a non-empty path to use a different persistent cache directory,
// or pass an empty string to use the default.
func WithCacheDir(path string) ValidatorOption {
	return func(cfg *validatorConfig) {
		if path == "" {
			cfg.cacheDir = defaultValidatorCacheDir()
			return
		}
		cfg.cacheDir = path
	}
}

// WithTempCacheDir disables the persistent runtime cache.
//
// Validators using this option extract embedded runtime files into a temporary
// runtime root that Close removes.
func WithTempCacheDir() ValidatorOption {
	return func(cfg *validatorConfig) {
		cfg.cacheDir = ""
	}
}

// Validator is a bounded pool of BagIt validators sharing one embedded runtime.
//
// It is safe for concurrent use. At most pool size validations are executed at
// the same time; additional callers wait for a runner to become available
// instead of creating new temporary Python extractions.
type Validator struct {
	poolSize int64
	sem      *semaphore.Weighted

	mu      sync.Mutex
	pool    []*BagIt
	idle    []*BagIt
	closed  bool
	runtime *bagItRuntime

	closeOnce sync.Once
	closeErr  error
}

// NewValidator creates a concurrency-safe BagIt validator pool.
//
// The default pool size is 1. Embedded runtime files are cached under the
// user's cache directory by default. Omit WithCacheDir to use that default, use
// WithCacheDir with a non-empty path to choose a different persistent cache
// location, or use WithTempCacheDir to disable persistent caching.
func NewValidator(opts ...ValidatorOption) (*Validator, error) {
	cfg := validatorConfig{
		poolSize: defaultValidatorPoolSize,
		cacheDir: defaultValidatorCacheDir(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.poolSize < 1 {
		return nil, fmt.Errorf("pool size must be greater than zero")
	}

	runtime, err := newBagItRuntime(bagItRuntimeConfig{cacheDir: cfg.cacheDir})
	if err != nil {
		return nil, err
	}

	v := &Validator{
		poolSize: int64(cfg.poolSize),
		sem:      semaphore.NewWeighted(int64(cfg.poolSize)),
		pool:     make([]*BagIt, 0, cfg.poolSize),
		idle:     make([]*BagIt, 0, cfg.poolSize),
		runtime:  runtime,
	}

	for i := 0; i < cfg.poolSize; i++ {
		b := newBagIt(runtime, false)
		v.pool = append(v.pool, b)
		v.idle = append(v.idle, b)
	}

	return v, nil
}

// PoolSize returns the number of BagIt runners owned by v.
func (v *Validator) PoolSize() int {
	if v == nil {
		return 0
	}
	return int(v.poolSize)
}

// Validate validates path with a pooled BagIt runner.
//
// Validate blocks while all runners are busy. Use ValidateContext when the wait
// should respect cancellation or deadlines.
func (v *Validator) Validate(path string) error {
	return v.ValidateContext(context.Background(), path)
}

// ValidateContext validates path with a pooled BagIt runner.
//
// The context controls waiting for an available runner. Once a runner has been
// acquired, the validation runs to completion.
func (v *Validator) ValidateContext(ctx context.Context, path string) error {
	if v == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if err := v.acquire(ctx); err != nil {
		return err
	}

	b, err := v.take()
	if err != nil {
		v.sem.Release(1)
		return err
	}
	defer func() {
		v.put(b)
		v.sem.Release(1)
	}()

	return b.Validate(path)
}

// TryValidate validates path with a pooled BagIt runner if one is immediately
// available.
//
// TryValidate returns ErrBusy instead of waiting when all runners are busy.
func (v *Validator) TryValidate(path string) error {
	if v == nil {
		return ErrClosed
	}

	if err := v.tryAcquire(); err != nil {
		return err
	}

	b, err := v.take()
	if err != nil {
		v.sem.Release(1)
		return err
	}
	defer func() {
		v.put(b)
		v.sem.Release(1)
	}()

	return b.Validate(path)
}

// Close releases all embedded Python resources owned by v.
//
// Close waits for active validations to finish before cleaning up. Calls to
// Validate made after Close starts return ErrClosed.
func (v *Validator) Close() error {
	if v == nil {
		return nil
	}

	v.closeOnce.Do(func() {
		v.closeErr = v.close()
	})

	return v.closeErr
}

// Cleanup is an alias for Close.
func (v *Validator) Cleanup() error {
	return v.Close()
}

func (v *Validator) acquire(ctx context.Context) error {
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return ErrClosed
	}

	if err := v.sem.Acquire(ctx, 1); err != nil {
		return err
	}

	v.mu.Lock()
	closed = v.closed
	v.mu.Unlock()
	if closed {
		v.sem.Release(1)
		return ErrClosed
	}

	return nil
}

func (v *Validator) tryAcquire() error {
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return ErrClosed
	}

	if ok := v.sem.TryAcquire(1); !ok {
		return ErrBusy
	}

	v.mu.Lock()
	closed = v.closed
	v.mu.Unlock()
	if closed {
		v.sem.Release(1)
		return ErrClosed
	}

	return nil
}

func (v *Validator) take() (*BagIt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if len(v.idle) == 0 {
		return nil, fmt.Errorf("validator runner pool is empty")
	}

	i := len(v.idle) - 1
	b := v.idle[i]
	v.idle[i] = nil
	v.idle = v.idle[:i]

	return b, nil
}

func (v *Validator) put(b *BagIt) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.idle = append(v.idle, b)
}

func (v *Validator) close() error {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return nil
	}
	v.closed = true
	v.mu.Unlock()

	if err := v.sem.Acquire(context.Background(), v.poolSize); err != nil {
		return err
	}
	defer v.sem.Release(v.poolSize)

	return v.cleanup()
}

func (v *Validator) cleanup() error {
	v.mu.Lock()
	pool := v.pool
	runtime := v.runtime
	v.pool = nil
	v.idle = nil
	v.runtime = nil
	v.mu.Unlock()

	var e error
	for _, b := range pool {
		if err := b.Cleanup(); err != nil {
			e = errors.Join(e, err)
		}
	}
	if runtime != nil {
		if err := runtime.cleanup(); err != nil {
			e = errors.Join(e, err)
		}
	}

	return e
}

func defaultValidatorCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(dir, defaultValidatorCacheDirName)
}
