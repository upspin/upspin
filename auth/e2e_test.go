// End to end tests using a test HTTP server instance with CA checking disabled, but otherwise functional TLS encryption.
package auth_test

import (
	"crypto/tls"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/auth"
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

	user   = upspin.UserName("joe@blow.com")
	server *httptest.Server
	url    string
)

func lookup(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	if userName == user {
		return nil, []upspin.PublicKey{p256Key.Public, p521Key.Public}, nil
	}
	return nil, nil, errors.New("No user here")
}

func authHelloHandle(authHandler auth.Handler, w http.ResponseWriter, r *http.Request) {
	if !authHandler.IsAuthenticated() {
		log.Fatal("Expected authenticated connection here")
	}
	w.Write([]byte("HELLO"))
}

func stopHandle(w http.ResponseWriter, r *http.Request) {
	log.Println("Closing server. Goodbye.")
	server.Close()
}

func startServer() *httptest.Server {
	ah := auth.NewHandler(&auth.Config{
		Lookup: lookup,
	})
	mux := http.NewServeMux()
	// This URI is protected by auth
	mux.HandleFunc("/authhello", ah.Handle(authHelloHandle))
	// This URI is not protected by auth
	mux.HandleFunc("/stop", stopHandle)
	return httptest.NewTLSServer(mux)
}

func TestEndToEnd(t *testing.T) {
	if !strings.HasPrefix(url, "https://") {
		t.Fatalf("URL scheme is not https: %q", url)
	}
	req, err := http.NewRequest("GET", url+"/authhello", nil)
	if err != nil {
		t.Fatal(err)
	}
	// We disable checking whether our certificate is valid or not, since it's self-signed.
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}
	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	client := &http.Client{
		Transport: tr,
	}
	err = auth.SignRequest(user, p256Key, req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "HELLO" {
		t.Errorf("Expected HELLO, got %q", data)
	}
	req, err = http.NewRequest("GET", url+"/stop", nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	ch := make(chan bool)
	go func() {
		server = startServer()
		url = server.URL
		log.Printf("Started e2e server. url: %s", url)
		ch <- true
	}()
	ready := <-ch
	if ready {
		m.Run()
	}
	log.Println("Finishing e2e tests")
}
