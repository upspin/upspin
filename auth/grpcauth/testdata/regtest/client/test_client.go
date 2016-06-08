package main

import (
	"flag"
	"log"
	"time"
	"upspin.io/auth/grpcauth"
	"upspin.io/auth/grpcauth/testdata"
	"upspin.io/factotum"
	"upspin.io/upspin"
)

var (
	name    = flag.String("name", "client", "this client's name")
	netaddr = flag.String("netaddr", "localhost:9999", "address and port pair of the server")
	cli     *client

	p256Key = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
		Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
	}
	p521Key = upspin.KeyPair{
		Public:  upspin.PublicKey("p521\n5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077"),
		Private: upspin.PrivateKey("1921083967088521992602096949959788705212477628248305933393351928788805710122036603979819682701613077258730599983893835863485419440554982916289222458067993673"),
	}

	user = upspin.UserName("joe@blow.com")
)

type client struct {
	grpcauth.AuthClientService // For handling Authenticate, Ping and Close.
	grpcClient                 prototest.TestServiceClient
}

func (c *client) SayShit() {
	gCtx, err := c.NewAuthContext()
	if err != nil {
		log.Fatal(err)
	}
	req := &prototest.DoATrumpRequest{
		PeopleDemand: *name,
	}
	log.Printf("Client: telling server: %q", req.PeopleDemand)
	resp, err := c.grpcClient.DoATrump(gCtx, req)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Client: server responded: %q", resp.TrumpResponse)
}

func startClient(netaddr string) {
	const allowSelfSignedCert = true

	f, err := factotum.New(p256Key)
	if err != nil {
		log.Fatal(err)
	}
	ctx := &upspin.Context{
		UserName: user,
		Factotum: f,
	}

	authClient, err := grpcauth.NewGRPCClient(ctx, upspin.NetAddr(netaddr), allowSelfSignedCert)
	if err != nil {
		log.Fatal(err)
	}
	grpcClient := prototest.NewTestServiceClient(authClient.GRPCConn())
	authClient.SetService(grpcClient)
	cli = &client{
		AuthClientService: *authClient,
		grpcClient:        grpcClient,
	}
	for {
		log.Printf("Client: going to say shit.")
		cli.SayShit()
		time.Sleep(20 * time.Minute)
	}
}

func main() {
	flag.Parse()

	startClient(*netaddr)
}
