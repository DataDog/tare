// Package scan provides runtime filesystem checks for container image analysis.
package scan

import "github.com/DataDog/tare/internal/rootfs"

// Warning represents a non-fatal issue found during analysis.
type Warning struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// runtimeFile defines a file to check and the warning if it's missing.
type runtimeFile struct {
	path    string
	message string
}

var runtimeFiles = []runtimeFile{
	{
		path:    "/etc/ssl/certs/ca-certificates.crt",
		message: "TLS connections will fail without CA certificates",
	},
	{
		path:    "/etc/nsswitch.conf",
		message: "DNS resolution may not work correctly for applications using glibc",
	},
	{
		path:    "/etc/passwd",
		message: "user lookups will fail (e.g. os/user, getpwuid)",
	},
	{
		path:    "/etc/group",
		message: "group lookups will fail",
	},
}

func checkRuntime(fsys rootfs.FS) []Warning {
	return checkFiles(fsys, runtimeFiles)
}

func checkFiles(fsys rootfs.FS, files []runtimeFile) []Warning {
	var warnings []Warning
	for _, rf := range files {
		if _, err := fsys.Stat(rf.path); err != nil {
			warnings = append(warnings, Warning{
				Path:    rf.path,
				Message: rf.message,
			})
		}
	}
	return warnings
}
