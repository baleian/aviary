package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// resolve asks the Supervisor about a request of the sandbox to the URL, as
// the egress of the sandbox does: where the Supervisor told the egress to
// ask. It returns the status and the headers of the answer.
func (s *supervisor) resolve(sandbox, method, target string) (int, map[string]string) {
	s.t.Helper()
	var vault struct {
		Credentials []struct{ Source struct{ Endpoint string } }
	}
	if err := json.Unmarshal(s.service.Vault(sandbox), &vault); err != nil || len(vault.Credentials) != 1 {
		s.t.Fatalf("the egress of the sandbox %s was given %s: %v", sandbox, s.service.Vault(sandbox), err)
	}
	endpoint, err := url.Parse(vault.Credentials[0].Source.Endpoint)
	if err != nil || !strings.HasPrefix(endpoint.String(), resolvedAt+"/") {
		s.t.Fatalf("the egress asks at %s", vault.Credentials[0].Source.Endpoint)
	}
	request, err := url.Parse(target)
	if err != nil {
		s.t.Fatal(err)
	}
	question, err := json.Marshal(map[string]any{
		"subject":     sandbox,
		"destination": map[string]string{"scheme": request.Scheme, "authority": request.Host + ":443"},
		"request":     map[string]any{"method": method, "host": request.Host, "path": request.RequestURI()},
	})
	if err != nil {
		s.t.Fatal(err)
	}
	answer, err := http.Post(s.resolver+endpoint.Path+"/resolve", "application/json", bytes.NewReader(question))
	if err != nil {
		s.t.Fatal(err)
	}
	defer answer.Body.Close()
	var answered struct{ Headers map[string]string }
	if answer.StatusCode == http.StatusOK {
		if err := json.NewDecoder(answer.Body).Decode(&answered); err != nil {
			s.t.Fatal(err)
		}
	}
	return answer.StatusCode, answered.Headers
}

// The egress of the sandbox service asks a Supervisor about the requests of
// a sandbox while a turn runs in it. A request to the model endpoint of the
// turn's agent gets the key of the endpoint, and no other request does.
func TestTheEgressIsAnsweredAboutTheRequestsOfASandbox(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session()
	accepted := s.send(session, "hello")
	runner := aviary.given(accepted.ID)

	// Another Supervisor answers as well as the one that follows the turn.
	other := aviary.supervisor()
	for _, asked := range []*supervisor{s, other} {
		status, headers := asked.resolve(runner.Sandbox, "POST", "https://api.example.com/v1/chat/completions")
		if status != http.StatusOK || headers["authorization"] != "Bearer sk-a-key" {
			t.Errorf("a model request was answered with %d and the headers %v", status, headers)
		}
		status, headers = asked.resolve(runner.Sandbox, "GET", "https://pypi.org/simple/")
		if status != http.StatusOK || len(headers) != 0 {
			t.Errorf("a request to another host was answered with %d and the headers %v", status, headers)
		}
	}

	// Once the turn has ended, no request of the sandbox is let through.
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))
	if status, headers := s.resolve(runner.Sandbox, "POST", "https://api.example.com/v1/chat/completions"); status != http.StatusForbidden || headers != nil {
		t.Errorf("a model request after the turn was answered with %d and the headers %v", status, headers)
	}
}
