package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	tareelf "github.com/DataDog/tare/internal/elf"
	"github.com/DataDog/tare/internal/rootfs"
)

func runElf(args []string) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: tare-tool elf <deps|info> PATH\n")
		return 2
	}

	switch args[0] {
	case "deps":
		return runElfDeps(args[1:])
	case "info":
		return runElfInfo(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown elf subcommand: %s\n", args[0])
		return 2
	}
}

func defaultRootFS() rootfs.FS {
	root, err := os.OpenRoot("/")
	if err != nil {
		panic(fmt.Sprintf("opening root: %v", err))
	}
	return rootfs.New(root, "/")
}

func runElfDeps(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: tare-tool elf deps PATH\n")
		return 2
	}

	path := args[0]
	fsys := defaultRootFS()
	result, err := tareelf.Deps(fsys, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "%s: no such file\n", path)
		} else {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		}
		return 1
	}

	if !result.IsDynamic() {
		fmt.Printf("%s: statically linked\n", path)
		return 0
	}

	fmt.Printf("%s:\n", path)
	if result.Interp != nil {
		status := "ok"
		if result.Interp.Status == tareelf.DepMissing {
			status = "MISSING"
		}
		fmt.Printf("  interpreter: %s [%s]\n", result.Interp.Path, status)
	}

	for _, dep := range result.Deps {
		if dep.Status == tareelf.DepOK {
			fmt.Printf("  %s => %s [ok]\n", dep.Name, dep.Resolved)
		} else {
			fmt.Printf("  %s => not found [MISSING]\n", dep.Name)
		}
	}

	if result.HasMissing() {
		return 1
	}
	return 0
}

func runElfInfo(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: tare-tool elf info PATH\n")
		return 2
	}

	path := args[0]
	fsys := defaultRootFS()
	info, err := tareelf.Info(fsys, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "%s: no such file\n", path)
		} else {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		}
		return 1
	}

	fmt.Printf("%s:\n", info.Path)
	fmt.Printf("  type: %s\n", info.Type)
	fmt.Printf("  arch: %s\n", info.Arch)

	return 0
}
