package depot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client is a client of the depots of an installation, at the one address
// that they share. A request is bounded only by its context, since an object
// may be of any size.
type Client struct {
	address string
	http    *http.Client
}

// At returns a client of the depots at address.
func At(address string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A connection carries one request: the cluster spreads connections over
	// the depots, not requests, and sends a depot that is being replaced no
	// new ones.
	transport.DisableKeepAlives = true
	return &Client{address: strings.TrimSuffix(address, "/"), http: &http.Client{Transport: transport}}
}

// Put writes the object of a new key from body and returns its size as the
// depot has it. It returns ErrExists if the key has an object, even one that
// was not written whole, and ErrFull if the depot has no room for the object.
func (c *Client) Put(ctx context.Context, key string, body io.Reader) (int64, error) {
	answer, err := c.ask(ctx, http.MethodPut, key, body)
	if err != nil {
		return 0, err
	}
	defer answer.Body.Close()
	var stored written
	if err := json.NewDecoder(io.LimitReader(answer.Body, 4<<10)).Decode(&stored); err != nil {
		return 0, fmt.Errorf("depot: PUT %s: %w", key, err)
	}
	return stored.Size, nil
}

// Get returns the object of the key and its size. The caller closes it, and
// reading it fails if the object ends before its size. It returns ErrNotFound
// if the key has no object.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	answer, err := c.ask(ctx, http.MethodGet, key, nil)
	if err != nil {
		return nil, 0, err
	}
	if answer.ContentLength < 0 {
		answer.Body.Close()
		return nil, 0, fmt.Errorf("depot: GET %s: the answer has no length", key)
	}
	return answer.Body, answer.ContentLength, nil
}

// Delete deletes the object of the key and every object whose key is under
// it. A key without objects is not an error.
func (c *Client) Delete(ctx context.Context, key string) error {
	answer, err := c.ask(ctx, http.MethodDelete, key, nil)
	if err != nil {
		return err
	}
	return answer.Body.Close()
}

// ask sends a request for the key and returns the answer if it is a success.
func (c *Client) ask(ctx context.Context, method, key string, body io.Reader) (*http.Response, error) {
	if !valid(key) {
		return nil, fmt.Errorf("depot: %s %q: %w", method, key, ErrKey)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.address+"/objects/"+key, body)
	if err != nil {
		return nil, err
	}
	answer, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("depot: %w", err)
	}
	if answer.StatusCode >= 200 && answer.StatusCode <= 299 {
		return answer, nil
	}
	defer answer.Body.Close()
	switch answer.StatusCode {
	case http.StatusNotFound:
		err = ErrNotFound
	case http.StatusConflict:
		err = ErrExists
	case http.StatusInsufficientStorage:
		err = ErrFull
	default:
		said, _ := io.ReadAll(io.LimitReader(answer.Body, 4<<10))
		err = fmt.Errorf("%d %s", answer.StatusCode, strings.TrimSpace(string(said)))
	}
	return nil, fmt.Errorf("depot: %s %s: %w", method, key, err)
}
