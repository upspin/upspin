// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package local converts blob references into local path names for on-disk storage.
package local // import "upspin.io/cloud/storage/disk/internal/local"
import (
	"encoding/base64"
	"path/filepath"
	"strings"

	"upspin.io/errors"
)

// Both encodings compute the base64 encoding of the reference.
// The old one then takes an encoding like abcdef and creates file
//	ab/abcdef
// The new one does better balancing by creating a multi-level
// hierarchy to avoid huge directories:
//	ab/cd/de/ef+
// Here the + is used as a pad to guarantee the file is not confusable
// with a directory. The tree is never longer than 5 levels.

// Use RawURLEncoding to ensure path-safe characters.
var enc = base64.RawURLEncoding

// Maximum number of directories to create in a single path.
const numDirs = 5

// Path returns the file path to hold the contents of the blob with the
// specified reference. The returned path is rooted in the provided base
// directory.
func Path(base, ref string) string {
	// The provided reference may not be safe so base64-encode it.
	// We divide the structure into subdirectories with two-byte names
	// to an arbitrary depth.  We must be careful though not to create
	// a directory the same name as a blob. This is easily done by
	// making all blob names an odd number of bytes long. The
	// base64 URL character set includes only - and _ as punctuation,
	// so we use + as a pad. We also pad to make sure the path is long
	// enough.
	// We avoid multiple allocations by thinking ahead.
	length := enc.EncodedLen(len(ref))
	buf := make([]byte, length, length+4) // Extra room to pad.
	enc.Encode(buf, []byte(ref))
	// Need at least 3 bytes, two for a directory and one for a file.
	// An empty ref gets the blob name "..../++/+".
	for len(buf) < 3 {
		buf = append(buf, '+')
	}
	// Blob needs a padding byte if it's short and could be confused with a directory.
	if len(buf) < 2*numDirs+1 && len(buf)%2 == 0 {
		buf = append(buf, '+')
	}
	str := string(buf)               // This is the blob name to turn into a file path name.
	const numElems = 1 + numDirs + 1 // Base plus directories plus file name.
	elems := make([]string, 0, numElems)
	elems = append(elems, base)
	for i := 1; i < numElems-1; i++ {
		if len(str) < 3 {
			break
		}
		elems = append(elems, str[:2])
		str = str[2:]
	}
	elems = append(elems, str)
	return filepath.Join(elems...)
}

// Ref returns the reference for the given path name. The path name must have
// the storage base path stripped from it, and should not begin with a path
// separator.
func Ref(path string) (string, error) {
	// A sanity check.
	if strings.Count(path, string(filepath.Separator)) > numDirs {
		return "", errors.Errorf("path %q contains too many directories", path)
	}
	// Remove path separators.
	encoded := strings.Replace(path, string(filepath.Separator), "", -1)
	// Remove trailing padding (+ symbols).
	if i := strings.Index(encoded, "+"); i >= 0 {
		encoded = encoded[:i]
	}
	// Decode base64-encoded path.
	ref, err := enc.DecodeString(encoded)
	if err != nil {
		return "", errors.Errorf("%q: %v", encoded, err)
	}
	return string(ref), nil
}

// OldPath returns the file path to hold the contents of the blob with the
// specified reference. The returned path is rooted in the provided base
// directory.
//
// This is the prior encoding and can no longer be deployed. It is maintained
// because ../../convert.go uses it to convert old disk trees that still have
// it.
//
// Deprecated: Use Path.
func OldPath(base, ref string) string {
	// The provided reference may not be safe so base64-encode it.
	enc := base64.RawURLEncoding.EncodeToString([]byte(ref))
	var sub string
	if len(enc) > 1 {
		sub = enc[:2]
	}
	return filepath.Join(base, sub, enc)
}
