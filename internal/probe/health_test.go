package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbeOneUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	got := probeOne(context.Background(), newHTTPClient(), srv.URL)
	if !got.OK || got.Status != 204 {
		t.Fatalf("health = %+v, want ok with 204", got)
	}
	if got.Error != "" {
		t.Fatalf("error = %q, want none", got.Error)
	}
	if got.LatencyMS <= 0 {
		t.Fatalf("latency = %v, want a positive measurement", got.LatencyMS)
	}
	if got.URL != srv.URL {
		t.Fatalf("url = %q", got.URL)
	}
}

func TestProbeOneDownOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	got := probeOne(context.Background(), newHTTPClient(), srv.URL)
	if got.OK || got.Status != 500 {
		t.Fatalf("health = %+v, want down with 500", got)
	}
}

func TestProbeOneDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.WriteHeader(200)
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer srv.Close()

	got := probeOne(context.Background(), newHTTPClient(), srv.URL+"/healthz")
	if got.OK {
		t.Fatalf("health = %+v, want down: a redirect to a login page is not health", got)
	}
	if got.Status != 302 {
		t.Fatalf("status = %d, want the 302 itself", got.Status)
	}
}

func TestProbeOneConnectionRefusedCarriesTheMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	got := probeOne(context.Background(), newHTTPClient(), url)
	if got.OK {
		t.Fatal("health ok = true against a closed server")
	}
	if !strings.Contains(got.Error, "connect") && !strings.Contains(got.Error, "refused") {
		t.Fatalf("error = %q, want the transport failure verbatim", got.Error)
	}
}

func TestProbeOneTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	c := newHTTPClient()
	c.Timeout = 100 * time.Millisecond

	got := probeOne(context.Background(), c, srv.URL)
	if got.OK || got.Error == "" {
		t.Fatalf("health = %+v, want a timeout failure", got)
	}
}

func TestCollectHealthProbesEveryURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	got := collectHealth(context.Background(), newHTTPClient(), map[string]string{
		"a": srv.URL,
		"b": srv.URL + "/other",
	})
	if len(got) != 2 || !got["a"].OK || !got["b"].OK {
		t.Fatalf("health = %+v, want both up", got)
	}
}

func TestCollectHealthWithNothingConfigured(t *testing.T) {
	if got := collectHealth(context.Background(), newHTTPClient(), nil); len(got) != 0 {
		t.Fatalf("health = %+v, want empty", got)
	}
}
