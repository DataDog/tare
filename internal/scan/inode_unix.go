//go:build unix

package scan

import (
	"os"
	"syscall"
)

func inodeOf(info os.FileInfo) (uint64, bool) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino, true
	}
	return 0, false
}
