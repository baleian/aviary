package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// service is the sandbox service of the tests: it keeps the templates that
// it is asked to build, and builds none until the test says how one ended.
type service struct {
	*httptest.Server
	mu        sync.Mutex
	templates []Template
	specs     []TemplateSpec
	// refuses is what the service answers instead, if it is set.
	refuses int
}

const key = "the-key-of-the-service"

func serve(t *testing.T) *service {
	t.Helper()
	s := &service{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/templates", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var spec TemplateSpec
		if r.Header.Get("OPEN-SANDBOX-API-KEY") != key || json.NewDecoder(r.Body).Decode(&spec) != nil {
			http.Error(w, `{"message": "who are you"}`, http.StatusUnauthorized)
			return
		}
		if s.refuses != 0 {
			http.Error(w, `{"message": "the store is not there"}`, s.refuses)
			return
		}
		template := Template{
			ID:       fmt.Sprintf("tpl-%d", len(s.templates)+1),
			Metadata: spec.Metadata,
			Status:   TemplateStatus{Phase: "Pending"},
		}
		s.templates = append(s.templates, template)
		s.specs = append(s.specs, spec)
		_ = json.NewEncoder(w).Encode(template)
	})
	mux.HandleFunc("GET /v1/templates", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		name, value, _ := strings.Cut(r.URL.Query().Get("metadata"), "=")
		var found []Template
		for _, template := range s.templates {
			if template.Metadata[name] == value {
				found = append(found, template)
			}
		}
		// Two to a page, to have more than one.
		page := 1
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		from := min((page-1)*2, len(found))
		to := min(from+2, len(found))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      found[from:to],
			"pagination": map[string]any{"hasNextPage": to < len(found)},
		})
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// ends sets how the building of the template ended.
func (s *service) ends(id, phase, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.templates {
		if s.templates[i].ID == id {
			s.templates[i].Status = TemplateStatus{Phase: phase, Message: message}
		}
	}
}

// asked waits until the service was asked for so many templates, and
// returns what they were to be built from.
func (s *service) asked(t *testing.T, count int) []TemplateSpec {
	t.Helper()
	for range 500 {
		s.mu.Lock()
		specs := slices.Clone(s.specs)
		s.mu.Unlock()
		if len(specs) >= count {
			return specs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the service was not asked for %d templates", count)
	return nil
}

func (s *service) client(t *testing.T) *Client {
	t.Helper()
	client, err := New(s.URL, key)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func needed() Templates {
	return Templates{
		Harnesses: map[string]Harness{
			"opencode": {Image: "registry.example.com/opencode:1.0", Sandbox: Size{CPU: "1", Memory: "2Gi", Disk: "4Gi"}},
		},
		Store:              "s3://sandboxes/templates",
		MaxToolOutputBytes: 65536,
		Poll:               5 * time.Millisecond,
	}
}

// bake bakes in the background and returns where the outcome comes.
func bake(client *Client, templates Templates) <-chan error {
	done := make(chan error, 1)
	go func() { done <- client.Bake(context.Background(), templates) }()
	return done
}

func outcome(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the baking did not end")
		return nil
	}
}

func TestATemplateIsBuiltFromTheHarnessAsTheInstallationSetsItUp(t *testing.T) {
	s := serve(t)
	done := bake(s.client(t), needed())
	spec := s.asked(t, 1)[0]
	digest := spec.Metadata[metadataTemplate]
	spec.Metadata = nil
	want := TemplateSpec{
		Image:          "registry.example.com/opencode:1.0",
		Entrypoint:     []string{"aviary-session"},
		Env:            map[string]string{"AVIARY_MAX_TOOL_OUTPUT_BYTES": "65536"},
		ResourceLimits: Size{CPU: "1", Memory: "2Gi", Disk: "4Gi"},
		Readiness:      Readiness{Probe: "tcp://127.0.0.1:8700"},
		Publish:        "s3://sandboxes/templates",
		Format:         "native",
	}
	if got, _ := json.Marshal(spec); len(digest) != 32 || string(got) != string(must(json.Marshal(want))) {
		t.Errorf("the template was to be built from %s, with the digest %q", got, digest)
	}
	// The baking waits for the template to be built.
	select {
	case err := <-done:
		t.Fatalf("the baking ended before the template was built: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	s.ends("tpl-1", "Building", "")
	s.ends("tpl-1", Succeeded, "")
	if err := outcome(t, done); err != nil {
		t.Fatal(err)
	}
}

func must(encoded []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return encoded
}

func TestATemplateThatIsThereIsNotBuiltAgain(t *testing.T) {
	s := serve(t)
	done := bake(s.client(t), needed())
	s.asked(t, 1)
	s.ends("tpl-1", Succeeded, "")
	if err := outcome(t, done); err != nil {
		t.Fatal(err)
	}
	// The same installation is set up again, and then with a harness more.
	if err := outcome(t, bake(s.client(t), needed())); err != nil {
		t.Fatal(err)
	}
	more := needed()
	more.Harnesses["codex"] = Harness{Image: "registry.example.com/codex:1.0", Sandbox: Size{CPU: "1", Memory: "1Gi", Disk: "4Gi"}}
	done = bake(s.client(t), more)
	specs := s.asked(t, 2)
	s.ends("tpl-2", Succeeded, "")
	if err := outcome(t, done); err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || specs[1].Metadata[metadataHarness] != "codex" {
		t.Errorf("the service was asked for %v", specs)
	}
}

func TestAHarnessThatHasChangedHasAnotherTemplate(t *testing.T) {
	first := needed()
	spec, err := first.spec("opencode")
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*Templates){
		"the image": func(t *Templates) {
			t.Harnesses["opencode"] = Harness{Image: "registry.example.com/opencode:1.1", Sandbox: t.Harnesses["opencode"].Sandbox}
		},
		"the size": func(t *Templates) {
			t.Harnesses["opencode"] = Harness{Image: t.Harnesses["opencode"].Image, Sandbox: Size{CPU: "2", Memory: "2Gi", Disk: "4Gi"}}
		},
		"the store":     func(t *Templates) { t.Store = "s3://elsewhere/templates" },
		"the tool text": func(t *Templates) { t.MaxToolOutputBytes = 0 },
	}
	for what, change := range changes {
		changed := needed()
		change(&changed)
		other, err := changed.spec("opencode")
		if err != nil {
			t.Fatal(err)
		}
		if other.Metadata[metadataTemplate] == spec.Metadata[metadataTemplate] {
			t.Errorf("a harness of which %s has changed has the same template", what)
		}
	}
	again, _ := needed().spec("opencode")
	if again.Metadata[metadataTemplate] != spec.Metadata[metadataTemplate] {
		t.Error("the same harness has another template")
	}
}

func TestATemplateThatIsBeingBuiltIsWaitedFor(t *testing.T) {
	s := serve(t)
	// Two set the installation up at once.
	first := bake(s.client(t), needed())
	s.asked(t, 1)
	second := bake(s.client(t), needed())
	time.Sleep(50 * time.Millisecond)
	s.ends("tpl-1", Succeeded, "")
	for _, done := range []<-chan error{first, second} {
		if err := outcome(t, done); err != nil {
			t.Fatal(err)
		}
	}
	if specs := s.asked(t, 1); len(specs) != 1 {
		t.Errorf("the template was built %d times", len(specs))
	}
}

func TestATemplateThatWasNotBuiltIsWhyTheBakingFails(t *testing.T) {
	s := serve(t)
	done := bake(s.client(t), needed())
	s.asked(t, 1)
	s.ends("tpl-1", Failed, "Error (exit code 1)")
	err := outcome(t, done)
	if err == nil || !strings.Contains(err.Error(), "opencode") || !strings.Contains(err.Error(), "tpl-1: Error (exit code 1)") {
		t.Fatalf("the baking ended with %v", err)
	}
	// The next time it is tried again, and the one that failed is left.
	done = bake(s.client(t), needed())
	s.asked(t, 2)
	s.ends("tpl-2", Succeeded, "")
	if err := outcome(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestWhatTheServiceRefusesIsWhyTheBakingFails(t *testing.T) {
	s := serve(t)
	s.refuses = http.StatusBadRequest
	err := outcome(t, bake(s.client(t), needed()))
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") || !strings.Contains(err.Error(), "the store is not there") {
		t.Fatalf("the baking ended with %v", err)
	}
	// A service that is asked with another key.
	stranger, _ := New(s.URL, "another-key")
	s.refuses = 0
	err = outcome(t, bake(stranger, needed()))
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("the baking with another key ended with %v", err)
	}
	if _, err := New("opensandbox-server", key); err == nil {
		t.Error("an address without a scheme was taken")
	}
}

func TestTheTemplatesOfALongListAreAllRead(t *testing.T) {
	s := serve(t)
	client := s.client(t)
	for range 5 {
		if _, err := client.CreateTemplate(context.Background(), TemplateSpec{Metadata: map[string]string{"of": "many"}}); err != nil {
			t.Fatal(err)
		}
	}
	found, err := client.Templates(context.Background(), "of", "many")
	if err != nil || len(found) != 5 {
		t.Fatalf("%d templates were read: %v", len(found), err)
	}
}
