// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package local converts blob references into local path names for on-disk storage.
package local // import "upspin.io/cloud/storage/disk/internal/local"
import (
	"encoding/base64"
	"path/filepath"
)

// Path returns the file path to hold the contents of the blob
// with the specified reference. The returned path is rooted
// in the provided base directory.
// The method used here will replace the OldPath function once
// a conversion tool is in place, as OldPath has several problems.
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
	enc := base64.RawURLEncoding
	length := enc.EncodedLen(len(ref))
	buf := make([]byte, length, length+4) // Extra room to pad.
	enc.Encode(buf, []byte(ref))
	const (
		numDirs  = 5
		numElems = 1 + numDirs + 1 // base plus up to 5 directories plus tail.
	)
	// Need at least 3 bytes, two for a directory and one for a file.
	// An empty ref gets the blob name "..../++/+".
	for len(buf) < 3 {
		buf = append(buf, '+')
	}
	// Blob needs a padding byte if it's short and could be confused with a directory.
	if len(buf) < 2*(numDirs+1)+1 && len(buf)%2 == 0 {
		buf = append(buf, '+')
	}
	str := string(buf) // This is the blob name to turn into a file path name.
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

// OldPath returns the file path to hold the contents of the blob
// with the specified reference. The returned path is rooted
// in the provided base directory.
// Deprecated: (Or soon to be.) Use Path.
func OldPath(base, ref string) string {
	// The provided reference may not be safe so base64-encode it.
	enc := base64.RawURLEncoding.EncodeToString([]byte(ref))
	var sub string
	if len(enc) > 1 {
		sub = enc[:2]
	}
	return filepath.Join(base, sub, enc)
}
