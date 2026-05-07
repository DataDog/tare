package main

import (
	"fmt"
	"os"
)

var version = "dev"

const usage = `Usage:
    tare-tool run-tests <plan-file>
    tare-tool idle
    tare-tool scan PATH [PATH...]
    tare-tool elf <deps|info> PATH
    tare-tool version

Commands:
    run-tests   Execute a JSON test plan and output TAP results.
    idle        Block until SIGTERM. Used as container entrypoint.
    scan        Walk paths, find ELF binaries, check shared library dependencies.
                Exits non-zero if any dependency is unresolved.
    elf         ELF binary inspection subcommands:
                  deps    Resolve shared library dependencies for a single binary.
                  info    Print binary metadata (type, arch).
    version     Print version information`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run-tests":
		os.Exit(runTests(os.Args[2:]))
	case "idle":
		os.Exit(runIdle(os.Args[2:]))
	case "scan":
		os.Exit(runScan(os.Args[2:]))
	case "elf":
		os.Exit(runElf(os.Args[2:]))
	case "version":
		fmt.Printf("tare-tool %s\n", version)
	case "help", "-h", "--help":
		fmt.Println(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
}
