package storecache

import (
	"os"
	"syscall"
	"time"
)

func atime(fi os.FileInfo) time.Time {
	// http://golang.org/src/os/stat_windows.go
	t := fi.Sys().(*syscall.Win32FileAttributeData).LastAccessTime
	return time.Unix(0, t.Nanoseconds())
}
