package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

const (
	// RunnerPort is where the runner in a sandbox listens.
	RunnerPort = 8700
	// What the template of a harness is found by: the name of the harness,
	// and a digest of what the template was built from.
	metadataHarness  = "aviary-harness"
	metadataTemplate = "aviary-template"
)

// session is what a sandbox runs, which the image of a harness has.
var session = []string{"aviary-session"}

// Harness is a harness as the installation sets it up.
type Harness struct {
	// Image is the image of the harness, in a registry that the sandbox
	// service takes it from.
	Image string `json:"image"`
	// Sandbox is the size of the sandboxes of its sessions.
	Sandbox Size `json:"sandbox"`
}

// Templates are the templates that an installation needs: one for each of
// its harnesses.
type Templates struct {
	Harnesses map[string]Harness
	// Store is where the sandbox service keeps the templates, such as
	// s3://sandboxes/templates.
	Store string
	// MaxToolOutputBytes is the longest text of a tool call that the runner
	// tells as it is. Zero is no limit.
	MaxToolOutputBytes int
	// Poll is how often a template that is being built is looked at.
	Poll time.Duration
}

// spec is what the template of the harness is built from, and the digest of
// that: a harness whose image or size has changed has another template.
func (t Templates) spec(name string) (TemplateSpec, error) {
	harness := t.Harnesses[name]
	spec := TemplateSpec{
		Image:          harness.Image,
		Entrypoint:     session,
		Env:            map[string]string{"AVIARY_MAX_TOOL_OUTPUT_BYTES": strconv.Itoa(t.MaxToolOutputBytes)},
		ResourceLimits: harness.Sandbox,
		Readiness:      Readiness{Probe: "tcp://127.0.0.1:" + strconv.Itoa(RunnerPort)},
		Publish:        t.Store,
		Format:         "native",
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return spec, err
	}
	digest := sha256.Sum256(encoded)
	spec.Metadata = map[string]string{
		metadataHarness:  name,
		metadataTemplate: hex.EncodeToString(digest[:16]),
	}
	return spec, nil
}

// Template returns the template of the harness as the installation is set
// up, which a release has had built: ErrNotFound if none has.
func (c *Client) Template(ctx context.Context, templates Templates, name string) (string, error) {
	spec, err := templates.spec(name)
	if err != nil {
		return "", err
	}
	found, err := c.Templates(ctx, metadataTemplate, spec.Metadata[metadataTemplate])
	if err != nil {
		return "", fmt.Errorf("the template of the harness %s: %w", name, err)
	}
	for _, template := range found {
		if template.Status.Phase == Succeeded {
			return template.ID, nil
		}
	}
	return "", fmt.Errorf("the template of the harness %s: %w", name, ErrNotFound)
}

// Bake has the templates built that the installation needs and does not
// have, and returns when all of them are built. A template that is being
// built already is waited for, and not built again.
func (c *Client) Bake(ctx context.Context, templates Templates) error {
	if templates.Poll == 0 {
		templates.Poll = 2 * time.Second
	}
	built := make(chan error, len(templates.Harnesses))
	for name := range templates.Harnesses {
		go func() { built <- c.bake(ctx, templates, name) }()
	}
	var failed []error
	for range templates.Harnesses {
		failed = append(failed, <-built)
	}
	return errors.Join(failed...)
}

func (c *Client) bake(ctx context.Context, templates Templates, name string) error {
	spec, err := templates.spec(name)
	if err != nil {
		return err
	}
	digest := spec.Metadata[metadataTemplate]
	// The template that this run asked for, once it has.
	mine := ""
	for {
		found, err := c.Templates(ctx, metadataTemplate, digest)
		if err != nil {
			return fmt.Errorf("the template of the harness %s: %w", name, err)
		}
		building := false
		for _, template := range found {
			switch template.Status.Phase {
			case Succeeded:
				slog.Info("template is there", "harness", name, "template", template.ID)
				return nil
			case Failed:
				// One of an earlier run is tried again.
				if template.ID == mine {
					return fmt.Errorf("the template of the harness %s was not built: %s: %s",
						name, template.ID, template.Status.Message)
				}
			default:
				building = true
			}
		}
		if !building {
			if mine != "" {
				return fmt.Errorf("the template of the harness %s is gone: %s", name, mine)
			}
			template, err := c.CreateTemplate(ctx, spec)
			if err != nil {
				return fmt.Errorf("the template of the harness %s: %w", name, err)
			}
			mine = template.ID
			slog.Info("template is being built", "harness", name, "template", mine, "image", spec.Image)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the template of the harness %s: %w", name, ctx.Err())
		case <-time.After(templates.Poll):
		}
	}
}
