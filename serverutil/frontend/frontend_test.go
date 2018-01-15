// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package frontend

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

var (
	testResponse = "ok"
	testDocPath  = "testdata/doc"

	addr string
	once sync.Once
)

func startServer() {
	s, err := newServer(nil, testDocPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.Handle("/", s)
	mux.Handle("/_test", canonicalHostHandler{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testResponse)
	})})
	testServer := httptest.NewServer(mux)
	addr = testServer.Listener.Addr().String()
}

var noRedirectClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func TestHostnameRedirect(t *testing.T) {
	once.Do(startServer)
	req, _ := http.NewRequest("GET", "http://"+addr+"/_test", nil)
	req.Host = "foo." + docHostname
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error making request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected status code to be %v, got %v", http.StatusFound, resp.StatusCode)
	}
}

func TestNoGzip(t *testing.T) {
	once.Do(startServer)
	req, err := http.NewRequest("GET", "http://"+addr+"/_test", nil)
	if err != nil {
		t.Fatalf("expected no error when creating request, but got %v", err)
	}

	// Don’t ask for gzipped responses.
	req.Header.Set("Accept-Encoding", "")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("expected no error making request, but got %v", err)
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("expected no error reading gzipped response body, got %v", err)
	}
	if string(b) != testResponse {
		t.Errorf("expected response body to be %q, got %q", testResponse, b)
	}
}

const defaultHost = "upspin.io"

func get(t *testing.T, url string) []byte {
	return getHost(t, defaultHost, url)
}

func getHost(t *testing.T, host, url string) []byte {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("expected no error, but got %v", err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
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
	return b
}

func TestGoImport(t *testing.T) {
	once.Do(startServer)
	for host := range sourceRepo {
		b := getHost(t, host, "http://"+addr+"/?go-get=1")
		expected := fmt.Sprintf(`<meta name="go-import" content="%v git %v">`, host, sourceRepo[host])
		if strings.TrimSpace(string(b)) != expected {
			t.Errorf("expected response body to be %q, got %q", expected, b)
		}
	}
}

func TestIndex(t *testing.T) {
	once.Do(startServer)
	for _, p := range []string{
		"/",
		"/doc/index.md",
	} {
		b := get(t, "http://"+addr+p)
		expected := `<h1>Index</h1>`
		if !strings.Contains(string(b), expected) {
			t.Errorf("expected response body to contain %q; body: %q", expected, b)
		}
		expected = `<title>Index · Upspin</title>`
		if !strings.Contains(string(b), expected) {
			t.Errorf("expected response body to contain %q; body: %q", expected, b)
		}
	}
}

func TestDocRoot(t *testing.T) {
	once.Do(startServer)
	for _, p := range []string{
		"/doc",
		"/doc/",
		"/doc/doc.md",
	} {
		b := get(t, "http://"+addr+p)
		expected := `<h1>Documentation</h1>`
		if !strings.Contains(string(b), expected) {
			t.Errorf("expected response body to contain %q; body: %q", expected, b)
		}
		expected = `<title>Documentation · Upspin</title>`
		if !strings.Contains(string(b), expected) {
			t.Errorf("expected response body to contain %q; body: %q", expected, b)
		}
	}
}

func TestDoc(t *testing.T) {
	once.Do(startServer)

	b := get(t, "http://"+addr+"/doc/test.md")
	expected := `<h1>Test</h1>`
	if !strings.Contains(string(b), expected) {
		t.Errorf("expected response body to contain %q; body: %q", expected, b)
	}
	expected = `<title>Test · Upspin</title>`
	if !strings.Contains(string(b), expected) {
		t.Errorf("expected response body to contain %q; body: %q", expected, b)
	}

	resp, err := http.Get("http://" + addr + "/doc/notfounddoc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status code to be %d, got %d", http.StatusNotFound, resp.StatusCode)
	}
}
