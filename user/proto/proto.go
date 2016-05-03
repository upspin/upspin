// Package proto contains the definitions shared between RPC user server and client,
// one pair for each remote call.
// TODO: Maybe move to gprc?
package proto

import "upspin.googlesource.com/upspin.git/upspin"

type LookupRequest struct {
	UserName upspin.UserName
}

type LookupResponse struct {
	Endpoints  []upspin.Endpoint
	PublicKeys []upspin.PublicKey
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
