// +build linux dragonfly openbsd solaris

package storecache

import (
	"os"
	"syscall"
	"time"
)

func atime(fi os.FileInfo) time.Time {
	// http://golang.org/src/os/stat_linux.go
	// http://golang.org/src/os/stat_dragonfly.go
	// http://golang.org/src/os/stat_openbsd.go
	// http://golang.org/src/os/stat_solaris.go
	t := fi.Sys().(*syscall.Stat_t).Atim
	return time.Unix(int64(t.Sec), int64(t.Nsec))
}
