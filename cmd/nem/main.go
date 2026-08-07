package main

import (
	"fmt"
	"os"
)

func main() {
	root := newRoot()
	if err := root.Execute(); err != nil {
		if ranHook && console != nil {
			console.Error(err, hintFor(err))
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(2) // cobra usage error: flags/args never validated
	}
}
