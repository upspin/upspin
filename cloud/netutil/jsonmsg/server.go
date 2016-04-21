// Package jsonmsg handles marshaling and unmarshaling Upspin data structures to and from JSON.
package jsonmsg

import (
	"net/http"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"
)

// This file supports the server side by marshaling special messages and sending them on the wire.

// SendWhichAccessResponse marshals accessPathName and sends it on w.
func SendWhichAccessResponse(accessPathName upspin.PathName, w http.ResponseWriter) {
	netutil.SendJSONReply(w, whichAccessMessage{Access: accessPathName})
}

// SendReferenceResponse marshals ref and sends it on w.
func SendReferenceResponse(ref upspin.Reference, w http.ResponseWriter) {
	netutil.SendJSONReply(w, refMessage{Ref: ref})
}

// SendUserLookupResponse marshals all parameters and sends them on w.
func SendUserLookupResponse(userName upspin.UserName, endpoints []upspin.Endpoint, keys []upspin.PublicKey, w http.ResponseWriter) {
	ue := userEntry{
		User:      userName,
		Keys:      keys,
		Endpoints: endpoints,
	}
	netutil.SendJSONReply(w, ue)
}
