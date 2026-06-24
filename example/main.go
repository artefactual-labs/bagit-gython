package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/artefactual-labs/bagit-gython"
)

const usage = `Usage:
	$ example -validate=/tmp/bag -pool-size=2`

func main() {
	os.Exit(run())
}

func run() int {
	flag.Usage = func() { fmt.Fprintf(os.Stderr, "%s\n", usage) }

	if len(os.Args) == 1 {
		flag.Usage()
		return 1
	}

	var validate string
	var poolSize int
	flag.StringVar(&validate, "validate", "", "path of bag to validate")
	flag.IntVar(&poolSize, "pool-size", 1, "number of concurrent validators")
	flag.Parse()

	if validate == "" {
		flag.Usage()
		return 1
	}

	validator, err := bagit.NewValidator(bagit.WithPoolSize(poolSize))
	if err != nil {
		fmt.Println(err)
		return 1
	}
	defer func() {
		if err := validator.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Cleanup failed: %v\n", err)
		}
	}()

	if err := validator.Validate(validate); err != nil {
		fmt.Printf("Validation failed: %v\n", err)
		return 1
	}

	fmt.Println("Valid!")
	return 0
}
