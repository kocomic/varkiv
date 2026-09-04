package main

import (
	"flag"
	"fmt"
	"os"

	"varkiv/internal/releasearchive"
)

func main() {
	var source string
	var prefix string
	var output string
	var format string
	flag.StringVar(&source, "source", "", "new release package directory")
	flag.StringVar(&prefix, "prefix", "", "portable top-level archive directory")
	flag.StringVar(&output, "out", "", "new output archive")
	flag.StringVar(&format, "format", "", "zip or tar.gz")
	flag.Parse()
	if flag.NArg() != 0 || source == "" || prefix == "" || output == "" || format == "" {
		fmt.Fprintln(os.Stderr, "usage: varkiv-release-archive --source DIR --prefix NAME --out NEW_FILE --format zip|tar.gz")
		os.Exit(2)
	}
	if err := releasearchive.Write(source, prefix, output, releasearchive.Format(format)); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("release_archive=created format=%s\n", format)
}
