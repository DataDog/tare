package main

import (
	"fmt"
	"os/user"
)

var version = "dev"

func main() {
	fmt.Printf("hello %s\n", version)

	// os/user uses cgo for lookups on Linux, which makes the binary
	// dynamically linked when built with CGO_ENABLED=1.
	u, err := user.Current()
	if err != nil {
		fmt.Printf("user: %v\n", err)
		return
	}
	fmt.Printf("running as %s (uid %s)\n", u.Username, u.Uid)
}
