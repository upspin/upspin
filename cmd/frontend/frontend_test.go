// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var (
	testResponse = "ok"
	testDocPath  = "testdata/doc"

	testServer http.Handler
	addr       string
	once       sync.Once
)

func startServer() {
	*docPath = testDocPath
	s := newServer().(*server)
	s.mux.HandleFunc("/_test", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testResponse)
	})
	testServer := httptest.NewServer(s)
	addr = testServer.Listener.Addr().String()
}

func TestNoGzip(t *testing.T) {
	once.Do(startServer)
	req, err := http.NewRequest("GET", "http://"+addr+"/_test", nil)

	// Don’t ask for gzipped responses.
	req.Header.Set("Accept-Encoding", "")
	if err != nil {
		t.Fatalf("expected no error when creating request, but got %v", err)
	}
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

func get(t *testing.T, url string) []byte {
	resp, err := http.Get(url)
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
	b := get(t, "http://"+addr+"/?go-get=1")
	expected := fmt.Sprintf(`<meta name="go-import" content="%v git %v">`, sourceBase, sourceRepo)
	if strings.TrimSpace(string(b)) != expected {
		t.Errorf("expected response body to be %q, got %q", expected, b)
	}
}

func TestDocList(t *testing.T) {
	once.Do(startServer)
	b := get(t, "http://"+addr+"/")
	expected := `<a href="/doc/test.md">test.md</a>`
	if !strings.Contains(string(b), expected) {
		t.Errorf("expected response body to contain %q; body: %q", expected, b)
	}
}

func TestDoc(t *testing.T) {
	once.Do(startServer)
	b := get(t, "http://"+addr+"/doc/test.md")
	expected := `<h1>Test</h1>`
	if !strings.Contains(string(b), expected) {
		t.Errorf("expected response body to contain %q; body: %q", expected, b)
	}

	resp, err := http.Get("http://" + addr + "/doc/notfounddoc")
	if err != nil {
		t.Fatalf("expected no error, but got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status code to be %d, got %d", http.StatusNotFound, resp.StatusCode)
	}
}
