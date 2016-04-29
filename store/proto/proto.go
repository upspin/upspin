// Package proto contains the definitions shared between RPC store server and client,
// one pair for each remote call.
// TODO: Maybe move to gprc?
package proto

import "upspin.googlesource.com/upspin.git/upspin"

type GetRequest struct {
	Reference upspin.Reference
}

type GetResponse struct {
	Data      []byte
	Locations []upspin.Location
}

type PutRequest struct {
	Data []byte
}

type PutResponse struct {
	Reference upspin.Reference
}

type DeleteRequest struct {
	Reference upspin.Reference
}

type DeleteResponse struct {
}
