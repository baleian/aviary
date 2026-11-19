// Package sandbox asks OpenSandbox, on which the sessions of Aviary run, for
// what a session runs in: the sandbox of the session, which is made from the
// template of the harness of its agent.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// ErrNotFound is the error for what is asked for and is not there: a sandbox
// that is gone, a template that was not built, a turn that a runner does not
// have.
var ErrNotFound = errors.New("not found")

// Refusal is an answer that is no success, of the sandbox service or of a
// runner.
type Refusal struct {
	// Who answered, and what it was asked.
	Who, Asked string
	// Status is the HTTP status of the answer, and Said what it said.
	Status int
	Said   string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("%s answered %d %s to %s: %s", r.Who, r.Status, http.StatusText(r.Status), r.Asked, r.Said)
}

// Is makes an answer that something is not there an ErrNotFound.
func (r *Refusal) Is(target error) bool {
	return target == ErrNotFound && r.Status == http.StatusNotFound
}

// Client asks the lifecycle API of OpenSandbox.
type Client struct {
	address string
	key     string
	http    *http.Client
	// stream is what the runners are asked with: the answer to one who reads
	// the events of a turn takes as long as the turn.
	stream *http.Client
	// quiet is for how long a runner may send nothing to one who reads the
	// events of a turn.
	quiet time.Duration
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
		stream:  &http.Client{},
		quiet:   time.Minute,
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

// Sandbox is the sandbox of a session: a small virtual machine in which the
// runner and the harness of the session run.
type Sandbox struct {
	ID     string        `json:"id"`
	Status SandboxStatus `json:"status"`
}

// SandboxStatus is how the sandbox is.
type SandboxStatus struct {
	// State is Pending, Running, Pausing, Paused, Resuming, Stopping,
	// Terminated or Failed.
	State   string `json:"state"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// The states of a sandbox that tell what to do with it.
const (
	// Running is the state of a sandbox whose runner can be asked.
	Running = "Running"
	// Pending is the state of a sandbox that is being made.
	Pending = "Pending"
)

// kept is for how many seconds a sandbox is kept, which the service wants to
// be told for one that is made from a template: a hundred years, which is
// until it is deleted.
const kept = 100 * 365 * 24 * 60 * 60

// CreateSandbox has a sandbox made from the template, and kept until it is
// deleted. It is there at once, and takes a moment to run: longer on a node
// that has not run its template before. A template that is not there, or
// not built, is ErrNotFound.
func (c *Client) CreateSandbox(ctx context.Context, template string, metadata map[string]string) (Sandbox, error) {
	spec := struct {
		TemplateID string            `json:"templateId"`
		Timeout    int64             `json:"timeout"`
		Metadata   map[string]string `json:"metadata,omitempty"`
	}{template, kept, metadata}
	var sandbox Sandbox
	err := c.ask(ctx, http.MethodPost, "/sandboxes", nil, spec, &sandbox)
	return sandbox, err
}

// Sandbox returns the sandbox as it is now, or ErrNotFound if it is gone.
func (c *Client) Sandbox(ctx context.Context, id string) (Sandbox, error) {
	var sandbox Sandbox
	err := c.ask(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, nil, &sandbox)
	return sandbox, err
}

// DeleteSandbox has the sandbox deleted, with all that is in it. A sandbox
// that is gone already is no error.
func (c *Client) DeleteSandbox(ctx context.Context, id string) error {
	err := c.ask(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(id), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Runner returns the runner in the sandbox, as it is reached: through the
// ingress gateway of the sandbox service, with a route that the service
// signs. The service gives the route also for a sandbox that is gone.
func (c *Client) Runner(ctx context.Context, id string) (*Runner, error) {
	var endpoint struct {
		Endpoint string            `json:"endpoint"`
		Headers  map[string]string `json:"headers"`
	}
	path := "/sandboxes/" + url.PathEscape(id) + "/endpoints/" + strconv.Itoa(RunnerPort)
	if err := c.ask(ctx, http.MethodGet, path, nil, nil, &endpoint); err != nil {
		return nil, err
	}
	route := endpoint.Headers[routeHeader]
	if endpoint.Endpoint == "" || route == "" {
		return nil, fmt.Errorf("the sandbox service gave no way to the sandbox %s", id)
	}
	return &Runner{address: "http://" + endpoint.Endpoint, route: route, http: c.stream, quiet: c.quiet}, nil
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
		return &Refusal{
			Who: "the sandbox service", Asked: method + " " + path,
			Status: answer.StatusCode, Said: strings.TrimSpace(string(said)),
		}
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(said, into); err != nil {
		return fmt.Errorf("the sandbox service answered %s %s with what is no JSON: %w", method, path, err)
	}
	return nil
}
