package storecache

import (
	"os"
	"sort"
)

type byAccessTime []os.FileInfo

var _ sort.Interface = (*byAccessTime)(nil)

func (fis byAccessTime) Len() int           { return len(fis) }
func (fis byAccessTime) Swap(i, j int)      { fis[i], fis[j] = fis[j], fis[i] }
func (fis byAccessTime) Less(i, j int) bool { return atime(fis[i]).Before(atime(fis[j])) }
