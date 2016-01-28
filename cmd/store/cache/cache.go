// Package cache stores blobs that are either 1) not yet stored in the cloud or
// 2) that should stay local for performance concerns.
// This cache interface allows some "peeking" inside for performance optimizations (i.e. renaming a ref)
package cache

import (
	"io"
)

type Interface interface {
	// Put stores a blob under the given ref for later retrieval. If another ref exists, it is
	// overwritten. Returns an error if blob can't be read or storage is full.
	Put(ref string, blob io.Reader) error

	// Rename renames a reference in the cache. It always succeeds if the
	// fromRef exists. If toRef exists, it is overwritten. This is
	// just a naming change for performance reasons and does not incur
	// a full blob copy operation.
	Rename(toRef string, fromRef string) error

	// Get returns a reader to a given ref if ref exists; nil otherwise.
	Get(ref string) *io.Reader

	// RandomRef returns a unique but random reference.
	RandomRef() string

	// Purge removes an entry from the cache. It returns an error if
	// the ref did not previously exist.
	Purge(ref string) error

	// IsCached returns true if a reference is in the cache, false otherwise.
	IsCached(ref string) bool
}
