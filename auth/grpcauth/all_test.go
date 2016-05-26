package grpcauth_test

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"testing"
	"time"

	gContext "golang.org/x/net/context"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/auth/grpcauth"
	prototest "upspin.googlesource.com/upspin.git/auth/grpcauth/testdata"
	"upspin.googlesource.com/upspin.git/upspin"
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
}

func lookup(userName upspin.UserName) ([]upspin.PublicKey, error) {
	if userName == user {
		return []upspin.PublicKey{p256Key.Public, p521Key.Public}, nil
	}
	return nil, errors.New("No user here")
}

func pickPort() (net.Listener, int) {
	var listener net.Listener
	var err error
	for port := range []int{6666, 6667, 7777, 9999, 4444, 3333, 2222, 1111} {
		listener, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			log.Printf("listen error:", err)
		}
		return listener, port
	}
	log.Fatal("Failed to pick a port for listening.")
	return nil, -1
}

func startServer() grpcauth.SecureServer {
	config := auth.Config{
		Lookup: lookup,
	}
	ss, err := grpcauth.NewSecureServer(config, "testdata/cert.pem", "testdata/key.pem")
	if err != nil {
		log.Fatal(err)
	}
	return ss
}

func (s *server) Hello(ctx gContext.Context, req *prototest.HelloRequest) (*prototest.HelloResponse, error) {
	return nil, nil
}

func TestMain(m *testing.M) {
	ch := make(chan bool)
	go func() {
		grpcServer = startServer()
		srv = &server{
			SecureServer: grpcServer,
		}
		listener, port := pickPort()
		prototest.RegisterTestServiceServer(grpcServer.GRPCServer(), srv)
		go grpcServer.Serve(listener)
		log.Printf("Starting e2e server. on port %d", port)
		ch <- true
	}()
	ready := <-ch
	var code int
	if ready {
		time.Sleep(time.Second)
		code = m.Run()
	}
	log.Printf("Finishing e2e tests: %d", code)
	os.Exit(code)
}
