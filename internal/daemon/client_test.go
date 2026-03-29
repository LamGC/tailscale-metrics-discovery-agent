package daemon

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPost_EncodableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	c := &Client{httpClient: srv.Client()}
	// Override transport to point at test server.
	c.httpClient.Transport = &http.Transport{}

	// Use the test server URL directly.
	pr, pw := io.Pipe()
	go func() {
		json.NewEncoder(pw).Encode(map[string]string{"key": "value"})
		pw.Close()
	}()
	resp, err := c.httpClient.Post(srv.URL+"/test", "application/json", pr)
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	resp.Body.Close()
}

func TestPost_UnEncodableBody_DoesNotHang(t *testing.T) {
	// math.NaN() cannot be JSON-encoded; verify the pipe doesn't hang.
	done := make(chan struct{})
	go func() {
		pr, pw := io.Pipe()
		go func() {
			if err := json.NewEncoder(pw).Encode(math.NaN()); err != nil {
				pw.CloseWithError(err)
				return
			}
			pw.Close()
		}()
		// Read from the pipe to see if it errors (not hangs).
		_, _ = io.ReadAll(pr)
		close(done)
	}()

	select {
	case <-done:
		// ok — did not hang
	case <-time.After(5 * time.Second):
		t.Fatal("pipe read hung on un-encodable body")
	}
}
