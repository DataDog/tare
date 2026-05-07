// Package harness provides harness tarballs for injection into containers.
//
// The .tar.gz files are built by 'make harness', which assembles the harness
// directory for each architecture and produces gzipped tarballs with a
// tmp/.tare/ prefix suitable for piping to 'docker cp - container:/'.
//
// Zero-byte placeholder files are committed to the repository. Running
// 'make harness' overwrites them with real tarballs locally.
package harness

import (
	_ "embed"
	"fmt"
)

//go:embed harness-linux-amd64.tar.gz
var linuxAmd64 []byte

//go:embed harness-linux-arm64.tar.gz
var linuxArm64 []byte

// embedded returns the gzipped tar of the harness for the given platform.
// Platform should be in "linux/amd64" format.
// Returns an error if the embedded harness is a zero-byte placeholder.
func embedded(platform string) ([]byte, error) {
	var data []byte

	switch platform {
	case "linux/amd64":
		data = linuxAmd64
	case "linux/arm64":
		data = linuxArm64
	default:
		return nil, fmt.Errorf("unsupported platform %q (available: linux/amd64, linux/arm64)", platform)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("harness for %s not built; run 'make harness'", platform)
	}

	return data, nil
}
