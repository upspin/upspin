// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"text/template"
	"time"

	"upspin.io/access"
	"upspin.io/pack"
	"upspin.io/upspin"
)

// infoDirEntry wraps a DirEntry to allow new methods for easy formatting.
// It also has fields that hold relevant information as we acquire it.
type infoDirEntry struct {
	*upspin.DirEntry
	client upspin.Client
	ctx    upspin.Context
	// The following fields are computed as we run.
	access    *access.Access
	lastUsers string
}

func (d *infoDirEntry) TimeString() string {
	return d.Time.Go().In(time.Local).Format("Mon Jan 2 15:04:05 MST 2006")
}

func (d *infoDirEntry) AttrString() string {
	return attrFormat(d.Attr)
}

func (d *infoDirEntry) Rights() []access.Right {
	return []access.Right{access.Read, access.Write, access.List, access.Create, access.Delete}
}

func (d *infoDirEntry) Readers() string {
	sharer.init()
	sharer.addAccess(d.DirEntry)
	d.lastUsers = "<nobody>"
	if d.IsDir() {
		return "is a directory"
	}
	_, users, _, err := sharer.readers(d.DirEntry)
	if err != nil {
		return err.Error()
	}
	d.lastUsers = users
	return users
}

func (d *infoDirEntry) Hashes() string {
	h := ""
	if d.IsDir() || d.Packing != upspin.EEPack {
		return h
	}
	packer := pack.Lookup(d.Packing)
	hashes, err := packer.ReaderHashes(d.Packdata)
	if err != nil {
		return h
	}
	for _, r := range hashes {
		if h == "" {
			h += " "
		}
		h += fmt.Sprintf("%x...", r[:4])
	}
	return h
}

func (d *infoDirEntry) Users(right access.Right) string {
	users := userListToString(usersWithAccess(d.client, d.access, right))
	if users == d.lastUsers {
		return "(same)"
	}
	d.lastUsers = users
	return users
}

func (d *infoDirEntry) WhichAccess() string {
	var acc *access.Access
	accEntry, err := whichAccessFollowLinks(d.client, d.Name)
	if err != nil {
		return err.Error()
	}
	accFile := "owner only"
	if accEntry == nil {
		// No access file applies.
		acc, err = access.New(d.Name)
		if err != nil {
			// Can't happen, since the name must be valid.
			exitf("%q: %s", d.Name, err)
		}
	} else {
		accFile = string(accEntry.Name)
		data, err := read(d.client, accEntry.Name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot open access file %q: %s\n", accFile, err)
		}
		acc, err = access.Parse(accEntry.Name, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot parse access file %q: %s\n", accFile, err)
		}
	}
	d.access = acc
	return accFile
}

// printInfo prints, in human-readable form, most of the information about
// the entry, including the users that have permission to access it.
// TODO: Present this more neatly.
// TODO: Present group information.
func printInfo(client upspin.Client, ctx upspin.Context, entry *upspin.DirEntry) {
	infoDir := &infoDirEntry{
		DirEntry: entry,
		client:   client,
		ctx:      ctx,
	}
	writer := tabwriter.NewWriter(os.Stdout, 4, 4, 1, ' ', 0)
	err := infoTmpl.Execute(writer, infoDir)
	if err != nil {
		exitf("executing info template: %v", err)
	}
	if writer.Flush() != nil {
		exitf("flushing template output: %v", err)
	}
}

func attrFormat(attr upspin.Attribute) string {
	switch attr {
	case upspin.AttrNone:
		return "none (plain file)"
	case upspin.AttrDirectory:
		return "directory"
	case upspin.AttrLink:
		return "link"
	}
	return fmt.Sprintf("attribute(%#x)", attr)
}

var infoTmpl = template.Must(template.New("info").Parse(infoText))

const infoText = `{{.Name}}
	packing:	{{.Packing}}
	size:	{{.Size}}
	time:	{{.TimeString}}
	writer:	{{.Writer}}
	attributes:	{{.AttrString}}
	sequence:	{{.Sequence}}
	access file:	{{.WhichAccess}}
	key holders: 	{{.Readers}}
	key hashes:     {{.Hashes}}
	{{range $right := .Rights -}}
	can {{$right}}:	{{$.Users $right}}
	{{end -}}
	Block#	Offset	Size	Location
	{{range $index, $block := .Blocks -}}
	{{$index}} {{.Offset}} {{.Size}} {{.Location}}
	{{end}}`
