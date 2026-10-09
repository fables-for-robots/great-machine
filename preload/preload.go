// Package preload is the client of the preload endpoint of the
// jaccard-nfs-nix-store sidecar: it sends the store paths of a closure,
// which the sidecar fetches before anything looks them up.
package preload

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// storeDir is where a Nix store is: what the name of a store path is put
// behind to make a line of the list.
const storeDir = "/nix/store"

// maxBody is how much of an answer that is not 200 is kept.
const maxBody = 4 << 10

// Client posts lists of store paths to a preload endpoint.
type Client struct {
	// URL is the preload endpoint of the sidecar, such as
	// http://127.0.0.1:9889/v1/preload.
	URL string
	// HTTP is the client the request is made with. nil: http.DefaultClient.
	HTTP *http.Client
}

// Error is an answer of the endpoint that is not 200: 404 for a store path
// the server does not have, 502 for one that could not be fetched, 400 for
// a list the endpoint could not read.
type Error struct {
	// Status is the HTTP status of the answer.
	Status int
	// Body is the first 4 KiB of the answer, in which the endpoint names
	// the store path and says what is wrong with it. It is valid UTF-8.
	Body string
}

// Error returns the status and what the endpoint said.
func (e *Error) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("the preload endpoint answered %d", e.Status)
	}
	return fmt.Sprintf("the preload endpoint answered %d: %s", e.Status, body)
}

// Preload posts /nix/store/<name> for every name, one on a line, and
// returns nil for an answer of 200. Any other answer is returned as an
// *Error. A request that could not be made, or that ctx ended, is an error
// of another kind, which names the endpoint.
func (c Client) Preload(ctx context.Context, names []string) error {
	var list strings.Builder
	for _, name := range names {
		list.WriteString(storeDir + "/" + name + "\n")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(list.String()))
	if err != nil {
		return fmt.Errorf("preload: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The error of net/http names the method and the URL.
		return fmt.Errorf("preload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil && len(body) == 0 {
		return fmt.Errorf("preload: reading the answer of %s, which is %d: %w", c.URL, resp.StatusCode, err)
	}
	// The cut may have gone through a character. "?" is no longer than
	// what it stands for, so the body stays within its 4 KiB.
	return &Error{Status: resp.StatusCode, Body: strings.ToValidUTF8(string(body), "?")}
}
