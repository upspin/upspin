// Package proto contains the definitions shared between RPC directory server and client,
// one pair for each remote call.
// TODO: Maybe move to gprc?
package proto

import "upspin.io/upspin"

type LookupRequest struct {
	Name upspin.PathName
}

type LookupResponse struct {
	Entry *upspin.DirEntry
}

type PutRequest struct {
	Entry *upspin.DirEntry
}

type PutResponse struct {
}

type MakeDirectoryRequest struct {
	Name upspin.PathName
}

type MakeDirectoryResponse struct {
	Location upspin.Location
}

type GlobRequest struct {
	Pattern string
}

type GlobResponse struct {
	Entries []*upspin.DirEntry
}

type DeleteRequest struct {
	Name upspin.PathName
}

type DeleteResponse struct {
}

type WhichAccessRequest struct {
	Name upspin.PathName
}

type WhichAccessResponse struct {
	Name upspin.PathName
}

// Types for the methods of upspin.Service.
// TODO: Put somewhere shared?

type ConfigureRequest struct {
	Options []string
}

type ConfigureResponse struct {
}

type EndpointRequest struct {
}

type EndpointResponse struct {
	Endpoint upspin.Endpoint
}

type ServerUserNameRequest struct {
}

type ServerUserNameResponse struct {
	UserName string
}

// Authenticate is not part of upspin.Directory. It is used only in the remote implementation.
type AuthenticateRequest struct {
	UserName  upspin.UserName
	Now       string
	Signature upspin.Signature
}

type AuthenticateResponse struct {
	ID int
}
