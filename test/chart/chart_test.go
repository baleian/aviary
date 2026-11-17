// Package chart checks what the chart makes of its values. `make test-chart`
// renders the chart with its own values and with those of the files in this
// directory, and tells the tests where the results are.
package chart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"aviary/test/internal/rendered"
)

// chart returns the objects that the chart makes of the values with the
// name, and the configuration of the Egress Proxy among them.
func chart(t *testing.T, values string) (objects []map[string]any, envoy map[string]any) {
	t.Helper()
	directory := os.Getenv("AVIARY_TEST_RENDERED")
	if directory == "" {
		// A test that was skipped here would look like one that passed.
		t.Fatal("AVIARY_TEST_RENDERED is not set: run the tests with `make test-chart`")
	}
	objects, err := rendered.Read(filepath.Join(directory, values+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	envoy, _, err = rendered.Envoy(objects)
	if err != nil {
		t.Fatal(err)
	}
	return objects, envoy
}

var at, named = rendered.At, rendered.Named

// listener returns the settings of the HTTP connection manager of the
// proxy's listener with the name.
func listener(envoy map[string]any, name string) any {
	listener := named(at(envoy, "static_resources", "listeners"), name)
	return at(listener, "filter_chains", 0, "filters", 0, "typed_config")
}

// env returns the variables of the first container of a workload.
func env(workload map[string]any) map[string]string {
	env := map[string]string{}
	variables, _ := at(workload, "spec", "template", "spec", "containers", 0, "env").([]any)
	for _, variable := range variables {
		name, _ := at(variable, "name").(string)
		env[name], _ = at(variable, "value").(string)
	}
	return env
}

func TestEgressProxy(t *testing.T) {
	for _, values := range []string{"default", "set", "closed"} {
		_, envoy := chart(t, values)
		for _, name := range []string{"llm", "forward"} {
			// Without it, a request that the proxy could not ask a
			// Supervisor about is answered as forbidden, and a harness takes
			// that for an answer and does not ask again.
			check := named(at(listener(envoy, name), "http_filters"), "envoy.filters.http.ext_authz")
			if code := at(check, "typed_config", "status_on_error", "code"); code != "ServiceUnavailable" {
				t.Errorf("%s: a request on %s that no Supervisor can be asked about is answered with %v", values, name, code)
			}
		}
		// Envoy's own interface can stop Envoy and show its configuration.
		if socket := at(envoy, "admin", "address", "socket_address"); socket != nil {
			t.Errorf("%s: Envoy's own interface listens on %v, where the forward proxy can be asked to connect", values, socket)
		}
		routes := at(listener(envoy, "metrics"), "route_config", "virtual_hosts", 0, "routes")
		if list, _ := routes.([]any); len(list) != 1 || at(routes, 0, "match", "path") != "/metrics" {
			t.Errorf("%s: the metrics listener passes on more than the metrics: %v", values, routes)
		}
	}
}

// A turn may run for as long as the installation says, and a request of it
// may be silent for that long: a model that thinks, a download.
func TestEgressProxyKeepsARequestForAsLongAsATurn(t *testing.T) {
	for values, longest := range map[string]string{"default": "1800s", "set": "7200s"} {
		objects, envoy := chart(t, values)
		for _, name := range []string{"llm", "forward"} {
			if idle := at(listener(envoy, name), "stream_idle_timeout"); idle != longest {
				t.Errorf("%s: a silent request on %s is ended after %v, not after %s", values, name, idle, longest)
			}
		}
		if values == "set" {
			route := at(listener(envoy, "llm"), "route_config", "virtual_hosts", 0, "routes", 0, "route")
			if timeout := at(route, "timeout"); timeout != "0s" {
				t.Errorf("a request to the model is ended after %v", timeout)
			}
		}
		// A pod that is to stop does not wait for the turns that use it.
		for _, name := range []string{"aviary-egress-proxy", "aviary-supervisor"} {
			pod := at(rendered.Find(objects, "Deployment", name), "spec", "template", "spec")
			if grace := at(pod, "terminationGracePeriodSeconds"); grace != nil {
				t.Errorf("%s: a pod of %s is given %v seconds to stop", values, name, grace)
			}
		}
	}
}

func TestModelEndpoint(t *testing.T) {
	// llm returns the first route of the listener for the model endpoint.
	llm := func(envoy map[string]any) any {
		return at(listener(envoy, "llm"), "route_config", "virtual_hosts", 0, "routes", 0)
	}

	// Without a model endpoint the proxy says so.
	_, none := chart(t, "default")
	if status := at(llm(none), "direct_response", "status"); status != 503 {
		t.Errorf("without a model endpoint, a model request is answered with %v", status)
	}
	if named(at(none, "static_resources", "clusters"), "llm") != nil {
		t.Error("without a model endpoint, the proxy has one to pass requests on to")
	}

	// The endpoint is reached under the path of its URL, over TLS, and the
	// API key is put where the turn token was.
	_, set := chart(t, "set")
	route := llm(set)
	if at(route, "match", "path_separated_prefix") != "/v1" || at(route, "route", "host_rewrite_literal") != "llm.example.com" {
		t.Errorf("a model request is passed on by the route %v", route)
	}
	cluster := named(at(set, "static_resources", "clusters"), "llm")
	address := at(cluster, "load_assignment", "endpoints", 0, "lb_endpoints", 0, "endpoint", "address", "socket_address")
	if at(address, "address") != "llm.example.com" || at(address, "port_value") != 443 || at(cluster, "transport_socket") == nil {
		t.Errorf("the model endpoint is reached at %v, with the transport %v", address, at(cluster, "transport_socket"))
	}
	if named(at(listener(set, "llm"), "http_filters"), "envoy.filters.http.credential_injector") == nil {
		t.Error("the proxy does not put the API key into a model request")
	}

	// The model endpoint is a host like any other: the list has to allow it.
	_, closed := chart(t, "closed")
	if status := at(llm(closed), "direct_response", "status"); status != 403 {
		t.Errorf("a model request to an endpoint that is not allowed is answered with %v", status)
	}
}

func TestAllowedHosts(t *testing.T) {
	rules := func(envoy map[string]any) any {
		return at(named(at(listener(envoy, "forward"), "http_filters"), "envoy.filters.http.rbac"), "typed_config", "rules")
	}
	// The default lets every host through: there is no list to hold a
	// request against.
	if _, open := chart(t, "default"); rules(open) != nil {
		t.Errorf("with every host allowed, the proxy holds requests against %v", rules(open))
	}

	_, set := chart(t, "set")
	var hosts []string
	permissions, _ := at(rules(set), "policies", "listed-hosts", "permissions").([]any)
	for _, permission := range permissions {
		host, _ := at(permission, "header", "string_match", "exact").(string)
		hosts = append(hosts, host)
		if at(permission, "header", "string_match", "ignore_case") != true {
			t.Errorf("the host %s is allowed only as it is written", host)
		}
	}
	// A host stands for itself on the ports of HTTPS and HTTP, and a host
	// with a port for that port alone.
	expected := []string{
		"llm.example.com", "llm.example.com:443", "llm.example.com:80",
		"packages.example.com", "packages.example.com:443", "packages.example.com:80",
		"git.example.com:8443",
	}
	if !slices.Equal(hosts, expected) || at(rules(set), "action") != "ALLOW" {
		t.Errorf("the proxy lets requests through to %v, not to %v", hosts, expected)
	}
}

// What the Supervisor is told of how to run turns.
func TestSupervisorSettings(t *testing.T) {
	objects, _ := chart(t, "default")
	settings := env(rendered.Find(objects, "Deployment", "aviary-supervisor"))
	for name, value := range map[string]string{
		"AVIARY_GATEWAY_URL":            "http://aviary-supervisor.aviary-system:9090",
		"AVIARY_ENCRYPTION_KEY":         "k1",
		"AVIARY_LLM_BASE_URL":           "http://aviary-egress-proxy.aviary-system:8080",
		"AVIARY_LLM_MODEL":              "",
		"AVIARY_PROXY_URL":              "http://aviary-egress-proxy.aviary-system:3128",
		"AVIARY_RUNNER_IMAGE":           "aviary-runner:dev",
		"AVIARY_MAX_CONCURRENT_TURNS":   "50",
		"AVIARY_MAX_QUEUED_TURNS":       "500",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS": "120",
		"AVIARY_MAX_STARTUP_SECONDS":    "90",
		"AVIARY_MAX_TURN_SECONDS":       "1800",
		"AVIARY_MAX_RECONNECT_SECONDS":  "60",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":  "65536",
	} {
		if settings[name] != value {
			t.Errorf("the Supervisor is told %s=%q, not %q", name, settings[name], value)
		}
	}

	objects, _ = chart(t, "set")
	supervisor := rendered.Find(objects, "Deployment", "aviary-supervisor")
	settings = env(supervisor)
	for name, value := range map[string]string{
		"AVIARY_ENCRYPTION_KEY": "k2",
		// The harnesses reach the model under the path of its URL.
		"AVIARY_LLM_BASE_URL":           "http://aviary-egress-proxy.aviary-system:8080/v1",
		"AVIARY_LLM_MODEL":              "a-model",
		"AVIARY_MAX_CONCURRENT_TURNS":   "7",
		"AVIARY_MAX_QUEUED_TURNS":       "11",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS": "13",
		"AVIARY_MAX_STARTUP_SECONDS":    "17",
		"AVIARY_MAX_TURN_SECONDS":       "7200",
		"AVIARY_MAX_RECONNECT_SECONDS":  "19",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":  "23",
	} {
		if settings[name] != value {
			t.Errorf("the Supervisor is told %s=%q, not %q", name, settings[name], value)
		}
	}
	if replicas := at(supervisor, "spec", "replicas"); replicas != 3 {
		t.Errorf("the chart runs %v Supervisors, not 3", replicas)
	}
	if replicas := at(rendered.Find(objects, "Deployment", "aviary-egress-proxy"), "spec", "replicas"); replicas != 2 {
		t.Errorf("the chart runs %v Egress Proxies, not 2", replicas)
	}

	// A value that is set for one harness leaves the rest of the harnesses
	// as they are.
	var harnesses map[string]struct {
		Image     string
		Resources struct{ Requests, Limits map[string]string }
	}
	if err := json.Unmarshal([]byte(settings["AVIARY_HARNESSES"]), &harnesses); err != nil {
		t.Fatalf("the Supervisor is told the harnesses %s: %v", settings["AVIARY_HARNESSES"], err)
	}
	opencode, codex := harnesses["opencode"], harnesses["codex"]
	if len(harnesses) != 2 || opencode.Image != "aviary-harness-opencode:dev" || codex.Image != "aviary-harness-codex:dev" ||
		opencode.Resources.Requests["memory"] != "333Mi" || opencode.Resources.Requests["cpu"] != "250m" ||
		opencode.Resources.Limits["memory"] != "4Gi" || codex.Resources.Requests["memory"] != "256Mi" {
		t.Errorf("the Supervisor is told the harnesses %s", settings["AVIARY_HARNESSES"])
	}
}

// The Supervisor finds the keys that it seals secrets with where it is told
// that they are: the entries of the secret that the values name.
func TestSupervisorEncryptionKeys(t *testing.T) {
	for values, secret := range map[string]string{"default": "aviary-encryption", "set": "other-keys"} {
		objects, _ := chart(t, values)
		supervisor := rendered.Find(objects, "Deployment", "aviary-supervisor")
		pod := at(supervisor, "spec", "template", "spec")
		mount := named(at(pod, "containers", 0, "volumeMounts"), "encryption")
		told := env(supervisor)["AVIARY_ENCRYPTION_KEYS"]
		if told == "" || at(mount, "mountPath") != told || at(mount, "readOnly") != true {
			t.Errorf("%s: the Supervisor is told that the keys are in %q, and they are mounted as %v", values, told, mount)
		}
		if mounted := at(named(at(pod, "volumes"), "encryption"), "secret", "secretName"); mounted != secret {
			t.Errorf("%s: the keys are those of the secret %v, not of %s", values, mounted, secret)
		}
	}
}

// A turn pod connects to the Supervisor and to the Egress Proxy, and to
// nothing else: not even to the cluster's DNS, which can carry data out.
func TestTurnPodsAreCutOff(t *testing.T) {
	objects, _ := chart(t, "default")
	policy := at(rendered.Find(objects, "NetworkPolicy", "aviary-turn-pods"), "spec")
	if at(policy, "podSelector") == nil || len(at(policy, "podSelector").(map[string]any)) != 0 ||
		!reflect.DeepEqual(at(policy, "policyTypes"), []any{"Ingress", "Egress"}) || at(policy, "ingress") != nil {
		t.Errorf("the policy of the turn pods is %v", policy)
	}
	reached := map[string][]any{}
	rules, _ := at(policy, "egress").([]any)
	for _, rule := range rules {
		peers, _ := at(rule, "to").([]any)
		if len(peers) != 1 || at(peers[0], "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name") != "aviary-system" {
			t.Errorf("a turn pod may connect to %v", peers)
			continue
		}
		component, _ := at(peers[0], "podSelector", "matchLabels", "app.kubernetes.io/component").(string)
		ports, _ := at(rule, "ports").([]any)
		for _, port := range ports {
			reached[component] = append(reached[component], at(port, "port"))
		}
	}
	expected := map[string][]any{"supervisor": {9090}, "egress-proxy": {8080, 3128}}
	if !reflect.DeepEqual(reached, expected) {
		t.Errorf("a turn pod may connect to %v, not to %v", reached, expected)
	}

	// Kubernetes holds the turn pods to what a restricted namespace allows.
	labels := at(rendered.Find(objects, "Namespace", "aviary-runtime"), "metadata", "labels")
	if at(labels, "pod-security.kubernetes.io/enforce") != "restricted" {
		t.Errorf("the namespace of the turn pods has the labels %v", labels)
	}
}

// The turn pods reach the outside through the proxy, so the proxy must not
// be a way into the cluster.
func TestEgressProxyIsKeptFromTheInside(t *testing.T) {
	// outside returns the rules of the proxy's policy without the ones for
	// the Supervisor and for DNS, which are the first two.
	outside := func(values string) []any {
		objects, _ := chart(t, values)
		rules, _ := at(rendered.Find(objects, "NetworkPolicy", "aviary-egress-proxy"), "spec", "egress").([]any)
		if len(rules) < 3 || at(rules[0], "ports", 0, "port") != 9091 || at(rules[1], "ports", 0, "port") != 53 {
			t.Fatalf("%s: the policy of the proxy has the rules %v", values, rules)
		}
		return rules[2:]
	}

	anywhere := outside("default")
	inside := []any{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"}
	if len(anywhere) != 1 || at(anywhere[0], "ports") != nil ||
		at(anywhere[0], "to", 0, "ipBlock", "cidr") != "0.0.0.0/0" || !reflect.DeepEqual(at(anywhere[0], "to", 0, "ipBlock", "except"), inside) ||
		at(anywhere[0], "to", 1, "ipBlock", "cidr") != "::/0" || !reflect.DeepEqual(at(anywhere[0], "to", 1, "ipBlock", "except"), []any{"fc00::/7", "fe80::/10"}) {
		t.Errorf("the proxy may connect to %v", anywhere)
	}

	// What an installation names besides is added.
	if set := outside("set"); len(set) != 2 ||
		at(set[1], "to", 0, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name") != "llm-gateway" {
		t.Errorf("with a namespace named besides, the proxy may connect to %v", set)
	}
	// A model endpoint at an address inside is reached at that address.
	if closed := outside("closed"); len(closed) != 2 ||
		at(closed[1], "to", 0, "ipBlock", "cidr") != "10.1.2.3/32" || at(closed[1], "ports", 0, "port") != 8000 {
		t.Errorf("with a model endpoint at an address inside, the proxy may connect to %v", closed)
	}
}

// The Supervisor may do in the cluster what it takes to run turns, and no
// more.
func TestSupervisorPermissions(t *testing.T) {
	objects, _ := chart(t, "default")
	role := rendered.Find(objects, "Role", "aviary-supervisor")
	expected := []any{
		map[string]any{"apiGroups": []any{"batch"}, "resources": []any{"jobs"}, "verbs": []any{"create", "delete", "list", "watch"}},
		map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"list"}},
	}
	if at(role, "metadata", "namespace") != "aviary-runtime" || !reflect.DeepEqual(at(role, "rules"), expected) {
		t.Errorf("the Supervisor may %v in the namespace %v", at(role, "rules"), at(role, "metadata", "namespace"))
	}
	for _, object := range objects {
		if kind := object["kind"]; kind == "ClusterRole" || kind == "ClusterRoleBinding" {
			t.Errorf("the chart makes the %v %v", kind, at(object, "metadata", "name"))
		}
	}
	// The schema is brought up to date once, before the Supervisors of a
	// release start.
	migrate := rendered.Find(objects, "Job", "aviary-migrate")
	if at(migrate, "metadata", "annotations", "helm.sh/hook") != "pre-install,pre-upgrade" ||
		!reflect.DeepEqual(at(migrate, "spec", "template", "spec", "containers", 0, "args"), []any{"migrate"}) {
		t.Errorf("the migration is %v", migrate)
	}
}
