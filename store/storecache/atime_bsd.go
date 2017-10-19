// +build darwin freebsd netbsd

package storecache

import (
	"os"
	"syscall"
	"time"
)

func atime(fi os.FileInfo) time.Time {
	// http://golang.org/src/os/stat_darwin.go
	// http://golang.org/src/os/stat_freebsd.go
	// http://golang.org/src/os/stat_netbsd.go
	t := fi.Sys().(*syscall.Stat_t).Atimespec
	return time.Unix(int64(t.Sec), int64(t.Nsec))
}
