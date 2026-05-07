package main

import (
	"fmt"
	"os"
)

var version = "dev"

const mainUsage = `Usage:
    tare check -i IMAGE [options] [config-files...]
    tare scan  -i IMAGE [options] [config-files...]
    tare version

Commands:
    check     Check a container image for runtime issues and run structure tests
    scan      Scan a container image for shared library dependency issues
    version   Print version information

Run "tare <command> --help" for command options.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, mainUsage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "check":
		os.Exit(runCheck(os.Args[2:]))
	case "scan":
		os.Exit(runScan(os.Args[2:]))
	case "version":
		fmt.Printf("tare %s\n", version)
	case "help", "-h", "--help":
		fmt.Println(mainUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		fmt.Fprintln(os.Stderr, mainUsage)
		os.Exit(1)
	}
}
