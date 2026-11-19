// Package sandbox asks OpenSandbox, on which the sessions of Aviary run, for
// what a session runs in: the sandbox of the session, which is made from the
// template of the harness of its agent.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// answerLimit is the most of an answer that is read. A list of templates is
// a few kilobytes.
const answerLimit = 4 << 20

// Client asks the lifecycle API of OpenSandbox.
type Client struct {
	address string
	key     string
	http    *http.Client
}

// New returns a client of the lifecycle API at the address, such as
// http://opensandbox-server.opensandbox-system, which it asks with the API
// key.
func New(address, key string) (*Client, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%q is not the address of a sandbox service", address)
	}
	return &Client{
		address: strings.TrimSuffix(address, "/") + "/v1",
		key:     key,
		http:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Template is what the sandboxes of a harness are made from: the image of
// the harness as a machine that was started, with the runner in it, and
// stopped where it waits for its first turn.
type Template struct {
	ID       string            `json:"templateId"`
	Metadata map[string]string `json:"metadata"`
	Status   TemplateStatus    `json:"status"`
}

// TemplateStatus is how far the building of a template is.
type TemplateStatus struct {
	// Phase is Pending, Building, Succeeded or Failed.
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

// The phases in which a template is not being built any more.
const (
	Succeeded = "Succeeded"
	Failed    = "Failed"
)

// TemplateSpec is what a template is built from.
type TemplateSpec struct {
	Image          string            `json:"image"`
	Entrypoint     []string          `json:"entrypoint"`
	Env            map[string]string `json:"env,omitempty"`
	ResourceLimits Size              `json:"resourceLimits"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Readiness      Readiness         `json:"readiness"`
	// Publish is where the template is kept, in the object store of the
	// sandbox service.
	Publish string `json:"publish"`
	Format  string `json:"format"`
}

// Size is how large the sandboxes of a template are, each a quantity as
// Kubernetes writes one: "1", "2Gi".
type Size struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	Disk   string `json:"disk"`
}

// Readiness tells when the machine of a template has started.
type Readiness struct {
	Probe string `json:"probe"`
}

// CreateTemplate has a template built. Building takes a while: the template
// that is returned is one that is not built yet.
func (c *Client) CreateTemplate(ctx context.Context, spec TemplateSpec) (Template, error) {
	var template Template
	err := c.ask(ctx, http.MethodPost, "/templates", nil, spec, &template)
	return template, err
}

// Templates returns the templates that have the metadata.
func (c *Client) Templates(ctx context.Context, name, value string) ([]Template, error) {
	var templates []Template
	for page := 1; ; page++ {
		var listed struct {
			Items      []Template `json:"items"`
			Pagination struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pagination"`
		}
		query := url.Values{"metadata": {name + "=" + value}, "page": {strconv.Itoa(page)}}
		if err := c.ask(ctx, http.MethodGet, "/templates", query, nil, &listed); err != nil {
			return nil, err
		}
		templates = append(templates, listed.Items...)
		if !listed.Pagination.HasNextPage {
			return templates, nil
		}
	}
}

// ask sends a request and reads the answer into into. An answer that is no
// success is an error that says what the service said.
func (c *Client) ask(ctx context.Context, method, path string, query url.Values, body, into any) error {
	var sent io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		sent = bytes.NewReader(encoded)
	}
	address := c.address + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, address, sent)
	if err != nil {
		return err
	}
	request.Header.Set("OPEN-SANDBOX-API-KEY", c.key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	answer, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("the sandbox service: %w", err)
	}
	defer answer.Body.Close()
	said, err := io.ReadAll(io.LimitReader(answer.Body, answerLimit))
	if err != nil {
		return fmt.Errorf("the sandbox service: %w", err)
	}
	if answer.StatusCode < 200 || answer.StatusCode > 299 {
		return fmt.Errorf("the sandbox service answered %s to %s %s: %s",
			answer.Status, method, path, strings.TrimSpace(string(said)))
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(said, into); err != nil {
		return fmt.Errorf("the sandbox service answered %s %s with what is no JSON: %w", method, path, err)
	}
	return nil
}
