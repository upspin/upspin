// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package rpc provides a framework for implementing RPC servers and clients.

RPC wire protocol

The protocol for a particular method is to send an HTTP request with
the appropriate request message to an "api" URL with the server type and
method name; the response will then be returned. Both request and response
are sent as binary protocol buffers, as declared by upspin.io/upspin/proto.

For example, to call the Put method of the Store server running at
store.example.com, send to the URL
     https://store.example.com/api/Store/Put
a POST request with, as payload, an encoded StorePutRequest. The response
will be a StorePutResponse. There is such a pair of protocol buffer messages
for each method.

For streaming RPC methods, the requests are the same, but the response is sent
as the bytes "OK" followed by a series of encoded protocol buffers.
Each encoded message is preceded by a four byte, big-endian-encoded int32 that
describes the length of the following encoded protocol buffer.
The stream is considered closed when the HTTP response stream ends.

If an error occurs while processing a request, the server returns a 500
Internal Server Error status code and the response body contains the error
string.

For details of transcoding between Upspin types and their protocol buffer form,
see the documentation in upspin.io/upspin/proto.
*/
package rpc
