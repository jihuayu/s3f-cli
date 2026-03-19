package main

import (
	"flag"
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version information")
	flag.BoolVar(showVersion, "v", false, "print version information")
	flag.Parse()

	if *showVersion {
		fmt.Fprintf(os.Stdout, "s3f version=%s commit=%s date=%s\n", version, commit, date)
		return
	}

	fmt.Fprintln(os.Stdout, "s3f-cli scaffold: use the internal packages to wire a shell runtime and S3 backend.")
}
