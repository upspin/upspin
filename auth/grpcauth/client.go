package grpcauth

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/upspin"
)

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

/* TODO: this is coming up soon.
type AuthClientService struct {
	authToken  string
	clientConn grpc.ClientConn
}

func (ac *AuthClientService) Authenticate(ctx *upspin.Context) error {
	req := &proto.AuthenticateRequest{
		UserName: ctx.UserName,
		Now:      time.Now().UTC().Format(time.ANSIC), // to discourage signature replay
	}
	sig, err := ctx.Factotum.UserSign([]byte(string(req.UserName) + " Authenticate " + req.Now))
	if err != nil {
		return err
	}
	req.Signature = sig
	var resp proto.AuthenticateResponse
}
*/
