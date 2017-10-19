package storecache

import (
	"os"
	"syscall"
	"time"
)

func atime(fi os.FileInfo) time.Time {
	// http://golang.org/src/os/stat_plan9.go
	return time.Unix(int64(fi.Sys().(*syscall.Dir).Atime), 0)
}
