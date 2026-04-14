package main

import (
	"flag"
	"fmt"
	"os"

	"hduwords/internal/tuiapp"
	"hduwords/internal/updatecheck"
)

func main() {
	applyUpdate := flag.Bool("apply-update", false, "apply downloaded update and exit")
	sourcePath := flag.String("source", "", "downloaded update source path")
	targetPath := flag.String("target", "", "target executable path")
	flag.Parse()
	if *applyUpdate {
		if err := updatecheck.InstallBinary(*sourcePath, *targetPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := tuiapp.Run(flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
