package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client is a simple JSON-over-HTTP client that speaks to a daemon's
// management API through a socket (Unix domain socket or Windows named pipe).
type Client struct {
	httpClient *http.Client
}

// NewClient creates a management Client for the socket at socketPath.
func NewClient(socketPath string) *Client {
	return &Client{
		httpClient: &http.Client{
			Transport: newSocketTransport(socketPath),
		},
	}
}

// Get performs a GET request to path and decodes the JSON response into out.
func (c *Client) Get(path string, out any) error {
	resp, err := c.httpClient.Get("http://localhost" + path)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Post performs a POST request with a JSON body to path.
// If out is non-nil the response body is decoded into it.
func (c *Client) Post(path string, body any, out any) error {
	pr, pw := io.Pipe()
	go func() {
		if err := json.NewEncoder(pw).Encode(body); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.Close()
	}()
	resp, err := c.httpClient.Post("http://localhost"+path, "application/json", pr)
	if err != nil {
		pr.Close() // unblock goroutine if still writing
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
