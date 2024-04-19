package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/artefactual-labs/bagit-gython"
)

const usage = `Usage:
	$ example -validate=/tmp/bag`

func main() {
	flag.Usage = func() { fmt.Fprintf(os.Stderr, "%s\n", usage) }

	if len(os.Args) == 1 {
		flag.Usage()
		os.Exit(1)
	}

	var validate string
	flag.StringVar(&validate, "validate", "", "path of bag to validate")
	flag.Parse()

	if validate == "" {
		flag.Usage()
		os.Exit(1)
	}

	b, err := bagit.NewBagIt()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if err := b.Validate(validate); err != nil {
		fmt.Printf("Validation failed: %v", err)
		os.Exit(1)
	}

	fmt.Println("Valid!")
}
