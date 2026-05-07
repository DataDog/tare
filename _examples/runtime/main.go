// A tiny app that exits non-zero unless its deployment environment is in
// place: an APP_ENV variable (provided by the deployment) and a config
// file at /etc/myapp/config.yaml (mounted from a k8s configmap or
// equivalent). Used to demonstrate tare.runtime in `_examples/runtime`.
package main

import (
	"fmt"
	"os"
)

func main() {
	env := os.Getenv("APP_ENV")
	if env == "" {
		fmt.Fprintln(os.Stderr, "APP_ENV not set")
		os.Exit(1)
	}
	data, err := os.ReadFile("/etc/myapp/config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "config missing: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("env=%s\n", env)
	fmt.Printf("config:\n%s", data)
}
