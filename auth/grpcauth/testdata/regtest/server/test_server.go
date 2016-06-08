package main

import (
	"errors"
	"fmt"
	gContext "golang.org/x/net/context"
	"log"
	"net"
	"testing"
	"time"
	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/auth/grpcauth/testdata"
	"upspin.io/upspin"
)

var (
	p256Key = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
		Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
	}
	p521Key = upspin.KeyPair{
		Public:  upspin.PublicKey("p521\n5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077"),
		Private: upspin.PrivateKey("1921083967088521992602096949959788705212477628248305933393351928788805710122036603979819682701613077258730599983893835863485419440554982916289222458067993673"),
	}

	user       = upspin.UserName("joe@blow.com")
	grpcServer grpcauth.SecureServer
	srv        *server
)

type server struct {
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
	t         *testing.T
	iteration int
}

func lookup(userName upspin.UserName) ([]upspin.PublicKey, error) {
	if userName == user {
		return []upspin.PublicKey{p256Key.Public, p521Key.Public}, nil
	}
	return nil, errors.New("No user here")
}

func startServer() (port string) {
	config := auth.Config{
		Lookup: lookup,
	}
	var err error
	grpcServer, err = grpcauth.NewSecureServer(config, "../../cert.pem", "../../key.pem")
	if err != nil {
		log.Fatal(err)
	}
	srv = &server{
		SecureServer: grpcServer,
	}
	var listener net.Listener
	listener, port = pickPort()
	prototest.RegisterTestServiceServer(grpcServer.GRPCServer(), srv)
	log.Printf("Starting e2e server on port %s", port)
	go grpcServer.Serve(listener)
	return port
}

func pickPort() (listener net.Listener, port string) {
	var err error
	listener, err = net.Listen("tcp", "localhost:0")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	_, port, err = net.SplitHostPort(listener.Addr().String())
	if err != nil {
		log.Fatalf("Failed to parse listener address: %v", err)
	}
	return listener, port
}

func (s *server) DoATrump(ctx gContext.Context, req *prototest.DoATrumpRequest) (*prototest.DoATrumpResponse, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if session.User() != user {
		log.Fatalf("Expected user %q, got %q", user, session.User())
	}
	if !session.IsAuthenticated() {
		log.Fatalf("Expected authenticated session.")
	}
	resp := &prototest.DoATrumpResponse{
		TrumpResponse: fmt.Sprintf("%s: meh: %s", time.Now(), req.PeopleDemand),
	}
	log.Printf("Got request: %s. Replying...", req.PeopleDemand)
	return resp, nil // not reached
}

func monitorServer() {
	for {
		time.Sleep(1 * time.Minute)
		log.Printf("Server alive")
	}
}

func main() {
	port := startServer()
	fmt.Printf("Started server on port: %d", port)
	monitorServer()
}
