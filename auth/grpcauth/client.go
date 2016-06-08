// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package grpcauth

import (
	"crypto/tls"
	"math/rand"
	"strings"
	"time"

	gContext "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// GRPCCommon is an interface that all GRPC services implement for authentication and ping as part of upspin.Service.
type GRPCCommon interface {
	// Authenticate is the GRPC call for Authenticate.
	Authenticate(ctx gContext.Context, in *proto.AuthenticateRequest, opts ...grpc.CallOption) (*proto.AuthenticateResponse, error)
	// Ping is the GRPC call for Ping.
	Ping(ctx gContext.Context, in *proto.PingRequest, opts ...grpc.CallOption) (*proto.PingResponse, error)
}

// AuthClientService is a partial Service that uses GRPC as transport and implements Authentication.
type AuthClientService struct {
	grpcCommon       GRPCCommon
	grpcConn         *grpc.ClientConn
	context          upspin.Context
	authToken        string
	lastTokenRefresh time.Time

	keepAliveInterval time.Duration // interval of keep alive packets.
	lastNetActivity   time.Time     // last known time of any network activity.
	closeKeepAlive    chan bool     // channel used to tell the keep alive routine to exit.
	keepAliveRound    uint64        // counts iterations of the keep alive routine. Mostly for tests.
}

const (
	// AllowSelfSignedCertificate is used for documenting the parameter with same name in NewGRPCClient.
	AllowSelfSignedCertificate = true

	// KeepAliveInterval is a suggested interval between keep-alive ping requests to the server.
	// A value of 0 means keep-alives are disabled. Google Cloud Platform (GCP) times out connections
	// every 10 minutes so a smaller values are recommended for talking to servers on GCP.
	KeepAliveInterval = 5 * time.Minute
)

// To be safe, we refresh the token 1 hour ahead of time.
var tokenFreshnessDuration = authTokenDuration - time.Hour

// NewGRPCClient returns new GRPC client connected securely (with TLS) to a GRPC server at a net address.
// The address is expected to be a raw network address with port number, as in domain.com:5580. However, for convenience,
// it is optionally accepted for the time being to use one of the following prefixes:
// https://, http://, grpc://. This may change in the future.
// A keep alive interval indicates the amount of time to send ping requests to the server. A duration of 0 disables
// keep alive packets.
// If allowSelfSignedCertificates is true, the client will connect with a server with a self-signed certificate.
// Otherwise it will reject it. Mostly only useful for testing a local server.
func NewGRPCClient(context *upspin.Context, netAddr upspin.NetAddr, keepAliveInterval time.Duration, allowSelfSignedCertificate bool) (*AuthClientService, error) {
	if keepAliveInterval != 0 && keepAliveInterval < time.Minute {
		log.Info.Printf("Keep alive interval too short. You may overload the server and be throttled")
	}
	addr := string(netAddr)
	isHTTP := strings.HasPrefix(addr, "http://")
	isHTTPS := strings.HasPrefix(addr, "https://")
	isGRPC := strings.HasPrefix(addr, "grpc://")
	skip := 0
	switch {
	case isHTTP, isGRPC:
		skip = 7
	case isHTTPS:
		skip = 8
	}
	conn, err := grpc.Dial(addr[skip:],
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: allowSelfSignedCertificate})),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}
	ac := &AuthClientService{
		grpcConn:          conn,
		context:           *context,
		keepAliveInterval: keepAliveInterval,
		closeKeepAlive:    make(chan bool),
	}
	if keepAliveInterval != 0 {
		go ac.keepAlive()
	}
	return ac, nil
}

// keepAlive loops forever pinging the server every keepAliveInterval. It skips pings if there has been network
// activity more recently than the keep alive interval. It must run on a separate go routine.
func (ac *AuthClientService) keepAlive() {
	log.Printf("Starting keep alive client")
	sleepFor := ac.keepAliveInterval
	for {
		ac.keepAliveRound++
		select {
		case <-time.After(sleepFor):
			lastIdleness := time.Now().Sub(ac.lastNetActivity)
			if lastIdleness < ac.keepAliveInterval {
				sleepFor = ac.keepAliveInterval - lastIdleness
				log.Debug.Printf("New ping in %v", sleepFor)
				continue
			}
			sleepFor = ac.keepAliveInterval
			if !ac.Ping() {
				log.Error.Printf("KeepAlive: ping failed")
			}
			log.Debug.Printf("Keep alive: ping okay")
			ac.lastNetActivity = time.Now()
		case <-ac.closeKeepAlive:
			log.Printf("Exiting keep alive routine")
			ac.keepAliveRound = 0
			return
		}
	}
}

// SetService sets the underlying RPC service which was obtained with proto.NewSERVICENAMEClient, where SERVICENAME is
// the RPC service definition from the proto file.
func (ac *AuthClientService) SetService(common GRPCCommon) {
	ac.grpcCommon = common
}

// GRPCConn returns the grpc client connection used to dial the server.
func (ac *AuthClientService) GRPCConn() *grpc.ClientConn {
	return ac.grpcConn
}

// Authenticate implements upspin.Service.
func (ac *AuthClientService) Authenticate(ctx *upspin.Context) error {
	req := &proto.AuthenticateRequest{
		UserName: string(ctx.UserName),
		Now:      time.Now().UTC().Format(time.ANSIC), // to discourage signature replay
	}
	sig, err := ctx.Factotum.UserSign([]byte(string(req.UserName) + " Authenticate " + req.Now))
	if err != nil {
		return err
	}
	req.Signature = &proto.Signature{
		R: sig.R.String(),
		S: sig.S.String(),
	}
	resp, err := ac.grpcCommon.Authenticate(gContext.Background(), req)
	if err != nil {
		return err
	}
	log.Debug.Printf("Authenticate: got authtoken for user %s: %s", req.UserName, resp.Token)
	ac.authToken = resp.Token
	now := time.Now()
	ac.lastTokenRefresh = now
	ac.lastNetActivity = now
	return nil
}

// Ping implements uspin.Service.
func (ac *AuthClientService) Ping() bool {
	seq := rand.Int31()
	req := &proto.PingRequest{
		PingSequence: seq,
	}
	gctx, _ := gContext.WithTimeout(gContext.Background(), 3*time.Second) // ignore the cancel function.
	resp, err := ac.grpcCommon.Ping(gctx, req)
	if err != nil {
		log.Printf("Ping error: %s", err)
	}
	ac.lastNetActivity = time.Now()
	return err == nil && resp.PingSequence == seq
}

func (ac *AuthClientService) isAuthTokenExpired() bool {
	return ac.authToken == "" || ac.lastTokenRefresh.Add(tokenFreshnessDuration).Before(time.Now())
}

// NewAuthContext creates a new RPC context with the required authentication tokens set and ensures re-authentication
// is done if necessary.
func (ac *AuthClientService) NewAuthContext() (gContext.Context, error) {
	var err error
	if ac.isAuthTokenExpired() {
		err = ac.Authenticate(&ac.context)
		if err != nil {
			return nil, err
		}
	}
	log.Debug.Printf("SetAuthContext: set auth token: %s", ac.authToken)
	return metadata.NewContext(gContext.Background(), metadata.Pairs(authTokenKey, ac.authToken)), nil
}

// Close implements upspin.Service.
func (ac *AuthClientService) Close() {
	ac.closeKeepAlive <- true
	// The only error returned is ErrClientConnClosing, meaning something else has already caused it to close.
	_ = ac.grpcConn.Close() // explicitly ignore the error as there's nothing we can do.
}
