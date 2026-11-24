// Package chart tests the objects that the chart renders. `make test-chart`
// renders the chart with its default values and with each values file in
// this directory, and sets AVIARY_TEST_RENDERED to where the results are.
package chart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"aviary/test/internal/rendered"
)

// chart returns the objects rendered from the named values, and the Egress
// Proxy's Envoy configuration among them.
func chart(t *testing.T, values string) (objects []map[string]any, envoy map[string]any) {
	t.Helper()
	directory := os.Getenv("AVIARY_TEST_RENDERED")
	if directory == "" {
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

var at, named, find = rendered.At, rendered.Named, rendered.Find

// listener returns the HTTP connection manager settings of the named Envoy
// listener.
func listener(envoy map[string]any, name string) any {
	listener := named(at(envoy, "static_resources", "listeners"), name)
	return at(listener, "filter_chains", 0, "filters", 0, "typed_config")
}

// env returns the environment variables of a workload's first container.
func env(workload map[string]any) map[string]string {
	env := map[string]string{}
	variables, _ := at(workload, "spec", "template", "spec", "containers", 0, "env").([]any)
	for _, variable := range variables {
		name, _ := at(variable, "name").(string)
		env[name], _ = at(variable, "value").(string)
	}
	return env
}

// count returns the length of a list, or -1 if value is not a list.
func count(value any) int {
	list, ok := value.([]any)
	if !ok {
		return -1
	}
	return len(list)
}

func TestEgressProxy(t *testing.T) {
	for values, workers := range map[string][]any{"default": {"--concurrency", "2"}, "set": {"--concurrency", "4"}, "closed": nil} {
		objects, envoy := chart(t, values)
		// Otherwise Envoy starts a worker for every CPU of its node.
		args := at(find(objects, "Deployment", "aviary-egress-proxy"), "spec", "template", "spec", "containers", 0, "args")
		if want := append([]any{"--config-path", "/etc/envoy/envoy.yaml", "--disable-hot-restart"}, workers...); !reflect.DeepEqual(args, want) {
			t.Errorf("%s: Envoy args = %v, want %v", values, args, want)
		}
		for _, name := range []string{"llm", "forward"} {
			// Otherwise Envoy answers 403 when no Supervisor can be asked,
			// and a harness does not retry that.
			check := named(at(listener(envoy, name), "http_filters"), "envoy.filters.http.ext_authz")
			if code := at(check, "typed_config", "status_on_error", "code"); code != "ServiceUnavailable" {
				t.Errorf("%s: %s ext_authz status_on_error = %v, want ServiceUnavailable", values, name, code)
			}
		}
		// Envoy's admin interface can stop Envoy and dump its configuration.
		if socket := at(envoy, "admin", "address", "socket_address"); socket != nil {
			t.Errorf("%s: Envoy admin interface listens on %v, reachable through the forward proxy", values, socket)
		}
		routes := at(listener(envoy, "metrics"), "route_config", "virtual_hosts", 0, "routes")
		if list, _ := routes.([]any); len(list) != 1 || at(routes, 0, "match", "path") != "/metrics" {
			t.Errorf("%s: metrics listener routes = %v, want only /metrics", values, routes)
		}
	}
}

// Each access log line identifies the origin of its request on its own, and
// for a request of the model listener its provider or MCP server.
func TestEgressProxyRecordsWhereARequestCameFrom(t *testing.T) {
	_, envoy := chart(t, "default")
	told := func(field string) string { return "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:" + field + ")%" }
	for name, model := range map[string]bool{"llm": true, "forward": false} {
		record, _ := at(listener(envoy, name), "access_log", 0, "typed_config", "log_format", "json_format").(map[string]any)
		for _, field := range []string{"turn", "session", "agent", "project", "user", "subject"} {
			if record[field] != told(field) {
				t.Errorf("%s access log has %v for %s", name, record[field], field)
			}
		}
		for _, field := range []string{"provider", "mcp_server"} {
			if value, recorded := record[field]; recorded != model || (model && value != told(field)) {
				t.Errorf("%s access log has %v for %s", name, value, field)
			}
		}
	}
}

// A request may be silent for as long as a turn may run, for example while a
// model thinks or during a download.
func TestEgressProxyKeepsARequestForAsLongAsATurn(t *testing.T) {
	for values, longest := range map[string]string{"default": "1800s", "set": "7200s"} {
		objects, envoy := chart(t, values)
		for _, name := range []string{"llm", "forward"} {
			if idle := at(listener(envoy, name), "stream_idle_timeout"); idle != longest {
				t.Errorf("%s: %s stream_idle_timeout = %v, want %s", values, name, idle, longest)
			}
		}
		// The turn's deadline ends a model request, not Envoy.
		routes, _ := at(listener(envoy, "llm"), "route_config", "virtual_hosts", 0, "routes").([]any)
		passed := 0
		for _, route := range routes {
			if at(route, "route") == nil {
				continue
			}
			passed++
			if timeout := at(route, "route", "timeout"); timeout != "0s" {
				t.Errorf("%s: model route %v has timeout %v, want 0s", values, at(route, "match"), timeout)
			}
		}
		if passed != 2 {
			t.Errorf("%s: %d model routes forward requests, want 2 (with and without TLS)", values, passed)
		}
		// A terminating pod is not given extra time for the turns that use it.
		for _, name := range []string{"aviary-egress-proxy", "aviary-supervisor"} {
			pod := at(find(objects, "Deployment", name), "spec", "template", "spec")
			if grace := at(pod, "terminationGracePeriodSeconds"); grace != nil {
				t.Errorf("%s: %s terminationGracePeriodSeconds = %v, want unset", values, name, grace)
			}
		}
	}
}

// A model request goes to the endpoint, and with the key, that the Supervisor
// returns. The chart itself configures no model endpoint and holds no key.
func TestModelRequestsGoWhereTheSupervisorSays(t *testing.T) {
	for _, values := range []string{"default", "set", "closed"} {
		objects, envoy := chart(t, values)
		routes, _ := at(listener(envoy, "llm"), "route_config", "virtual_hosts", 0, "routes").([]any)
		if len(routes) != 3 {
			t.Fatalf("%s: model routes = %v, want 3", values, routes)
		}
		for i, cluster := range []string{"model-tls", "model"} {
			route := routes[i]
			if at(route, "route", "cluster") != cluster || at(route, "match", "headers", 0, "name") != "x-aviary-upstream-tls" ||
				at(route, "typed_per_filter_config", "envoy.filters.http.dynamic_forward_proxy", "host_rewrite_header") != "x-aviary-upstream" {
				t.Errorf("%s: unexpected model route %v", values, route)
			}
			if named(at(envoy, "static_resources", "clusters"), cluster) == nil {
				t.Errorf("%s: no Envoy cluster %s", values, cluster)
			}
		}
		// Without an endpoint from the Supervisor, a request is not forwarded.
		if last := routes[2]; at(last, "match", "headers") != nil || at(last, "direct_response", "status") != 503 {
			t.Errorf("%s: last model route = %v, want a direct 503 response for every request", values, last)
		}
		// The proxy has no key of its own to add, and mounts nothing to read
		// one from.
		whole, err := json.Marshal(envoy)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(whole), "credential_injector") {
			t.Errorf("%s: proxy configuration has a credential_injector", values)
		}
		proxy := at(find(objects, "Deployment", "aviary-egress-proxy"), "spec", "template", "spec")
		if volumes, _ := at(proxy, "volumes").([]any); len(volumes) != 1 || at(volumes[0], "configMap", "name") != "aviary-egress-proxy" {
			t.Errorf("%s: proxy volumes = %v, want only its ConfigMap", values, at(proxy, "volumes"))
		}
	}
}

func TestAllowedHosts(t *testing.T) {
	rules := func(envoy map[string]any, name string) any {
		return at(named(at(listener(envoy, name), "http_filters"), "envoy.filters.http.rbac"), "typed_config", "rules")
	}
	// The forward proxy checks the host that a request names, and the model
	// listener the host that the Supervisor named.
	for name, header := range map[string]string{"forward": ":authority", "llm": "x-aviary-upstream"} {
		// With every host allowed, which is the default, there are no rules.
		if _, open := chart(t, "default"); rules(open, name) != nil {
			t.Errorf("%s: with every host allowed, RBAC rules = %v, want none", name, rules(open, name))
		}

		_, set := chart(t, "set")
		var hosts []string
		permissions, _ := at(rules(set, name), "policies", "listed-hosts", "permissions").([]any)
		for _, permission := range permissions {
			if at(permission, "header") == nil {
				continue
			}
			host, _ := at(permission, "header", "string_match", "exact").(string)
			hosts = append(hosts, host)
			if at(permission, "header", "string_match", "ignore_case") != true || at(permission, "header", "name") != header {
				t.Errorf("%s: permission for host %s = %v, want a case-insensitive header match", name, host, permission)
			}
		}
		// A host matches on the default HTTPS and HTTP ports, and a host with
		// a port only on that port.
		expected := []string{
			"llm.example.com", "llm.example.com:443", "llm.example.com:80",
			"packages.example.com", "packages.example.com:443", "packages.example.com:80",
			"git.example.com:8443",
		}
		if !slices.Equal(hosts, expected) || at(rules(set, name), "action") != "ALLOW" {
			t.Errorf("%s: allowed hosts = %v, want %v", name, hosts, expected)
		}

		// An empty list allows nothing.
		_, closed := chart(t, "closed")
		permissions, _ = at(rules(closed, name), "policies", "listed-hosts", "permissions").([]any)
		for _, permission := range permissions {
			if at(permission, "header") != nil {
				t.Errorf("%s: with no host allowed, got host permission %v", name, permission)
			}
		}
		if at(rules(closed, name), "action") != "ALLOW" {
			t.Errorf("%s: with no host allowed, RBAC rules = %v, want action ALLOW", name, rules(closed, name))
		}
	}
}

func TestSupervisorSettings(t *testing.T) {
	objects, _ := chart(t, "default")
	settings := env(find(objects, "Deployment", "aviary-supervisor"))
	for name, value := range map[string]string{
		"AVIARY_ENCRYPTION_KEY":            "k1",
		"AVIARY_SECRET_STORE_HOSTS":        "[]",
		"AVIARY_SECRET_CACHE_SECONDS":      "60",
		"AVIARY_IDENTITY_PROVIDER":         `{"kind":"","settings":{}}`,
		"AVIARY_ADMIN_GROUPS":              "[]",
		"AVIARY_USER_GROUPS":               "[]",
		"AVIARY_ALLOWED_ORIGINS":           "[]",
		"AVIARY_SANDBOX_NAMESPACE":         "aviary-runtime",
		"AVIARY_SANDBOX_VOLUME_SIZE":       "10Gi",
		"AVIARY_SANDBOX_STORAGE_CLASS":     "",
		"AVIARY_SANDBOX_IDLE_SECONDS":      "300",
		"AVIARY_SANDBOX_ARCHIVE_SECONDS":   "604800",
		"AVIARY_SANDBOX_RETENTION_SECONDS": "0",
		"AVIARY_LLM_BASE_URL":              "http://aviary-egress-proxy.aviary-system:8080",
		"AVIARY_LLM_LOCALHOST":             "",
		"AVIARY_PROXY_URL":                 "http://aviary-egress-proxy.aviary-system:3128",
		"AVIARY_DEPOT_URL":                 "http://aviary-depot.aviary-system:8080",
		"AVIARY_MAX_CONCURRENT_TURNS":      "50",
		"AVIARY_MAX_QUEUED_TURNS":          "500",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS":    "120",
		"AVIARY_MAX_STARTUP_SECONDS":       "120",
		"AVIARY_MAX_RESTORE_SECONDS":       "600",
		"AVIARY_MAX_TURN_SECONDS":          "1800",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":     "65536",
	} {
		if settings[name] != value {
			t.Errorf("Supervisor env %s = %q, want %q", name, settings[name], value)
		}
	}

	// The model is set per agent, not per installation.
	if model, told := settings["AVIARY_LLM_MODEL"]; told {
		t.Errorf("Supervisor env AVIARY_LLM_MODEL = %q, want unset", model)
	}

	objects, _ = chart(t, "set")
	supervisor := find(objects, "Deployment", "aviary-supervisor")
	settings = env(supervisor)
	for name, value := range map[string]string{
		"AVIARY_ENCRYPTION_KEY":            "k2",
		"AVIARY_SECRET_STORE_HOSTS":        `["secrets.example.com:8200","store.example.com"]`,
		"AVIARY_SECRET_CACHE_SECONDS":      "5",
		"AVIARY_IDENTITY_PROVIDER":         `{"kind":"oidc","settings":{"audience":"aviary","claims":{"username":"email"},"issuer":"https://login.example.com/realms/main"}}`,
		"AVIARY_ADMIN_GROUPS":              `["platform"]`,
		"AVIARY_USER_GROUPS":               `["team-a","team-b"]`,
		"AVIARY_ALLOWED_ORIGINS":           `["https://aviary.example.com"]`,
		"AVIARY_SANDBOX_VOLUME_SIZE":       "50Gi",
		"AVIARY_SANDBOX_STORAGE_CLASS":     "fast",
		"AVIARY_SANDBOX_IDLE_SECONDS":      "0",
		"AVIARY_SANDBOX_ARCHIVE_SECONDS":   "2592000",
		"AVIARY_SANDBOX_RETENTION_SECONDS": "604800",
		"AVIARY_LLM_LOCALHOST":             "host.example.com",
		"AVIARY_MAX_CONCURRENT_TURNS":      "7",
		"AVIARY_MAX_QUEUED_TURNS":          "11",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS":    "13",
		"AVIARY_MAX_STARTUP_SECONDS":       "17",
		"AVIARY_MAX_RESTORE_SECONDS":       "1200",
		"AVIARY_MAX_TURN_SECONDS":          "7200",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":     "23",
	} {
		if settings[name] != value {
			t.Errorf("Supervisor env %s = %q, want %q", name, settings[name], value)
		}
	}
	if replicas := at(supervisor, "spec", "replicas"); replicas != 3 {
		t.Errorf("Supervisor replicas = %v, want 3", replicas)
	}

	// A value set for one harness leaves its other values and the other
	// harnesses as they are.
	type resources map[string]map[string]string
	var harnesses map[string]struct {
		Image     string
		Resources resources
	}
	if err := json.Unmarshal([]byte(settings["AVIARY_HARNESSES"]), &harnesses); err != nil {
		t.Fatalf("parse AVIARY_HARNESSES %s: %v", settings["AVIARY_HARNESSES"], err)
	}
	opencode, codex, claude := harnesses["opencode"], harnesses["codex"], harnesses["claude-code"]
	if len(harnesses) != 3 || opencode.Image != "aviary-harness-opencode:dev" || codex.Image != "aviary-harness-codex:dev" ||
		claude.Image != "aviary-harness-claude-code:dev" ||
		!reflect.DeepEqual(opencode.Resources, resources{"requests": {"cpu": "250m", "memory": "1Gi"}, "limits": {"memory": "8Gi"}}) ||
		!reflect.DeepEqual(codex.Resources, resources{"requests": {"cpu": "250m", "memory": "256Mi"}, "limits": {"memory": "4Gi"}}) ||
		!reflect.DeepEqual(claude.Resources, resources{"requests": {"cpu": "250m", "memory": "512Mi"}, "limits": {"memory": "4Gi"}}) {
		t.Errorf("AVIARY_HARNESSES = %s", settings["AVIARY_HARNESSES"])
	}
}

// A number of a million or more in a values file is rendered as it is set,
// not in the notation of a floating-point number, which neither the
// Supervisor nor Envoy reads.
func TestNumbersOfAMillionOrMore(t *testing.T) {
	objects, envoy := chart(t, "large")
	settings := env(find(objects, "Deployment", "aviary-supervisor"))
	for name, value := range map[string]string{
		"AVIARY_SECRET_CACHE_SECONDS":      "1000001",
		"AVIARY_SANDBOX_IDLE_SECONDS":      "1000002",
		"AVIARY_SANDBOX_ARCHIVE_SECONDS":   "2592000",
		"AVIARY_SANDBOX_RETENTION_SECONDS": "31536000",
		"AVIARY_MAX_CONCURRENT_TURNS":      "1000003",
		"AVIARY_MAX_QUEUED_TURNS":          "1000004",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS":    "1000005",
		"AVIARY_MAX_STARTUP_SECONDS":       "1000006",
		"AVIARY_MAX_RESTORE_SECONDS":       "1000007",
		"AVIARY_MAX_TURN_SECONDS":          "1000008",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":     "1048576",
	} {
		if settings[name] != value {
			t.Errorf("Supervisor env %s = %q, want %q", name, settings[name], value)
		}
	}
	for _, name := range []string{"llm", "forward"} {
		if idle := at(listener(envoy, name), "stream_idle_timeout"); idle != "1000008s" {
			t.Errorf("%s stream_idle_timeout = %v, want 1000008s", name, idle)
		}
	}
	depot := at(find(objects, "Deployment", "aviary-depot"), "spec", "template", "spec")
	if grace := at(depot, "terminationGracePeriodSeconds"); grace != 1000037 {
		t.Errorf("depot terminationGracePeriodSeconds = %v, want 1000037", grace)
	}
}

// One pod serves the state metrics. Of the Supervisor's settings it gets only
// the database and the harnesses, and it has no access to the cluster.
func TestStateMetrics(t *testing.T) {
	objects, _ := chart(t, "default")
	served := find(objects, "Deployment", "aviary-state-metrics")
	// Two pods would report every count twice, also while one replaces the
	// other.
	if at(served, "spec", "replicas") != 1 || at(served, "spec", "strategy", "type") != "Recreate" {
		t.Errorf("state metrics: replicas = %v, strategy = %v; want 1 and Recreate", at(served, "spec", "replicas"), at(served, "spec", "strategy"))
	}
	pod := at(served, "spec", "template", "spec")
	container := at(pod, "containers", 0)
	supervisor := at(find(objects, "Deployment", "aviary-supervisor"), "spec", "template", "spec", "containers", 0)
	if at(container, "image") != at(supervisor, "image") || !reflect.DeepEqual(at(container, "args"), []any{"state-metrics"}) ||
		at(named(at(container, "ports"), "metrics"), "containerPort") != 9092 {
		t.Errorf("state metrics container = %v, want the image of the Supervisor with the command state-metrics and the port 9092", container)
	}
	for _, name := range []string{"AVIARY_DATABASE_URL", "AVIARY_HARNESSES"} {
		if set := named(at(container, "env"), name); set == nil || !reflect.DeepEqual(set, named(at(supervisor, "env"), name)) {
			t.Errorf("state metrics env %s = %v, want that of the Supervisor", name, set)
		}
	}
	if count(at(container, "env")) != 2 {
		t.Errorf("state metrics env = %v, want only the database URL and the harnesses", at(container, "env"))
	}
	if at(pod, "automountServiceAccountToken") != false || at(pod, "serviceAccountName") != nil {
		t.Errorf("state metrics pod has the service account %v, token mounted %v; want neither",
			at(pod, "serviceAccountName"), at(pod, "automountServiceAccountToken"))
	}

	objects, _ = chart(t, "set")
	if served := find(objects, "Deployment", "aviary-state-metrics"); served != nil {
		t.Errorf("chart renders the state metrics although they are disabled: %v", served)
	}
}

// The chart installs agent-sandbox in the version that the Supervisor is
// written for, unless the cluster has it already.
func TestAgentSandbox(t *testing.T) {
	objects, _ := chart(t, "default")
	controller := find(objects, "Deployment", "agent-sandbox-controller")
	image := at(controller, "spec", "template", "spec", "containers", 0, "image")
	if image != "registry.k8s.io/agent-sandbox/agent-sandbox-controller:v1.0.11" || at(controller, "metadata", "namespace") != "agent-sandbox-system" {
		t.Errorf("agent-sandbox controller: image = %v, namespace = %v; want v1.0.11 in agent-sandbox-system", image, at(controller, "metadata", "namespace"))
	}

	objects, _ = chart(t, "set")
	for _, object := range objects {
		if name, _ := at(object, "metadata", "name").(string); strings.HasPrefix(name, "agent-sandbox") {
			t.Errorf("chart renders the %v %s although agent-sandbox is disabled", object["kind"], name)
		}
	}
}

// The Egress Proxy asks the Supervisors on a dedicated port, which the
// sandboxes cannot reach.
func TestSupervisorAnswersTheEgressProxy(t *testing.T) {
	objects, envoy := chart(t, "default")
	port := named(at(find(objects, "Service", "aviary-supervisor"), "spec", "ports"), "authz")
	container := named(at(find(objects, "Deployment", "aviary-supervisor"), "spec", "template", "spec", "containers", 0, "ports"), "authz")
	if at(port, "port") != 9091 || at(port, "targetPort") != "authz" || at(container, "containerPort") != 9091 {
		t.Errorf("Supervisor authz Service port = %v, container port = %v; want 9091", port, container)
	}
	asked := at(named(at(envoy, "static_resources", "clusters"), "supervisor"), "load_assignment", "endpoints", 0, "lb_endpoints", 0,
		"endpoint", "address", "socket_address")
	if at(asked, "address") != "aviary-supervisor.aviary-system" || at(asked, "port_value") != 9091 {
		t.Errorf("proxy's Supervisor address = %v, want aviary-supervisor.aviary-system:9091", asked)
	}
}

// The encryption keys are mounted from the secret that the values name, at
// the path in AVIARY_ENCRYPTION_KEYS.
func TestSupervisorEncryptionKeys(t *testing.T) {
	for values, secret := range map[string]string{"default": "aviary-encryption", "set": "other-keys"} {
		objects, _ := chart(t, values)
		supervisor := find(objects, "Deployment", "aviary-supervisor")
		pod := at(supervisor, "spec", "template", "spec")
		mount := named(at(pod, "containers", 0, "volumeMounts"), "encryption")
		told := env(supervisor)["AVIARY_ENCRYPTION_KEYS"]
		if told == "" || at(mount, "mountPath") != told || at(mount, "readOnly") != true {
			t.Errorf("%s: AVIARY_ENCRYPTION_KEYS = %q, mount = %v; want a read-only mount at that path", values, told, mount)
		}
		if mounted := at(named(at(pod, "volumes"), "encryption"), "secret", "secretName"); mounted != secret {
			t.Errorf("%s: encryption keys Secret = %v, want %s", values, mounted, secret)
		}
	}
}

// A sandbox can connect only to the Egress Proxy, not even to the cluster
// DNS, since lookups can carry data out. Only the Supervisors can connect to
// it, on the runner port. Its namespace enforces the restricted Pod Security
// Standard, and is kept with the sessions in it when the chart is
// uninstalled.
func TestSandboxesAreCutOff(t *testing.T) {
	objects, _ := chart(t, "default")
	policy := at(find(objects, "NetworkPolicy", "aviary-sandboxes"), "spec")
	if at(find(objects, "NetworkPolicy", "aviary-sandboxes"), "metadata", "namespace") != "aviary-runtime" ||
		!reflect.DeepEqual(at(policy, "podSelector"), map[string]any{}) || !reflect.DeepEqual(at(policy, "policyTypes"), []any{"Ingress", "Egress"}) {
		t.Errorf("sandbox NetworkPolicy spec = %v", policy)
	}
	rules, _ := at(policy, "ingress").([]any)
	if len(rules) != 1 || count(at(rules[0], "from")) != 1 ||
		at(rules[0], "from", 0, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name") != "aviary-system" ||
		at(rules[0], "from", 0, "podSelector", "matchLabels", "app.kubernetes.io/component") != "supervisor" ||
		!reflect.DeepEqual(at(rules[0], "ports"), []any{map[string]any{"protocol": "TCP", "port": 8700}}) {
		t.Errorf("sandbox ingress rules = %v, want only the Supervisors on port 8700", rules)
	}
	out, _ := at(policy, "egress").([]any)
	if len(out) != 1 || count(at(out[0], "to")) != 1 ||
		at(out[0], "to", 0, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name") != "aviary-system" ||
		at(out[0], "to", 0, "podSelector", "matchLabels", "app.kubernetes.io/component") != "egress-proxy" ||
		!reflect.DeepEqual(at(out[0], "ports"), []any{map[string]any{"protocol": "TCP", "port": 8080}, map[string]any{"protocol": "TCP", "port": 3128}}) {
		t.Errorf("sandbox egress rules = %v, want only the Egress Proxy on ports 8080 and 3128", out)
	}
	namespace := find(objects, "Namespace", "aviary-runtime")
	if at(namespace, "metadata", "annotations", "helm.sh/resource-policy") != "keep" ||
		at(namespace, "metadata", "labels", "pod-security.kubernetes.io/enforce") != "restricted" {
		t.Errorf("sandbox namespace = %v, want it kept on uninstall and restricted", namespace)
	}
}

// The proxy must not give the sandboxes a way into the cluster.
func TestEgressProxyIsKeptFromTheInside(t *testing.T) {
	// outside returns the egress rules of the proxy's policy after the first
	// two, which allow the Supervisor and DNS.
	outside := func(values string) []any {
		objects, _ := chart(t, values)
		rules, _ := at(find(objects, "NetworkPolicy", "aviary-egress-proxy"), "spec", "egress").([]any)
		if len(rules) < 3 || at(rules[0], "ports", 0, "port") != 9091 || at(rules[1], "ports", 0, "port") != 53 {
			t.Fatalf("%s: proxy egress rules = %v, want the Supervisor and DNS first", values, rules)
		}
		return rules[2:]
	}

	anywhere := outside("default")
	inside := []any{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"}
	if len(anywhere) != 1 || at(anywhere[0], "ports") != nil ||
		at(anywhere[0], "to", 0, "ipBlock", "cidr") != "0.0.0.0/0" || !reflect.DeepEqual(at(anywhere[0], "to", 0, "ipBlock", "except"), inside) ||
		at(anywhere[0], "to", 1, "ipBlock", "cidr") != "::/0" || !reflect.DeepEqual(at(anywhere[0], "to", 1, "ipBlock", "except"), []any{"fc00::/7", "fe80::/10"}) {
		t.Errorf("proxy egress rules = %v, want every address outside the private ranges", anywhere)
	}

	// The rules of extraEgress are added.
	if set := outside("set"); len(set) != 2 ||
		at(set[1], "to", 0, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name") != "llm-gateway" {
		t.Errorf("proxy egress rules with extraEgress = %v", set)
	}
}

// The Supervisor may manage the Sandboxes of the sessions, and nothing else
// in the cluster.
func TestSupervisorPermissions(t *testing.T) {
	objects, _ := chart(t, "default")
	role := find(objects, "Role", "aviary-supervisor")
	expected := []any{map[string]any{
		"apiGroups": []any{"agents.x-k8s.io"}, "resources": []any{"sandboxes"}, "verbs": []any{"create", "get", "list", "patch", "delete"},
	}}
	if at(role, "metadata", "namespace") != "aviary-runtime" || !reflect.DeepEqual(at(role, "rules"), expected) {
		t.Errorf("Supervisor Role rules = %v in namespace %v", at(role, "rules"), at(role, "metadata", "namespace"))
	}
	binding := find(objects, "RoleBinding", "aviary-supervisor")
	if at(binding, "metadata", "namespace") != "aviary-runtime" || at(binding, "roleRef", "name") != "aviary-supervisor" ||
		at(binding, "subjects", 0, "name") != "aviary-supervisor" || at(binding, "subjects", 0, "namespace") != "aviary-system" {
		t.Errorf("Supervisor RoleBinding = %v", binding)
	}
	// Only the controller of agent-sandbox has permissions in the whole
	// cluster.
	for _, object := range objects {
		if object["kind"] != "ClusterRoleBinding" {
			continue
		}
		subjects := at(object, "subjects")
		if count(subjects) != 1 || at(subjects, 0, "name") != "agent-sandbox-controller" || at(subjects, 0, "namespace") != "agent-sandbox-system" {
			t.Errorf("chart binds the ClusterRole %v to %v", at(object, "roleRef", "name"), subjects)
		}
	}
	// The schema is migrated once per release, before the Supervisors start.
	migrate := find(objects, "Job", "aviary-migrate")
	if at(migrate, "metadata", "annotations", "helm.sh/hook") != "pre-install,pre-upgrade" ||
		!reflect.DeepEqual(at(migrate, "spec", "template", "spec", "containers", 0, "args"), []any{"migrate"}) {
		t.Errorf("migration Job = %v", migrate)
	}
}

// The depots run the image of the Supervisor with the command depot. Of the
// Supervisor's settings they get none, and they have no access to the
// cluster. A depot that stops is given time to finish its transfers: as long
// as a turn is given to restore a home. The Supervisors are told where the
// depots are.
func TestDepot(t *testing.T) {
	for values, replicas := range map[string]int{"default": 1, "set": 3} {
		objects, _ := chart(t, values)
		depot := find(objects, "Deployment", "aviary-depot")
		// A new depot starts before the one that it replaces stops, also if
		// there is only one.
		if at(depot, "spec", "replicas") != replicas || at(depot, "spec", "strategy") != nil {
			t.Errorf("%s: depot replicas = %v, strategy = %v; want %d and the rolling update that is the default",
				values, at(depot, "spec", "replicas"), at(depot, "spec", "strategy"), replicas)
		}
		pod := at(depot, "spec", "template", "spec")
		container := at(pod, "containers", 0)
		supervisor := at(find(objects, "Deployment", "aviary-supervisor"), "spec", "template", "spec", "containers", 0)
		if at(container, "image") != at(supervisor, "image") || !reflect.DeepEqual(at(container, "args"), []any{"depot"}) ||
			at(named(at(container, "ports"), "http"), "containerPort") != 8080 ||
			at(named(at(container, "ports"), "metrics"), "containerPort") != 9092 {
			t.Errorf("%s: depot container = %v, want the image of the Supervisor with the command depot and the ports 8080 and 9092", values, container)
		}
		directory := env(depot)["AVIARY_DEPOT_DIRECTORY"]
		if count(at(container, "env")) != 1 || directory == "" || at(container, "volumeMounts", 0, "mountPath") != directory ||
			at(container, "volumeMounts", 0, "name") != at(pod, "volumes", 0, "name") {
			t.Errorf("%s: depot env = %v, mounts = %v; want only the directory of the objects, where the volume is mounted",
				values, at(container, "env"), at(container, "volumeMounts"))
		}
		if at(pod, "automountServiceAccountToken") != false || at(pod, "serviceAccountName") != nil {
			t.Errorf("%s: depot pod has the service account %v, token mounted %v; want neither",
				values, at(pod, "serviceAccountName"), at(pod, "automountServiceAccountToken"))
		}
		settings := env(find(objects, "Deployment", "aviary-supervisor"))
		restore, err := strconv.Atoi(settings["AVIARY_MAX_RESTORE_SECONDS"])
		if err != nil {
			t.Fatalf("%s: Supervisor env AVIARY_MAX_RESTORE_SECONDS = %q, want a number", values, settings["AVIARY_MAX_RESTORE_SECONDS"])
		}
		if grace, _ := at(pod, "terminationGracePeriodSeconds").(int); grace <= restore {
			t.Errorf("%s: a stopping depot is given %v seconds for its transfers, want more than the %d of a restore",
				values, at(pod, "terminationGracePeriodSeconds"), restore)
		}
		service := at(find(objects, "Service", "aviary-depot"), "spec")
		if !reflect.DeepEqual(at(service, "selector"), at(depot, "spec", "selector", "matchLabels")) ||
			!reflect.DeepEqual(at(service, "ports"), []any{map[string]any{"name": "http", "port": 8080, "targetPort": "http"}}) {
			t.Errorf("%s: depot Service = %v, want the depots at port 8080", values, service)
		}
		if settings["AVIARY_DEPOT_URL"] != "http://aviary-depot.aviary-system:8080" {
			t.Errorf("%s: Supervisor env AVIARY_DEPOT_URL = %q, want the Service of the depots", values, settings["AVIARY_DEPOT_URL"])
		}
	}
}

// The objects are in one volume that every depot mounts. A volume that one
// node mounts, as the default is, keeps the depots on that node together. A
// volume that many nodes mount lets them run on any node. The volume is kept
// when the chart is uninstalled, as the sessions are.
func TestDepotVolume(t *testing.T) {
	together := []any{map[string]any{
		"topologyKey": "kubernetes.io/hostname",
		"labelSelector": map[string]any{"matchLabels": map[string]any{
			"app.kubernetes.io/name": "aviary", "app.kubernetes.io/component": "depot",
		}},
	}}
	for _, test := range []struct {
		values, claim string
		// spec is the claim that the chart makes, or nil if it makes none.
		spec     map[string]any
		together bool
	}{
		{"default", "aviary-depot", map[string]any{
			"accessModes": []any{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]any{"storage": "30Gi"}},
		}, true},
		{"set", "aviary-depot", map[string]any{
			"accessModes": []any{"ReadWriteMany"}, "storageClassName": "shared",
			"resources": map[string]any{"requests": map[string]any{"storage": "100Gi"}},
		}, false},
		{"claim", "objects", nil, true},
	} {
		objects, _ := chart(t, test.values)
		pod := at(find(objects, "Deployment", "aviary-depot"), "spec", "template", "spec")
		if count(at(pod, "volumes")) != 1 || at(pod, "volumes", 0, "persistentVolumeClaim", "claimName") != test.claim {
			t.Errorf("%s: depot volumes = %v, want only the claim %s", test.values, at(pod, "volumes"), test.claim)
		}
		affinity := at(pod, "affinity", "podAffinity", "requiredDuringSchedulingIgnoredDuringExecution")
		if test.together && !reflect.DeepEqual(affinity, together) || !test.together && at(pod, "affinity") != nil {
			t.Errorf("%s: depot affinity = %v, want the depots on one node: %v", test.values, at(pod, "affinity"), test.together)
		}
		made := find(objects, "PersistentVolumeClaim", "aviary-depot")
		if test.spec == nil {
			if made != nil {
				t.Errorf("%s: chart makes the claim %v although the installation has its own", test.values, made)
			}
			continue
		}
		if !reflect.DeepEqual(at(made, "spec"), test.spec) || at(made, "metadata", "annotations", "helm.sh/resource-policy") != "keep" {
			t.Errorf("%s: depot claim = %v, want the spec %v, kept on uninstall", test.values, made, test.spec)
		}
	}
}

// A depot does not ask who reads an object, so only the Supervisors can
// connect to it. It connects to nothing itself. Its metrics can be scraped.
func TestOnlySupervisorsReachTheDepot(t *testing.T) {
	objects, _ := chart(t, "default")
	policy := at(find(objects, "NetworkPolicy", "aviary-depot"), "spec")
	depot := at(find(objects, "Deployment", "aviary-depot"), "spec", "selector", "matchLabels")
	if !reflect.DeepEqual(at(policy, "podSelector", "matchLabels"), depot) ||
		!reflect.DeepEqual(at(policy, "policyTypes"), []any{"Ingress", "Egress"}) || at(policy, "egress") != nil {
		t.Errorf("depot NetworkPolicy = %v, want it to select the depots, with rules for both directions and none that lets a depot connect", policy)
	}
	supervisors := at(find(objects, "Deployment", "aviary-supervisor"), "spec", "selector", "matchLabels")
	expected := []any{
		map[string]any{
			"from":  []any{map[string]any{"podSelector": map[string]any{"matchLabels": supervisors}}},
			"ports": []any{map[string]any{"protocol": "TCP", "port": 8080}},
		},
		map[string]any{"ports": []any{map[string]any{"protocol": "TCP", "port": 9092}}},
	}
	if !reflect.DeepEqual(at(policy, "ingress"), expected) {
		t.Errorf("depot ingress rules = %v, want only the Supervisors on port 8080 and anyone on the metrics port", at(policy, "ingress"))
	}
}
