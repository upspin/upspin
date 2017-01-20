package main

import (
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var (
	testServer http.Handler
	addr       string
	once       sync.Once
)

func startServer() {
	testServer := httptest.NewServer(newServer())
	addr = testServer.Listener.Addr().String()
}

func TestGzip(t *testing.T) {
	once.Do(startServer)
	req, err := http.NewRequest("GET", "http://"+addr+"/", nil)
	if err != nil {
		t.Fatalf("expected no error when creating request, but got %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("expected no error making request, but got %v", err)
	}
	defer resp.Body.Close()
	r, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("expected no error creating new gzip reader, but got %v", err)
	}
	defer r.Close()
	if _, err := ioutil.ReadAll(r); err != nil {
		t.Fatalf("expected no error reading gzipped response body, got %v", err)
	}
}

func TestGoImport(t *testing.T) {
	once.Do(startServer)
	resp, err := http.Get("http://" + addr + "/?go-get=1")
	if err != nil {
		t.Fatalf("expected no error, but got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status code to be %d, got %d", http.StatusOK, resp.StatusCode)
	}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("expected no error reading response body, got %v", err)
	}
	expected := fmt.Sprintf(`<meta name="go-import" content="%v git %v">`, sourceBase, sourceRepo)
	if strings.TrimSpace(string(b)) != expected {
		t.Errorf("expected response body to be %q, got %q", expected, b)
	}
}
