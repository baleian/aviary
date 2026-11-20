// Package chart checks what the chart makes of its values. `make test-chart`
// renders the chart with its own values and with those of the files in this
// directory, and tells the tests where the results are.
package chart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"aviary/test/internal/rendered"
)

// chart returns the objects that the chart makes of the values with the
// name.
func chart(t *testing.T, values string) []map[string]any {
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
	return objects
}

var at, named = rendered.At, rendered.Named

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

// A Supervisor that is to stop does not wait for the turns that use it.
func TestSupervisorStopsWithoutWaitingForTheTurns(t *testing.T) {
	for _, values := range []string{"default", "set"} {
		pod := at(rendered.Find(chart(t, values), "Deployment", "aviary-supervisor"), "spec", "template", "spec")
		if grace := at(pod, "terminationGracePeriodSeconds"); grace != nil {
			t.Errorf("%s: a pod of the Supervisor is given %v seconds to stop", values, grace)
		}
	}
}

// What the Supervisor is told of how to run turns.
func TestSupervisorSettings(t *testing.T) {
	objects := chart(t, "default")
	settings := env(rendered.Find(objects, "Deployment", "aviary-supervisor"))
	for name, value := range map[string]string{
		"AVIARY_ENCRYPTION_KEY":            "k1",
		"AVIARY_SECRET_STORE_HOSTS":        "[]",
		"AVIARY_SECRET_CACHE_SECONDS":      "60",
		"AVIARY_IDENTITY_PROVIDER":         `{"kind":"","settings":{}}`,
		"AVIARY_ADMIN_GROUPS":              "[]",
		"AVIARY_USER_GROUPS":               "[]",
		"AVIARY_SANDBOX_NAMESPACE":         "aviary-runtime",
		"AVIARY_SANDBOX_VOLUME_SIZE":       "10Gi",
		"AVIARY_SANDBOX_STORAGE_CLASS":     "",
		"AVIARY_SANDBOX_IDLE_SECONDS":      "300",
		"AVIARY_RESOLVER_TLS":              "/etc/aviary/resolver",
		"AVIARY_SANDBOX_RETENTION_SECONDS": "0",
		"AVIARY_MAX_CONCURRENT_TURNS":      "50",
		"AVIARY_MAX_QUEUED_TURNS":          "500",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS":    "120",
		"AVIARY_MAX_STARTUP_SECONDS":       "120",
		"AVIARY_MAX_TURN_SECONDS":          "1800",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":     "65536",
	} {
		if settings[name] != value {
			t.Errorf("the Supervisor is told %s=%q, not %q", name, settings[name], value)
		}
	}

	// The model of a turn is that of its agent, and no setting.
	if model, told := settings["AVIARY_LLM_MODEL"]; told {
		t.Errorf("the Supervisor is told the model %q", model)
	}

	objects = chart(t, "set")
	supervisor := rendered.Find(objects, "Deployment", "aviary-supervisor")
	settings = env(supervisor)
	for name, value := range map[string]string{
		"AVIARY_ENCRYPTION_KEY":            "k2",
		"AVIARY_SECRET_STORE_HOSTS":        `["secrets.example.com:8200","store.example.com"]`,
		"AVIARY_SECRET_CACHE_SECONDS":      "5",
		"AVIARY_IDENTITY_PROVIDER":         `{"kind":"oidc","settings":{"audience":"aviary","claims":{"username":"email"},"issuer":"https://login.example.com/realms/main"}}`,
		"AVIARY_ADMIN_GROUPS":              `["platform"]`,
		"AVIARY_USER_GROUPS":               `["team-a","team-b"]`,
		"AVIARY_SANDBOX_VOLUME_SIZE":       "50Gi",
		"AVIARY_SANDBOX_STORAGE_CLASS":     "fast",
		"AVIARY_SANDBOX_IDLE_SECONDS":      "0",
		"AVIARY_SANDBOX_RETENTION_SECONDS": "604800",
		"AVIARY_MAX_CONCURRENT_TURNS":      "7",
		"AVIARY_MAX_QUEUED_TURNS":          "11",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS":    "13",
		"AVIARY_MAX_STARTUP_SECONDS":       "17",
		"AVIARY_MAX_TURN_SECONDS":          "7200",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":     "23",
	} {
		if settings[name] != value {
			t.Errorf("the Supervisor is told %s=%q, not %q", name, settings[name], value)
		}
	}
	if replicas := at(supervisor, "spec", "replicas"); replicas != 3 {
		t.Errorf("the chart runs %v Supervisors, not 3", replicas)
	}

	// A value that is set for one harness leaves the rest of the harnesses
	// as they are.
	type resources map[string]map[string]string
	var harnesses map[string]struct {
		Image     string
		Resources resources
	}
	if err := json.Unmarshal([]byte(settings["AVIARY_HARNESSES"]), &harnesses); err != nil {
		t.Fatalf("the Supervisor is told the harnesses %s: %v", settings["AVIARY_HARNESSES"], err)
	}
	opencode, codex := harnesses["opencode"], harnesses["codex"]
	if len(harnesses) != 2 || opencode.Image != "aviary-harness-opencode:dev" || codex.Image != "aviary-harness-codex:dev" ||
		!reflect.DeepEqual(opencode.Resources, resources{"requests": {"cpu": "250m", "memory": "1Gi"}, "limits": {"memory": "8Gi"}}) ||
		!reflect.DeepEqual(codex.Resources, resources{"requests": {"cpu": "250m", "memory": "256Mi"}, "limits": {"memory": "4Gi"}}) {
		t.Errorf("the Supervisor is told the harnesses %s", settings["AVIARY_HARNESSES"])
	}
}

// The Supervisor answers the egress of the sandbox service with the
// certificate of the secret that the values name, where it is told that the
// certificate is, at the port that it is reached at.
func TestSupervisorAnswersTheEgress(t *testing.T) {
	for values, secret := range map[string]string{"default": "aviary-resolver-tls", "set": "other-resolver-tls"} {
		objects := chart(t, values)
		supervisor := rendered.Find(objects, "Deployment", "aviary-supervisor")
		pod := at(supervisor, "spec", "template", "spec")
		mount := named(at(pod, "containers", 0, "volumeMounts"), "resolver")
		if told := env(supervisor)["AVIARY_RESOLVER_TLS"]; told == "" || at(mount, "mountPath") != told || at(mount, "readOnly") != true {
			t.Errorf("%s: the Supervisor is told that its certificate is in %q, and it is mounted as %v", values, told, mount)
		}
		if mounted := at(named(at(pod, "volumes"), "resolver"), "secret", "secretName"); mounted != secret {
			t.Errorf("%s: the certificate is that of the secret %v, not of %s", values, mounted, secret)
		}
		if port := named(at(rendered.Find(objects, "Service", "aviary-supervisor"), "spec", "ports"), "resolver"); at(port, "port") != 9443 {
			t.Errorf("%s: the resolver is reached at %v", values, port)
		}
	}
}

// The Supervisor finds the keys that it seals secrets with where it is told
// that they are: the entries of the secret that the values name.
func TestSupervisorEncryptionKeys(t *testing.T) {
	for values, secret := range map[string]string{"default": "aviary-encryption", "set": "other-keys"} {
		objects := chart(t, values)
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

// The Supervisor may do in the cluster what it takes to keep the sandboxes
// of the sessions, and no more.
func TestSupervisorPermissions(t *testing.T) {
	objects := chart(t, "default")
	role := rendered.Find(objects, "Role", "aviary-supervisor")
	expected := []any{map[string]any{
		"apiGroups": []any{"agents.x-k8s.io"}, "resources": []any{"sandboxes"}, "verbs": []any{"create", "get", "list", "patch", "delete"},
	}}
	if at(role, "metadata", "namespace") != "aviary-runtime" || !reflect.DeepEqual(at(role, "rules"), expected) {
		t.Errorf("the Supervisor may %v in the namespace %v", at(role, "rules"), at(role, "metadata", "namespace"))
	}
	binding := rendered.Find(objects, "RoleBinding", "aviary-supervisor")
	if at(binding, "metadata", "namespace") != "aviary-runtime" || at(binding, "roleRef", "name") != "aviary-supervisor" ||
		at(binding, "subjects", 0, "name") != "aviary-supervisor" || at(binding, "subjects", 0, "namespace") != "aviary-system" {
		t.Errorf("the role is bound as %v", binding)
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

// A sandbox connects to nothing, and only the Supervisors connect to it, at
// its runner. Its namespace stays when the chart is uninstalled, with the
// sessions in it.
func TestSandboxesAreCutOff(t *testing.T) {
	objects := chart(t, "default")
	policy := at(rendered.Find(objects, "NetworkPolicy", "aviary-sandboxes"), "spec")
	if at(rendered.Find(objects, "NetworkPolicy", "aviary-sandboxes"), "metadata", "namespace") != "aviary-runtime" ||
		len(at(policy, "podSelector").(map[string]any)) != 0 ||
		!reflect.DeepEqual(at(policy, "policyTypes"), []any{"Ingress", "Egress"}) || at(policy, "egress") != nil {
		t.Errorf("the policy of the sandboxes is %v", policy)
	}
	rules, _ := at(policy, "ingress").([]any)
	if len(rules) != 1 || len(at(rules[0], "from").([]any)) != 1 ||
		at(rules[0], "from", 0, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name") != "aviary-system" ||
		at(rules[0], "from", 0, "podSelector", "matchLabels", "app.kubernetes.io/component") != "supervisor" ||
		!reflect.DeepEqual(at(rules[0], "ports"), []any{map[string]any{"protocol": "TCP", "port": 8700}}) {
		t.Errorf("a sandbox may be connected to by %v", rules)
	}
	namespace := rendered.Find(objects, "Namespace", "aviary-runtime")
	if at(namespace, "metadata", "annotations", "helm.sh/resource-policy") != "keep" {
		t.Errorf("the namespace of the sandboxes is %v", namespace)
	}
}
