package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"s3f-cli/internal/update"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			printVersion()
			return
		case "update":
			if err := runUpdate(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "update failed:", err)
				os.Exit(1)
			}
			return
		}
	}

	showVersion := flag.Bool("version", false, "print version information")
	flag.BoolVar(showVersion, "v", false, "print version information")
	flag.Parse()

	if *showVersion {
		printVersion()
		return
	}

	fmt.Fprintln(os.Stdout, "s3f-cli scaffold: use the internal packages to wire a shell runtime and S3 backend.")
}

func printVersion() {
	fmt.Fprintf(os.Stdout, "s3f version=%s commit=%s date=%s\n", version, commit, date)
}

func runUpdate(args []string) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	checkOnly := flags.Bool("check", false, "only check whether a newer release exists")
	if err := flags.Parse(args); err != nil {
		return err
	}

	updater := update.New(update.Config{
		CurrentVersion: version,
	})
	if *checkOnly {
		result, err := updater.Check(context.Background())
		if err != nil {
			return err
		}
		if !result.Updated {
			fmt.Fprintf(os.Stdout, "s3f is already up to date (%s)\n", result.Version)
			return nil
		}
		fmt.Fprintf(os.Stdout, "new version available: %s\n", result.Version)
		return nil
	}

	result, err := updater.Update(context.Background())
	if err != nil {
		return err
	}
	if !result.Updated {
		fmt.Fprintf(os.Stdout, "s3f is already up to date (%s)\n", result.Version)
		return nil
	}
	fmt.Fprintf(os.Stdout, "updated s3f to %s using %s\n", result.Version, result.AssetName)
	return nil
}
