// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"time"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/upspin"
)

// printInfo prints, in human-readable form, most of the information about
// the entry, including the users that have permission to access it.
// TODO: Present this more neatly.
// TODO: Present group information.
func printInfo(client upspin.Client, ctx upspin.Context, entry *upspin.DirEntry) {
	fmt.Printf("%s:\n", entry.Name)
	fmt.Printf("\tpacking: %s\n", entry.Packing)
	size, err := entry.Size()
	if err != nil {
		fmt.Printf("\tsize: %d; invalid block structure: %s\n", size, err)
	} else {
		fmt.Printf("\tsize: %d\n", size)
	}
	fmt.Printf("\ttime: %s\n", entry.Time.Go().In(time.Local).Format("Mon Jan 2 15:04:05 MST 2006"))
	fmt.Printf("\twriter: %s\n", entry.Writer)
	fmt.Printf("\tattributes: %s\n", attrFormat(entry.Attr))
	fmt.Printf("\tsequence: %d\n", entry.Sequence)
	dir, err := bind.DirServer(ctx, ctx.DirEndpoint())
	if err != nil {
		exit(err)
	}
	var acc *access.Access
	accFile, err := dir.WhichAccess(entry.Name)
	if err != nil {
		fmt.Printf("\taccess file: %s\n", err)
	} else {
		if accFile == "" {
			// No access file applies.
			fmt.Printf("\taccess: owner only\n")
			acc, err = access.New(entry.Name)
			if err != nil {
				// Can't happen, since the name must be valid.
				exitf("%q: %s", entry.Name, err)
			}
		} else {
			fmt.Printf("\taccess file: %s\n", accFile)
			data, err := read(client, accFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cannot open access file %q: %s\n", accFile, err)
			}
			acc, err = access.Parse(accFile, data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cannot parse access file %q: %s\n", accFile, err)
			}
		}
	}
	keyUsers := "<nobody>"
	if !entry.IsDir() {
		sharer.init()
		sharer.addAccess(entry)
		_, keyUsers, err = sharer.readers(entry)
		if err != nil {
			fmt.Printf("\treaders with keys:   %s\n", err)
		} else {
			fmt.Printf("\treaders with keys:   %s\n", keyUsers)
		}
	}
	if acc != nil {
		rights := []access.Right{access.Read, access.Write, access.List, access.Create, access.Delete}
		for _, right := range rights {
			users := userListToString(usersWithAccess(client, acc, right))
			if users == keyUsers {
				fmt.Printf("\tcan %s: (same)\n", right)
			} else {
				fmt.Printf("\tcan %s: %s\n", right, users)
				keyUsers = users
			}
		}
	}
	fmt.Printf("\tBlock# Offset Size   Location\n")
	for i, block := range entry.Blocks {
		fmt.Printf("\t%-6d %-6d %-6d %s\n",
			i, block.Offset, block.Size, block.Location)
	}
}

func attrFormat(attr upspin.FileAttributes) string {
	switch attr {
	case upspin.AttrNone:
		return "none (plain file)"
	case upspin.AttrDirectory:
		return "directory"
	case upspin.AttrLink:
		return "link"
	case upspin.AttrDirectory | upspin.AttrLink:
		return "link, directory"
	}
	return fmt.Sprintf("attribute(%#x)", attr)
}
