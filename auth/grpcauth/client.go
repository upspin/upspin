package grpcauth

import (
	"math/rand"
	"time"

	gContext "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/upspin/proto"
)

// GRPCPartial is an interface that all GRPC services implement for authentication and ping as part of upspin.Service.
type GRPCPartial interface {
	// Authenticate is the GRPC call for Authenticate.
	Authenticate(ctx gContext.Context, in *proto.AuthenticateRequest, opts ...grpc.CallOption) (*proto.AuthenticateResponse, error)
	// Ping is the GRPC call for Ping.
	Ping(ctx gContext.Context, in *proto.PingRequest, opts ...grpc.CallOption) (*proto.PingResponse, error)
}

// AuthClientService is a partial Service that uses GRPC as transport and implements Authentication.
type AuthClientService struct {
	GRPCPartial GRPCPartial
	GRPCConn    *grpc.ClientConn

	authToken string
}

// NewGRPCClient returns new GRPC client connection connected securely (with TLS) to a GRPC server at the given address.
// If allowUnauthenticatedConnections is true, the connection may not be secure.
func NewGRPCClient(netAddr upspin.NetAddr, allowUnauthenticatedConnections bool) (*grpc.ClientConn, error) {
	// TODO: These timeouts are arbitrary.
	const (
		longTimeout  = 7 * time.Second
		shortTimeout = 3 * time.Second
	)
	// By default, wait until we get a connection. But if we're expecting TLS to fail, wait a little less.
	timeOut := longTimeout
	if allowUnauthenticatedConnections {
		timeOut = shortTimeout
	}
	addr := string(netAddr)
	conn, err := grpc.Dial(addr,
		grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")),
		grpc.WithBlock(),
		grpc.WithTimeout(timeOut),
	)
	if err != nil && allowUnauthenticatedConnections {
		conn, err = grpc.Dial(addr,
			grpc.WithInsecure(),
			grpc.WithBlock(),
			grpc.WithTimeout(longTimeout),
		)
		if err != nil {
			log.Debug.Printf("grpcauth: did not connect even insecurely: %v", err)
			return nil, err
		}
		log.Printf("grpcauth: connected insecurely.")
	}
	return conn, err
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
	resp, err := ac.GRPCPartial.Authenticate(gContext.Background(), req)
	if err != nil {
		return err
	}
	ac.authToken = resp.Token
	return nil
}

// Ping implements uspin.Service.
func (ac *AuthClientService) Ping() bool {
	seq := rand.Int31()
	req := &proto.PingRequest{
		PingSequence: seq,
	}
	resp, err := ac.GRPCPartial.Ping(gContext.Background(), req)
	return err == nil && resp.PingSequence == seq
}

// Shutdown implements upspin.Service.
func (ac *AuthClientService) Shutdown() {
	// The only error returned is ErrClientConnClosing, meaning something else has already caused it to close.
	_ = ac.GRPCConn.Close() // explicitly ignore the error as there's nothing we can do.
}
