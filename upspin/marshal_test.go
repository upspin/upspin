// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"reflect"
	"testing"
)

var dirEnt = DirEntry{
	Name:    "u@foo.com/a/directory",
	Packing: EEPack,
	Time:    123456,
	Blocks: []DirBlock{
		dirBlock1,
		dirBlock2,
	},
	Packdata: []byte{1, 2, 3, 4},
	Attr:     AttrDirectory, // Just so it's not zero; this is not a semantically valid entry.
	Sequence: 1234,
	Writer:   "u@foo.com",
}

var dirBlock1 = DirBlock{
	Location: Location{
		Endpoint: Endpoint{
			Transport: Remote,
			NetAddr:   "foo.com:1234",
		},
		Reference: "Cinder",
	},
	Offset:   0,
	Size:     1024,
	Packdata: []byte("sign"),
}

var dirBlock2 = DirBlock{
	Location: Location{
		Endpoint: Endpoint{
			Transport: InProcess,
			NetAddr:   "foo.com:1234",
		},
		Reference: "Ice",
	},
	Offset:   1024,
	Size:     4096,
	Packdata: []byte("nature"),
}

func TestDirEntMarshal(t *testing.T) {
	data, err := dirEnt.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var new DirEntry
	remaining, err := new.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("data remains after unmarshal")
	}
	if !reflect.DeepEqual(&dirEnt, &new) {
		t.Errorf("bad result. expected:")
		t.Errorf("%+v\n", &dirEnt)
		t.Errorf("got:")
		t.Errorf("%+v\n", &new)
	}
}

func TestDirBlockMarshal(t *testing.T) {
	data, err := dirBlock1.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var new DirBlock
	remaining, err := new.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("data remains after unmarshal")
	}
	if !reflect.DeepEqual(&dirBlock1, &new) {
		t.Errorf("bad result. expected:")
		t.Errorf("%+v\n", &dirBlock1)
		t.Errorf("got:")
		t.Errorf("%+v\n", &new)
	}
}

func TestDirEntMarshalAppendNoMalloc(t *testing.T) {
	// Marshal to see what length we need.
	data, err := dirEnt.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Toss old data but keep length.
	data = make([]byte, len(data))
	p := &data[0]
	data, err = dirEnt.MarshalAppend(data[:0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if p != &data[0] {
		t.Fatalf("MarshalAppend allocated")
	}
}
