package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/artefactual-labs/bagit-gython"
)

const usage = `Usage:
		$ example -api=validator -validate=/tmp/bag -pool-size=2
		$ example -api=validator -validate=/tmp/bag -cache-dir=/tmp/bagit-cache
		$ example -api=validator -validate=/tmp/bag -deferred-runtime -timings
		$ example -api=bagit -validate=/tmp/bag`

const (
	apiValidator = "validator"
	apiBagIt     = "bagit"
)

func main() {
	os.Exit(run())
}

func run() int {
	flag.Usage = func() { fmt.Fprintf(os.Stderr, "%s\n", usage) }

	if len(os.Args) == 1 {
		flag.Usage()
		return 1
	}

	var api string
	var validate string
	var cacheDir string
	var deferredRuntime bool
	var timings bool
	var poolSize int
	flag.StringVar(&api, "api", apiValidator, "API to use: validator or bagit")
	flag.StringVar(&validate, "validate", "", "path of bag to validate")
	flag.StringVar(&cacheDir, "cache-dir", "", "validator runtime cache directory")
	flag.BoolVar(&deferredRuntime, "deferred-runtime", false, "defer validator runtime setup until validation")
	flag.BoolVar(&timings, "timings", false, "print constructor, validation, and cleanup timings")
	flag.IntVar(&poolSize, "pool-size", 1, "number of concurrent validators")
	flag.Parse()

	if validate == "" {
		flag.Usage()
		return 1
	}

	var err error
	switch api {
	case apiValidator:
		err = validateWithPooledValidator(validate, poolSize, cacheDir, deferredRuntime, timings)
	case apiBagIt:
		err = validateWithSingleRunner(validate, timings)
	default:
		fmt.Fprintf(os.Stderr, "Unknown API %q\n", api)
		flag.Usage()
		return 1
	}

	if err != nil {
		fmt.Printf("Validation failed: %v\n", err)
		return 1
	}

	fmt.Println("Valid!")
	return 0
}

func validateWithPooledValidator(path string, poolSize int, cacheDir string, deferredRuntime bool, timings bool) error {
	opts := []bagit.ValidatorOption{bagit.WithPoolSize(poolSize)}
	if cacheDir != "" {
		opts = append(opts, bagit.WithCacheDir(cacheDir))
	}
	if deferredRuntime {
		opts = append(opts, bagit.WithDeferredRuntime())
	}

	constructorStart := time.Now()
	validator, err := bagit.NewValidator(opts...)
	constructorDuration := time.Since(constructorStart)
	if err != nil {
		if timings {
			printTimings(timingReport{
				constructor: constructorDuration,
			})
		}
		return err
	}

	validationStart := time.Now()
	validateErr := validator.Validate(path)
	validationDuration := time.Since(validationStart)

	cleanupStart := time.Now()
	cleanupErr := validator.Close()
	cleanupDuration := time.Since(cleanupStart)

	if timings {
		printTimings(timingReport{
			constructor: constructorDuration,
			validation:  validationDuration,
			cleanup:     cleanupDuration,
		})
	}

	return joinValidationAndCleanup(validateErr, cleanupErr, "clean up validator")
}

func validateWithSingleRunner(path string, timings bool) error {
	constructorStart := time.Now()
	b, err := bagit.NewBagIt()
	constructorDuration := time.Since(constructorStart)
	if err != nil {
		if timings {
			printTimings(timingReport{
				constructor: constructorDuration,
			})
		}
		return err
	}

	validationStart := time.Now()
	validateErr := b.Validate(path)
	validationDuration := time.Since(validationStart)

	cleanupStart := time.Now()
	cleanupErr := b.Cleanup()
	cleanupDuration := time.Since(cleanupStart)

	if timings {
		printTimings(timingReport{
			constructor: constructorDuration,
			validation:  validationDuration,
			cleanup:     cleanupDuration,
		})
	}

	return joinValidationAndCleanup(validateErr, cleanupErr, "clean up bagit")
}

type timingReport struct {
	constructor time.Duration
	validation  time.Duration
	cleanup     time.Duration
}

func printTimings(report timingReport) {
	fmt.Fprintln(os.Stderr, "Timings:")
	fmt.Fprintf(os.Stderr, "  constructor: %s\n", report.constructor)
	if report.validation > 0 {
		fmt.Fprintf(os.Stderr, "  validation:  %s\n", report.validation)
	}
	if report.cleanup > 0 {
		fmt.Fprintf(os.Stderr, "  cleanup:     %s\n", report.cleanup)
	}
}

func joinValidationAndCleanup(validateErr, cleanupErr error, cleanupOp string) error {
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("%s: %v", cleanupOp, cleanupErr)
	}

	return errors.Join(validateErr, cleanupErr)
}
