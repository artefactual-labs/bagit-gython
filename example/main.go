package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/artefactual-labs/bagit-gython"
)

const usage = `Usage:
		$ example -api=validator -validate=/tmp/bag -pool-size=2
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
	var poolSize int
	flag.StringVar(&api, "api", apiValidator, "API to use: validator or bagit")
	flag.StringVar(&validate, "validate", "", "path of bag to validate")
	flag.IntVar(&poolSize, "pool-size", 1, "number of concurrent validators")
	flag.Parse()

	if validate == "" {
		flag.Usage()
		return 1
	}

	var err error
	switch api {
	case apiValidator:
		err = validateWithPooledValidator(validate, poolSize)
	case apiBagIt:
		err = validateWithSingleRunner(validate)
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

func validateWithPooledValidator(path string, poolSize int) error {
	validator, err := bagit.NewValidator(bagit.WithPoolSize(poolSize))
	if err != nil {
		return err
	}

	validateErr := validator.Validate(path)
	cleanupErr := validator.Close()

	return joinValidationAndCleanup(validateErr, cleanupErr, "clean up validator")
}

func validateWithSingleRunner(path string) error {
	b, err := bagit.NewBagIt()
	if err != nil {
		return err
	}

	validateErr := b.Validate(path)
	cleanupErr := b.Cleanup()

	return joinValidationAndCleanup(validateErr, cleanupErr, "clean up bagit")
}

func joinValidationAndCleanup(validateErr, cleanupErr error, cleanupOp string) error {
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("%s: %v", cleanupOp, cleanupErr)
	}

	return errors.Join(validateErr, cleanupErr)
}
