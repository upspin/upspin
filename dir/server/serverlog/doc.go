// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package serverlog maintains logs for directory servers, permitting
// replay, recovering, and mirroring.
package serverlog

/*

The package defines and implements three components for record keeping for a
dir/server/tree.Tree:

1) writer - writes log entries to the end of the log file.
2) Reader - reads log entries from any offset of the log file.
3) checkpoint - saves the most recent commit point in the log and the root.

The structure on disk is, relative to a log directory:

tree.root.<username>  - root entry for username
tree.index.<username> - log checkpoint for username (historically named).
d.tree.log.<username> - subdirectory for username, containing files named:
<offset>.<version> - log greater than offset but less than the next offset file.
The .version part is missing for old-format logs.

There may also be a legacy file tree.log.<username> which will be renamed
(and set to offset 0) if found.

The format of the log files is straightforward. Each log is a
concatenation of records containing an Op, a marshaled DirEntry,
and a checksum.  The Op is written as a single byte that happens,
for historical reasons, to be the varint encoding of signed 0 or 1.

A record looks like this in the logs:

one byte: the Op, 0x00 for a Put, 0x02 for a Delete.
N bytes: the result of calling DirEntry.Marshal for the entry.
4 bytes: a simple checksum calculated by the checksum function.

To prevent problems with corrupted logs, a marshaled DirEntry is
required to fit within 64MB.

A root file contains one DirEntry, recording the directory entry
of the last saved root of the user's tree, marshaled by DirEntry.Marshal.

A checkpoint file contains one record, a varint-encoded signed
offset of the most recently saved global file offset (position in
the concatenation of all log files).

*/
