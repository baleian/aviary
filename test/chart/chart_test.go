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
	"strings"
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

var at, named, find = rendered.At, rendered.Named, rendered.Find

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

// A line of the proxy's record tells by itself where a request came from,
// and of a model request which provider it went to.
func TestEgressProxyRecordsWhereARequestCameFrom(t *testing.T) {
	_, envoy := chart(t, "default")
	told := func(field string) string { return "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:" + field + ")%" }
	for name, model := range map[string]bool{"llm": true, "forward": false} {
		record, _ := at(listener(envoy, name), "access_log", 0, "typed_config", "log_format", "json_format").(map[string]any)
		for _, field := range []string{"turn", "session", "agent", "project", "user", "subject"} {
			if record[field] != told(field) {
				t.Errorf("a line of the record of %s has %v as its %s", name, record[field], field)
			}
		}
		if provider, recorded := record["provider"]; recorded != model || (model && provider != told("provider")) {
			t.Errorf("a line of the record of %s has %v as its provider", name, provider)
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
		// The deadline of the turn ends a model request, not Envoy.
		routes, _ := at(listener(envoy, "llm"), "route_config", "virtual_hosts", 0, "routes").([]any)
		passed := 0
		for _, route := range routes {
			if at(route, "route") == nil {
				continue
			}
			passed++
			if timeout := at(route, "route", "timeout"); timeout != "0s" {
				t.Errorf("%s: a request to the model on the route %v is ended after %v", values, at(route, "match"), timeout)
			}
		}
		if passed != 2 {
			t.Errorf("%s: %d routes pass model requests on, not the two for endpoints with TLS and without", values, passed)
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

// A model request goes where the Supervisor says, with the key that the
// Supervisor gives. The chart itself knows no model endpoint and holds no
// key.
func TestModelRequestsGoWhereTheSupervisorSays(t *testing.T) {
	for _, values := range []string{"default", "set", "closed"} {
		objects, envoy := chart(t, values)
		routes, _ := at(listener(envoy, "llm"), "route_config", "virtual_hosts", 0, "routes").([]any)
		if len(routes) != 3 {
			t.Fatalf("%s: the model requests have the routes %v", values, routes)
		}
		for i, cluster := range []string{"model-tls", "model"} {
			route := routes[i]
			if at(route, "route", "cluster") != cluster || at(route, "match", "headers", 0, "name") != "x-aviary-upstream-tls" ||
				at(route, "typed_per_filter_config", "envoy.filters.http.dynamic_forward_proxy", "host_rewrite_header") != "x-aviary-upstream" {
				t.Errorf("%s: a model request is passed on by the route %v", values, route)
			}
			if named(at(envoy, "static_resources", "clusters"), cluster) == nil {
				t.Errorf("%s: the proxy has no cluster %s", values, cluster)
			}
		}
		// Without an endpoint from the Supervisor a request goes nowhere.
		if last := routes[2]; at(last, "match", "headers") != nil || at(last, "direct_response", "status") != 503 {
			t.Errorf("%s: a model request without an endpoint is answered by the route %v", values, last)
		}
		// The proxy has no key to put into a request, and nothing to read
		// one from.
		whole, err := json.Marshal(envoy)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(whole), "credential_injector") {
			t.Errorf("%s: the proxy puts a key of its own into requests", values)
		}
		proxy := at(find(objects, "Deployment", "aviary-egress-proxy"), "spec", "template", "spec")
		if volumes, _ := at(proxy, "volumes").([]any); len(volumes) != 1 || at(volumes[0], "configMap", "name") != "aviary-egress-proxy" {
			t.Errorf("%s: the pod of the proxy mounts %v", values, at(proxy, "volumes"))
		}
	}
}

func TestAllowedHosts(t *testing.T) {
	rules := func(envoy map[string]any, name string) any {
		return at(named(at(listener(envoy, name), "http_filters"), "envoy.filters.http.rbac"), "typed_config", "rules")
	}
	// A request through the forward proxy names its host itself. The host
	// of a model request is the one that the Supervisor named.
	for name, header := range map[string]string{"forward": ":authority", "llm": "x-aviary-upstream"} {
		// The default lets every host through: there is no list to hold a
		// request against.
		if _, open := chart(t, "default"); rules(open, name) != nil {
			t.Errorf("with every host allowed, the proxy holds requests on %s against %v", name, rules(open, name))
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
				t.Errorf("on %s, the host %s is allowed by %v", name, host, permission)
			}
		}
		// A host stands for itself on the ports of HTTPS and HTTP, and a
		// host with a port for that port alone.
		expected := []string{
			"llm.example.com", "llm.example.com:443", "llm.example.com:80",
			"packages.example.com", "packages.example.com:443", "packages.example.com:80",
			"git.example.com:8443",
		}
		if !slices.Equal(hosts, expected) || at(rules(set, name), "action") != "ALLOW" {
			t.Errorf("on %s, the proxy lets requests through to %v, not to %v", name, hosts, expected)
		}

		// An empty list lets nothing through.
		_, closed := chart(t, "closed")
		permissions, _ = at(rules(closed, name), "policies", "listed-hosts", "permissions").([]any)
		for _, permission := range permissions {
			if at(permission, "header") != nil {
				t.Errorf("on %s, with no host allowed, the proxy lets requests through by %v", name, permission)
			}
		}
		if at(rules(closed, name), "action") != "ALLOW" {
			t.Errorf("on %s, with no host allowed, the proxy holds requests against %v", name, rules(closed, name))
		}
	}
}

// What the Supervisor is told of how to run turns.
func TestSupervisorSettings(t *testing.T) {
	objects, _ := chart(t, "default")
	settings := env(rendered.Find(objects, "Deployment", "aviary-supervisor"))
	for name, value := range map[string]string{
		"AVIARY_GATEWAY_URL":            "http://aviary-supervisor.aviary-system:9090",
		"AVIARY_ENCRYPTION_KEY":         "k1",
		"AVIARY_SECRET_STORE_HOSTS":     "[]",
		"AVIARY_SECRET_CACHE_SECONDS":   "60",
		"AVIARY_IDENTITY_PROVIDER":      `{"kind":"","settings":{}}`,
		"AVIARY_ADMIN_GROUPS":           "[]",
		"AVIARY_USER_GROUPS":            "[]",
		"AVIARY_LLM_BASE_URL":           "http://aviary-egress-proxy.aviary-system:8080",
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

	// The model of a turn is that of its agent, and no setting.
	if model, told := settings["AVIARY_LLM_MODEL"]; told {
		t.Errorf("the Supervisor is told the model %q", model)
	}

	objects, _ = chart(t, "set")
	supervisor := rendered.Find(objects, "Deployment", "aviary-supervisor")
	settings = env(supervisor)
	for name, value := range map[string]string{
		"AVIARY_ENCRYPTION_KEY":         "k2",
		"AVIARY_SECRET_STORE_HOSTS":     `["secrets.example.com:8200","store.example.com"]`,
		"AVIARY_SECRET_CACHE_SECONDS":   "5",
		"AVIARY_IDENTITY_PROVIDER":      `{"kind":"oidc","settings":{"audience":"aviary","claims":{"username":"email"},"issuer":"https://login.example.com/realms/main"}}`,
		"AVIARY_ADMIN_GROUPS":           `["platform"]`,
		"AVIARY_USER_GROUPS":            `["team-a","team-b"]`,
		"AVIARY_LLM_BASE_URL":           "http://aviary-egress-proxy.aviary-system:8080",
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

// The templates of the harnesses are built once too, before the Supervisors
// of a release start, from what the release says of the harnesses.
func TestTemplatesAreBuiltWithTheRelease(t *testing.T) {
	objects, _ := chart(t, "default")
	templates := rendered.Find(objects, "Job", "aviary-templates")
	if at(templates, "metadata", "annotations", "helm.sh/hook") != "pre-install,pre-upgrade" ||
		!reflect.DeepEqual(at(templates, "spec", "template", "spec", "containers", 0, "args"), []any{"templates"}) {
		t.Errorf("the templates are built by %v", templates)
	}
	settings := env(templates)
	for name, value := range map[string]string{
		"AVIARY_SANDBOXES_URL":         "http://opensandbox-server.opensandbox-system",
		"AVIARY_TEMPLATE_STORE":        "s3://sandbox-images/publish",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES": "65536",
	} {
		if settings[name] != value {
			t.Errorf("the Job is told %s=%q, not %q", name, settings[name], value)
		}
	}
	var harnesses map[string]struct {
		Image   string            `json:"image"`
		Sandbox map[string]string `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(settings["AVIARY_HARNESSES"]), &harnesses); err != nil {
		t.Fatal(err)
	}
	size := map[string]string{"cpu": "1", "memory": "2Gi", "disk": "4Gi"}
	if opencode := harnesses["opencode"]; opencode.Image != "aviary-harness-opencode:dev" || !reflect.DeepEqual(opencode.Sandbox, size) {
		t.Errorf("the Job is told of the harness opencode: %v", opencode)
	}
	// The key of the sandbox service is in a secret, and not in the Job.
	key := named(at(templates, "spec", "template", "spec", "containers", 0, "env"), "AVIARY_SANDBOXES_API_KEY")
	if !reflect.DeepEqual(at(key, "valueFrom", "secretKeyRef"), map[string]any{"name": "aviary-sandboxes", "key": "api-key"}) {
		t.Errorf("the key of the sandbox service is %v", key)
	}

	// An installation with its own.
	objects, _ = chart(t, "set")
	settings = env(rendered.Find(objects, "Job", "aviary-templates"))
	if settings["AVIARY_SANDBOXES_URL"] != "https://sandboxes.example.com" || settings["AVIARY_TEMPLATE_STORE"] != "s3://example/templates" ||
		!strings.Contains(settings["AVIARY_HARNESSES"], `"disk":"16Gi"`) {
		t.Errorf("the Job of an installation with its own sandbox service is told %v", settings)
	}
}
