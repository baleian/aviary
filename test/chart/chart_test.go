// Package chart tests the objects that the chart renders. `make test-chart`
// renders the chart with its default values and with each values file in
// this directory, and sets AVIARY_TEST_RENDERED to where the results are.
package chart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"aviary/test/internal/rendered"
)

// chart returns the objects rendered from the named values.
func chart(t *testing.T, values string) []map[string]any {
	t.Helper()
	directory := os.Getenv("AVIARY_TEST_RENDERED")
	if directory == "" {
		t.Fatal("AVIARY_TEST_RENDERED is not set: run the tests with `make test-chart`")
	}
	objects, err := rendered.Read(filepath.Join(directory, values+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return objects
}

var at, named, find = rendered.At, rendered.Named, rendered.Find

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

func TestSupervisorSettings(t *testing.T) {
	objects := chart(t, "default")
	settings := env(find(objects, "Deployment", "aviary-supervisor"))
	for name, value := range map[string]string{
		"AVIARY_ENCRYPTION_KEY":            "k1",
		"AVIARY_SECRET_STORE_HOSTS":        "[]",
		"AVIARY_SECRET_CACHE_SECONDS":      "60",
		"AVIARY_IDENTITY_PROVIDER":         `{"kind":"","settings":{}}`,
		"AVIARY_ADMIN_GROUPS":              "[]",
		"AVIARY_USER_GROUPS":               "[]",
		"AVIARY_ALLOWED_ORIGINS":           "[]",
		"AVIARY_SANDBOX_ATESPACE":          "aviary",
		"AVIARY_SANDBOX_POOL":              `{"aviary.io/pool":"aviary-system"}`,
		"AVIARY_SANDBOX_SNAPSHOT":          "memory",
		"AVIARY_SANDBOX_HOSTS":             `["https://*","http://*"]`,
		"AVIARY_SANDBOX_IDLE_SECONDS":      "300",
		"AVIARY_SANDBOX_RETENTION_SECONDS": "0",
		"AVIARY_LLM_LOCALHOST":             "",
		"AVIARY_OBJECT_STORAGE":            "s3://aviary/",
		"AVIARY_HARNESSES":                 "{}",
		"AVIARY_MAX_CONCURRENT_TURNS":      "50",
		"AVIARY_MAX_QUEUED_TURNS":          "500",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS":    "120",
		"AVIARY_MAX_STARTUP_SECONDS":       "120",
		"AVIARY_MAX_TURN_SECONDS":          "1800",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":     "65536",
		"AVIARY_MAX_ATTACHMENT_BYTES":      "67108864",
		"AVIARY_MAX_ATTACHMENTS":           "10",
	} {
		if settings[name] != value {
			t.Errorf("Supervisor env %s = %q, want %q", name, settings[name], value)
		}
	}

	// The model is set per agent, not per installation.
	if model, told := settings["AVIARY_LLM_MODEL"]; told {
		t.Errorf("Supervisor env AVIARY_LLM_MODEL = %q, want unset", model)
	}

	objects = chart(t, "set")
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
		"AVIARY_SANDBOX_ATESPACE":          "team-a",
		"AVIARY_SANDBOX_SNAPSHOT":          "rootfs",
		"AVIARY_SANDBOX_HOSTS":             `["https://llm.example.com","https://*.packages.example.com","https://git.example.com:8443"]`,
		"AVIARY_SANDBOX_IDLE_SECONDS":      "0",
		"AVIARY_SANDBOX_RETENTION_SECONDS": "604800",
		"AVIARY_LLM_LOCALHOST":             "host.example.com",
		"AVIARY_OBJECT_STORAGE":            "s3://bucket/aviary/",
		"AWS_REGION":                       "us-east-1",
		"AWS_S3_USE_PATH_STYLE":            "false",
		"AVIARY_MAX_CONCURRENT_TURNS":      "7",
		"AVIARY_MAX_QUEUED_TURNS":          "11",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS":    "13",
		"AVIARY_MAX_STARTUP_SECONDS":       "17",
		"AVIARY_MAX_TURN_SECONDS":          "7200",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":     "23",
		"AVIARY_MAX_ATTACHMENT_BYTES":      "29",
		"AVIARY_MAX_ATTACHMENTS":           "0",
	} {
		if settings[name] != value {
			t.Errorf("Supervisor env %s = %q, want %q", name, settings[name], value)
		}
	}
	if replicas := at(supervisor, "spec", "replicas"); replicas != 3 {
		t.Errorf("Supervisor replicas = %v, want 3", replicas)
	}
	type harness struct {
		Image     string
		Resources map[string]map[string]string
	}
	var harnesses map[string]harness
	if err := json.Unmarshal([]byte(settings["AVIARY_HARNESSES"]), &harnesses); err != nil {
		t.Fatalf("parse AVIARY_HARNESSES %s: %v", settings["AVIARY_HARNESSES"], err)
	}
	want := map[string]harness{
		"opencode": {"registry.example.com/aviary-harness-opencode@sha256:2222222222222222222222222222222222222222222222222222222222222222",
			map[string]map[string]string{"limits": {"cpu": "2", "memory": "8Gi"}}},
		"codex": {"registry.example.com/aviary-harness-codex@sha256:3333333333333333333333333333333333333333333333333333333333333333",
			map[string]map[string]string{"limits": {"cpu": "1", "memory": "1Gi"}}},
	}
	if !reflect.DeepEqual(harnesses, want) {
		t.Errorf("AVIARY_HARNESSES = %s", settings["AVIARY_HARNESSES"])
	}
}

// A number of a million or more in a values file is rendered as it is set,
// not in the notation of a floating-point number, which the Supervisor does
// not read.
func TestNumbersOfAMillionOrMore(t *testing.T) {
	objects := chart(t, "large")
	settings := env(find(objects, "Deployment", "aviary-supervisor"))
	for name, value := range map[string]string{
		"AVIARY_SECRET_CACHE_SECONDS":      "1000001",
		"AVIARY_SANDBOX_IDLE_SECONDS":      "1000002",
		"AVIARY_SANDBOX_RETENTION_SECONDS": "31536000",
		"AVIARY_MAX_CONCURRENT_TURNS":      "1000003",
		"AVIARY_MAX_QUEUED_TURNS":          "1000004",
		"AVIARY_MAX_QUEUE_WAIT_SECONDS":    "1000005",
		"AVIARY_MAX_STARTUP_SECONDS":       "1000006",
		"AVIARY_MAX_TURN_SECONDS":          "1000008",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES":     "1048576",
		"AVIARY_MAX_ATTACHMENT_BYTES":      "1073741824",
		"AVIARY_MAX_ATTACHMENTS":           "1000009",
	} {
		if settings[name] != value {
			t.Errorf("Supervisor env %s = %q, want %q", name, settings[name], value)
		}
	}
}

// The Supervisor reaches Substrate in the namespace that the values name: its
// API with a token of the Supervisor's service account, and its router with
// the certificate of the Supervisor's identity. The egress gateway, whose
// identity the Supervisor checks, gets secrets on a port of their own, with
// the certificate of the Supervisor's Service. The kubelet renews the token
// and the certificates in their files.
func TestSupervisorReachesSubstrate(t *testing.T) {
	for values, namespace := range map[string]string{"default": "ate-system", "set": "substrate-system"} {
		objects := chart(t, values)
		supervisor := find(objects, "Deployment", "aviary-supervisor")
		settings := env(supervisor)
		for name, value := range map[string]string{
			"AVIARY_SUBSTRATE_API":              "api." + namespace + ".svc.cluster.local:443",
			"AVIARY_SUBSTRATE_AUTHORITY":        "api." + namespace + ".svc",
			"AVIARY_SUBSTRATE_ROUTER":           "https://atenet-router." + namespace + ".svc.cluster.local:443",
			"AVIARY_SUBSTRATE_ROUTER_NAME":      "atenet-router." + namespace + ".svc",
			"AVIARY_SUBSTRATE_EGRESS_GATEWAY":   "spiffe://cluster.local/ns/" + namespace + "/sa/atenet-egress",
			"AVIARY_SUBSTRATE_TOKEN_FILE":       "/var/run/substrate/token/token",
			"AVIARY_SUBSTRATE_CA_FILE":          "/var/run/substrate/servicedns/trust-bundle.pem",
			"AVIARY_SUBSTRATE_IDENTITY_FILE":    "/var/run/substrate/podidentity/credential-bundle.pem",
			"AVIARY_CREDENTIALS_CERT_FILE":      "/var/run/substrate/servicedns/credential-bundle.pem",
			"AVIARY_CREDENTIALS_CLIENT_CA_FILE": "/var/run/substrate/podidentity/trust-bundle.pem",
		} {
			if settings[name] != value {
				t.Errorf("%s: Supervisor env %s = %q, want %q", values, name, settings[name], value)
			}
		}
		pod := at(supervisor, "spec", "template", "spec")
		container := at(pod, "containers", 0)
		for volume, path := range map[string]string{
			"substrate-token": "/var/run/substrate/token", "servicedns": "/var/run/substrate/servicedns", "podidentity": "/var/run/substrate/podidentity",
		} {
			if mount := named(at(container, "volumeMounts"), volume); at(mount, "mountPath") != path || at(mount, "readOnly") != true {
				t.Errorf("%s: mount of %s = %v, want it read-only at %s", values, volume, mount, path)
			}
		}
		token := at(named(at(pod, "volumes"), "substrate-token"), "projected", "sources", 0, "serviceAccountToken")
		if at(token, "audience") != "api."+namespace+".svc" || at(token, "path") != "token" {
			t.Errorf("%s: token = %v, want one for the API of Substrate", values, token)
		}
		for volume, signer := range map[string]string{
			"servicedns": "servicedns.podcert.ate.dev/identity", "podidentity": "podidentity.podcert.ate.dev/identity",
		} {
			sources := at(named(at(pod, "volumes"), volume), "projected", "sources")
			if at(sources, 0, "podCertificate", "signerName") != signer || at(sources, 0, "podCertificate", "credentialBundlePath") != "credential-bundle.pem" ||
				at(sources, 1, "clusterTrustBundle", "signerName") != signer || at(sources, 1, "clusterTrustBundle", "path") != "trust-bundle.pem" {
				t.Errorf("%s: volume %s = %v, want the certificate and the trust bundle of %s", values, volume, sources, signer)
			}
		}
		// The Supervisor has no access to the cluster itself.
		if at(pod, "automountServiceAccountToken") != false || at(pod, "serviceAccountName") != "aviary-supervisor" {
			t.Errorf("%s: Supervisor service account %v, token mounted %v; want aviary-supervisor, without its token",
				values, at(pod, "serviceAccountName"), at(pod, "automountServiceAccountToken"))
		}
		port := named(at(find(objects, "Service", "aviary-supervisor"), "spec", "ports"), "credentials")
		if at(port, "port") != 50051 || at(port, "targetPort") != "credentials" || at(named(at(container, "ports"), "credentials"), "containerPort") != 50051 {
			t.Errorf("%s: credentials port of the Service = %v, want 50051 to the container's", values, port)
		}
		policy := at(find(objects, "NetworkPolicy", "aviary-supervisor"), "spec")
		gateway := map[string]any{
			"from": []any{map[string]any{
				"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": namespace}},
				"podSelector":       map[string]any{"matchLabels": map[string]any{"app": "atenet-egress"}},
			}},
			"ports": []any{map[string]any{"protocol": "TCP", "port": 50051}},
		}
		if !reflect.DeepEqual(at(policy, "podSelector", "matchLabels"), at(supervisor, "spec", "selector", "matchLabels")) ||
			count(at(policy, "ingress")) != 2 || !reflect.DeepEqual(at(policy, "ingress", 1), gateway) ||
			!reflect.DeepEqual(at(policy, "ingress", 0, "ports"), []any{map[string]any{"protocol": "TCP", "port": 8080}, map[string]any{"protocol": "TCP", "port": 9092}}) {
			t.Errorf("%s: Supervisor NetworkPolicy = %v, want anyone at the API and the metrics, and only the egress gateway at the secrets", values, policy)
		}
	}
}

// The workers of the sandboxes are a WorkerPool of the size that the values
// say, whose labels the templates of the sandboxes select.
func TestWorkers(t *testing.T) {
	for _, test := range []struct {
		values, image string
		replicas      int
		limits        map[string]any
	}{
		{"default", "ghcr.io/agent-substrate/substrate/ateworker-gvisor:v1.0.1@sha256:fd0c6a46d6c69d485856171e632906e774594bea9d00011a4d7038be3e084a2a",
			2, map[string]any{"cpu": "8", "memory": "16Gi"}},
		{"set", "registry.example.com/ateworker-gvisor@sha256:1111111111111111111111111111111111111111111111111111111111111111",
			5, map[string]any{"cpu": "16", "memory": "64Gi"}},
	} {
		objects := chart(t, test.values)
		pool := find(objects, "WorkerPool", "aviary")
		if at(pool, "metadata", "namespace") != "aviary-runtime" || find(objects, "Namespace", "aviary-runtime") == nil {
			t.Errorf("%s: WorkerPool namespace = %v, want aviary-runtime, which the chart makes", test.values, at(pool, "metadata", "namespace"))
		}
		spec := at(pool, "spec")
		if at(spec, "replicas") != test.replicas || at(spec, "workerImage") != test.image ||
			!reflect.DeepEqual(at(spec, "sandboxClasses"), []any{map[string]any{"name": "gvisor"}}) ||
			!reflect.DeepEqual(at(spec, "template", "resources", "limits"), test.limits) {
			t.Errorf("%s: WorkerPool spec = %v", test.values, spec)
		}
		var selected map[string]any
		if err := json.Unmarshal([]byte(env(find(objects, "Deployment", "aviary-supervisor"))["AVIARY_SANDBOX_POOL"]), &selected); err != nil ||
			!reflect.DeepEqual(selected, at(pool, "metadata", "labels")) {
			t.Errorf("%s: sandboxes select %v, %v; want the labels of the WorkerPool, %v", test.values, selected, err, at(pool, "metadata", "labels"))
		}
	}
}

// One pod serves the state metrics. Of the Supervisor's settings it gets only
// the database and the harnesses, and it has no access to the cluster.
func TestStateMetrics(t *testing.T) {
	objects := chart(t, "default")
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

	objects = chart(t, "set")
	if served := find(objects, "Deployment", "aviary-state-metrics"); served != nil {
		t.Errorf("chart renders the state metrics although they are disabled: %v", served)
	}
}

// No component of Aviary has permissions in the cluster: no binding names a
// service account of the namespace of the release. Substrate has its own.
func TestNoPermissions(t *testing.T) {
	for _, object := range chart(t, "default") {
		switch object["kind"] {
		case "RoleBinding", "ClusterRoleBinding":
			subjects, _ := at(object, "subjects").([]any)
			for _, subject := range subjects {
				if at(subject, "namespace") == "aviary-system" {
					t.Errorf("the %v %v binds %v", object["kind"], at(object, "metadata", "name"), subject)
				}
			}
		case "Role", "ClusterRole":
			if at(object, "metadata", "namespace") == "aviary-system" {
				t.Errorf("chart renders the %v %v in the namespace of the release", object["kind"], at(object, "metadata", "name"))
			}
		}
	}
	// The schema is migrated once per release, before the Supervisors start.
	migrate := find(chart(t, "default"), "Job", "aviary-migrate")
	if at(migrate, "metadata", "annotations", "helm.sh/hook") != "pre-install,pre-upgrade" ||
		!reflect.DeepEqual(at(migrate, "spec", "template", "spec", "containers", 0, "args"), []any{"migrate"}) {
		t.Errorf("migration Job = %v", migrate)
	}
}

// The encryption keys are mounted from the secret that the values name, at
// the path in AVIARY_ENCRYPTION_KEYS.
func TestSupervisorEncryptionKeys(t *testing.T) {
	for values, secret := range map[string]string{"default": "aviary-encryption", "set": "other-keys"} {
		objects := chart(t, values)
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
