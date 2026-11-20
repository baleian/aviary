// Package chart checks what the chart makes of its values. `make test-chart`
// renders the chart with its own values and with those of the files in this
// directory, and tells the tests where the results are.
package chart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		"AVIARY_SANDBOXES_URL":             "http://opensandbox-server.opensandbox-system",
		"AVIARY_TEMPLATE_STORE":            "s3://sandbox-images/publish",
		"AVIARY_SANDBOX_IDLE_SECONDS":      "300",
		"AVIARY_SANDBOX_HOSTS":             `["*"]`,
		"AVIARY_RESOLVER_URL":              "https://aviary-supervisor.aviary-system.svc.cluster.local:9443",
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
		"AVIARY_SANDBOXES_URL":             "https://sandboxes.example.com",
		"AVIARY_TEMPLATE_STORE":            "s3://example/templates",
		"AVIARY_SANDBOX_IDLE_SECONDS":      "0",
		"AVIARY_SANDBOX_HOSTS":             `["pypi.org","*.example.com"]`,
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
	var harnesses map[string]struct {
		Image   string
		Sandbox map[string]string
	}
	if err := json.Unmarshal([]byte(settings["AVIARY_HARNESSES"]), &harnesses); err != nil {
		t.Fatalf("the Supervisor is told the harnesses %s: %v", settings["AVIARY_HARNESSES"], err)
	}
	opencode, codex := harnesses["opencode"], harnesses["codex"]
	if len(harnesses) != 2 || opencode.Image != "aviary-harness-opencode:dev" || codex.Image != "aviary-harness-codex:dev" ||
		opencode.Sandbox["disk"] != "16Gi" || opencode.Sandbox["memory"] != "2Gi" || codex.Sandbox["disk"] != "4Gi" {
		t.Errorf("the Supervisor is told the harnesses %s", settings["AVIARY_HARNESSES"])
	}

	// A Supervisor finds the template of a harness by what the release had
	// it built from, so both are told the same of it, and the Supervisor
	// asks the sandbox service with the same key.
	built := rendered.Find(objects, "Job", "aviary-templates")
	for _, name := range []string{"AVIARY_SANDBOXES_URL", "AVIARY_TEMPLATE_STORE", "AVIARY_HARNESSES", "AVIARY_MAX_TOOL_OUTPUT_BYTES"} {
		if settings[name] == "" || settings[name] != env(built)[name] {
			t.Errorf("the Supervisor is told %s=%q, and the Job that builds the templates %q", name, settings[name], env(built)[name])
		}
	}
	key := func(object map[string]any) any {
		return at(named(at(object, "spec", "template", "spec", "containers", 0, "env"), "AVIARY_SANDBOXES_API_KEY"), "valueFrom", "secretKeyRef")
	}
	if key(supervisor) == nil || !reflect.DeepEqual(key(supervisor), key(built)) {
		t.Errorf("the Supervisor asks the sandbox service with the key %v, and the Job with %v", key(supervisor), key(built))
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
		port := named(at(rendered.Find(objects, "Service", "aviary-supervisor"), "spec", "ports"), "resolver")
		if at(port, "port") != 9443 || !strings.HasSuffix(env(supervisor)["AVIARY_RESOLVER_URL"], ":9443") {
			t.Errorf("%s: the egress reaches the Supervisor at %v, and the Supervisor says %s", values, port, env(supervisor)["AVIARY_RESOLVER_URL"])
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

// The Supervisor may do nothing in the cluster: the sessions run in the
// sandboxes of the sandbox service, which it asks.
func TestSupervisorPermissions(t *testing.T) {
	objects := chart(t, "default")
	for _, object := range objects {
		switch kind := object["kind"]; kind {
		case "Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding":
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
	objects := chart(t, "default")
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
	objects = chart(t, "set")
	settings = env(rendered.Find(objects, "Job", "aviary-templates"))
	if settings["AVIARY_SANDBOXES_URL"] != "https://sandboxes.example.com" || settings["AVIARY_TEMPLATE_STORE"] != "s3://example/templates" ||
		!strings.Contains(settings["AVIARY_HARNESSES"], `"disk":"16Gi"`) {
		t.Errorf("the Job of an installation with its own sandbox service is told %v", settings)
	}
}
