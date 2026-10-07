package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchDownload_NoAuthorization(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()

	d, err := FetchDownload(context.Background(), srv.URL+"/f.csv?sig=x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header sent to storage host: %q", gotAuth)
	}
	if string(d.Body) != "body" || d.ContentType != "application/zip" {
		t.Errorf("unexpected download: %+v", d)
	}
}

func TestFetchDownload_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := FetchDownload(context.Background(), srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("expected HTTP 403 error, got %v", err)
	}
}
