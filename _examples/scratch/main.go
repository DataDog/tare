package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("1.0.0")
		return
	}

	// Make an HTTPS request to verify TLS works. This requires CA
	// certificates to be present in the image. scratch has none,
	// so the bad image fails here.
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	resp, err := client.Head("https://www.google.com")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tls check failed: %v\n", err)
		os.Exit(1)
	}
	resp.Body.Close()
	fmt.Println("tls ok")
}
