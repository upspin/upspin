package storecache

import (
	"os"
	"sort"
)

type byRecentAccess []os.FileInfo

var _ sort.Interface = (*byRecentAccess)(nil)

func (fis byRecentAccess) Len() int           { return len(fis) }
func (fis byRecentAccess) Swap(i, j int)      { fis[i], fis[j] = fis[j], fis[i] }
func (fis byRecentAccess) Less(i, j int) bool { return atime(fis[i]).Before(atime(fis[j])) }
