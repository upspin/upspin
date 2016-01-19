// Simple cache for storing blobs that are either 1) not yet stored in the cloud or
// 2) that should stay local for performance concerns.
// This cache interface allows some "peeking" inside for performance optimizations (i.e. renaming a ref)
package cache

import (
	"io"
)

type Interface interface {
	// Stores a blob under the given ref for later retrieval. If another ref exists, it is
	// overwritten. Returns an error if blob can't be read or storage is full.
	Put(ref string, blob io.Reader) error

	// Renames a reference in the cache. Always succeeds if the
	// fromRef exists. If toRef exists, it is overwritten. This is
	// just a naming change for performance reasons and does not incur
	// a full blob copy operation.
	Rename(toRef string, fromRef string) error

	// Returns a reader to a given ref if ref exists. Nil otherwise.
	Get(ref string) *io.Reader

	// Helper function to uniquely name a ref.
	RandomRef() string
}
