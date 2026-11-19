package sandbox

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox/sandboxtest"
)

// installed starts a sandbox service that has the templates of the
// installation, and returns the template of its harness.
func installed(t *testing.T) (*sandboxtest.Service, *Client, string) {
	t.Helper()
	s := sandboxtest.New(t)
	c := client(t, s)
	if err := c.Bake(context.Background(), needed()); err != nil {
		t.Fatal(err)
	}
	template, err := c.Template(context.Background(), needed(), "opencode")
	if err != nil {
		t.Fatal(err)
	}
	return s, c, template
}

func TestASandboxIsMadeFromTheTemplateOfItsHarness(t *testing.T) {
	ctx := context.Background()
	s, c, template := installed(t)
	made, err := c.CreateSandbox(ctx, template, map[string]string{"aviary-session": "a session"}, 0)
	if err != nil || made.ID == "" || made.Status.State != Pending {
		t.Fatalf("the sandbox was made as %+v: %v", made, err)
	}
	if metadata := s.Sandboxes()[made.ID]; metadata["aviary-session"] != "a session" {
		t.Errorf("the sandbox was made with %v", metadata)
	}
	// It takes a moment to run.
	seen, err := c.Sandbox(ctx, made.ID)
	if err != nil || seen.ID != made.ID || seen.Status.State != Running {
		t.Fatalf("the sandbox is %+v: %v", seen, err)
	}
}

func TestAHarnessThatHasChangedHasNoTemplateUntilARelease(t *testing.T) {
	ctx := context.Background()
	s, c, _ := installed(t)
	changed := needed()
	changed.Harnesses["opencode"] = Harness{Image: "registry.example.com/opencode:1.1", Sandbox: Size{CPU: "1", Memory: "2Gi", Disk: "4Gi"}}
	if _, err := c.Template(ctx, changed, "opencode"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the template of a harness that has changed: %v", err)
	}
	// A release has it built, which takes a while.
	s.HoldTemplates()
	done := bake(c, changed)
	asked(t, s, 2)
	if _, err := c.Template(ctx, changed, "opencode"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the template that is being built: %v", err)
	}
	s.Ends("tpl-2", Succeeded, "")
	if err := outcome(t, done); err != nil {
		t.Fatal(err)
	}
	if template, err := c.Template(ctx, changed, "opencode"); err != nil || template != "tpl-2" {
		t.Fatalf("the template of the harness is %q: %v", template, err)
	}
	// A sandbox is not made from one that is not built, or not there.
	s.Ends("tpl-2", "Building", "")
	for _, template := range []string{"tpl-2", "tpl-none"} {
		if _, err := c.CreateSandbox(ctx, template, nil, 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("a sandbox of the template %s: %v", template, err)
		}
	}
}

func TestASandboxThatIsDeletedIsGone(t *testing.T) {
	ctx := context.Background()
	s, c, template := installed(t)
	made, err := c.CreateSandbox(ctx, template, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sandbox(ctx, made.ID); err != nil {
		t.Fatal(err)
	}
	runner, err := c.Runner(ctx, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Deleting it again is no error: it is gone, which was wanted.
	for range 2 {
		if err := c.DeleteSandbox(ctx, made.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Sandbox(ctx, made.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the sandbox that was deleted: %v", err)
	}
	if len(s.Sandboxes()) != 0 {
		t.Errorf("the service has the sandboxes %v", s.Sandboxes())
	}
	// The way to its runner is still given, and leads nowhere.
	var refusal *Refusal
	err = runner.Start(ctx, uuid.New(), Turn{Text: "hello"})
	if !errors.As(err, &refusal) || refusal.Status != http.StatusServiceUnavailable {
		t.Errorf("the runner of the sandbox that was deleted: %v", err)
	}
	again, err := c.Runner(ctx, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Cancel(ctx, uuid.New()); !errors.As(err, &refusal) || refusal.Status != http.StatusServiceUnavailable {
		t.Errorf("the runner of the sandbox that was deleted: %v", err)
	}
}

func TestASandboxThatSleepsIsWoken(t *testing.T) {
	ctx := context.Background()
	s, c, template := installed(t)
	made, err := c.CreateSandbox(ctx, template, nil, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if idle := s.Idle(made.ID); idle != 300 {
		t.Errorf("the sandbox is put to sleep after %d idle seconds", idle)
	}
	// One that is never put to sleep is made without a word about it.
	awake, err := c.CreateSandbox(ctx, template, nil, 0)
	if err != nil || s.Idle(awake.ID) != 0 {
		t.Errorf("a sandbox that is not to sleep is put to sleep after %d idle seconds: %v", s.Idle(awake.ID), err)
	}
	if _, err := c.Sandbox(ctx, made.ID); err != nil {
		t.Fatal(err)
	}
	runner, err := c.Runner(ctx, made.ID)
	if err != nil {
		t.Fatal(err)
	}

	s.Sleep(made.ID)
	if seen, err := c.Sandbox(ctx, made.ID); err != nil || seen.Status.State != Paused {
		t.Fatalf("the sandbox that sleeps is %+v: %v", seen, err)
	}
	// What is sent to it does not wake it.
	var refusal *Refusal
	if err := runner.Cancel(ctx, uuid.New()); !errors.As(err, &refusal) || refusal.Status != http.StatusServiceUnavailable {
		t.Errorf("the runner of a sandbox that sleeps: %v", err)
	}
	if seen, _ := c.Sandbox(ctx, made.ID); seen.Status.State != Paused || s.Woken(made.ID) != 0 {
		t.Errorf("the sandbox that was sent something is %+v, woken %d times", seen, s.Woken(made.ID))
	}
	if err := c.Resume(ctx, made.ID); err != nil {
		t.Fatal(err)
	}
	if seen, err := c.Sandbox(ctx, made.ID); err != nil || seen.Status.State != Running || s.Woken(made.ID) != 1 {
		t.Errorf("the sandbox that was woken is %+v, woken %d times: %v", seen, s.Woken(made.ID), err)
	}
	if err := runner.Cancel(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("the runner of the sandbox that was woken: %v", err)
	}
	// One that does not sleep is not woken.
	if err := c.Resume(ctx, made.ID); !errors.As(err, &refusal) || refusal.Status != http.StatusConflict {
		t.Errorf("waking a sandbox that runs: %v", err)
	}
}

func TestTheSandboxServiceIsAskedWithItsKey(t *testing.T) {
	ctx := context.Background()
	s, _, template := installed(t)
	stranger, err := New(s.URL, "another-key")
	if err != nil {
		t.Fatal(err)
	}
	var refusal *Refusal
	if _, err := stranger.CreateSandbox(ctx, template, nil, 0); !errors.As(err, &refusal) || refusal.Status != http.StatusUnauthorized {
		t.Errorf("a sandbox that is asked for with another key: %v", err)
	}
	if _, err := stranger.Runner(ctx, "sbx-1"); !errors.As(err, &refusal) || refusal.Status != http.StatusUnauthorized {
		t.Errorf("a runner that is asked for with another key: %v", err)
	}
}
