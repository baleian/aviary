package chart

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// container returns the container or init container of a workload by name.
func container(workload map[string]any, name string) any {
	pod := at(workload, "spec", "template", "spec")
	if found := named(at(pod, "containers"), name); found != nil {
		return found
	}
	return named(at(pod, "initContainers"), name)
}

func args(container any) []string {
	var list []string
	for _, arg := range at(container, "args").([]any) {
		list = append(list, arg.(string))
	}
	return list
}

// variables returns the environment of a container: a value, or the Secret
// and key that it comes from.
func variables(container any) map[string]string {
	list := map[string]string{}
	for _, variable := range at(container, "env").([]any) {
		name := at(variable, "name").(string)
		if value, ok := at(variable, "value").(string); ok {
			list[name] = value
		} else if secret := at(variable, "valueFrom", "secretKeyRef"); secret != nil {
			list[name] = "secret " + at(secret, "name").(string) + " " + at(secret, "key").(string)
		}
	}
	return list
}

// The chart installs Substrate for the Supervisors of the release, with the
// values of the installation.
func TestSubstrate(t *testing.T) {
	for _, test := range []struct {
		values, registry, location, upload, detach, database, storageSecret string
		storage                                                             map[string]string
		registryReplacement, credentialProvider                             bool
	}{
		// The store of the chart.
		{values: "default", registry: "ghcr.io/agent-substrate/substrate", location: "s3://snapshots/", upload: "5m", detach: "30m",
			database: "substrate-database", storageSecret: "substrate-store",
			storage: map[string]string{"AWS_REGION": "us-east-1", "AWS_ENDPOINT_URL": "http://substrate-store.aviary-system.svc:7070", "AWS_S3_USE_PATH_STYLE": "true",
				"AWS_ACCESS_KEY_ID": "secret substrate-store accessKeyId", "AWS_SECRET_ACCESS_KEY": "secret substrate-store secretAccessKey"}},
		{values: "substrate", registry: "mirror.example.com/substrate", location: "s3://snapshots/aviary/", upload: "1m", detach: "2h",
			database: "substrate-database", storageSecret: "object-storage",
			storage: map[string]string{"AWS_REGION": "eu-west-1", "AWS_ENDPOINT_URL": "http://s3.example.com:9000", "AWS_S3_USE_PATH_STYLE": "true",
				"AWS_ACCESS_KEY_ID": "secret object-storage accessKeyId", "AWS_SECRET_ACCESS_KEY": "secret object-storage secretAccessKey"},
			registryReplacement: true, credentialProvider: true},
	} {
		objects := chart(t, test.values)
		for _, object := range objects {
			switch object["kind"] {
			case "Deployment", "DaemonSet":
				pod := at(object, "spec", "template", "spec")
				for _, list := range []string{"containers", "initContainers"} {
					items, _ := at(pod, list).([]any)
					for _, item := range items {
						image := at(item, "image").(string)
						if strings.HasPrefix(image, "aviary-") || at(object, "metadata", "name") == "substrate-store" {
							continue
						}
						if !strings.HasPrefix(image, test.registry+"/") || !strings.Contains(image, ":v1.0.1@sha256:") {
							t.Errorf("%s: %v %v runs %s, want an image of the release at %s by digest",
								test.values, object["kind"], at(object, "metadata", "name"), image, test.registry)
						}
					}
				}
			}
		}

		if location := at(find(objects, "ConfigMap", "ate-api-server-envvars"), "data", "ATE_API_SNAPSHOT_LOCATION"); location != test.location {
			t.Errorf("%s: snapshot location = %v, want %s", test.values, location, test.location)
		}
		atelet := find(objects, "DaemonSet", "atelet")
		flags := args(container(atelet, "atelet"))
		for _, want := range []string{"--snapshot-upload-delay=" + test.upload, "--snapshot-detach-delay=" + test.detach} {
			if !slices.Contains(flags, want) {
				t.Errorf("%s: atelet args = %v, want %s", test.values, flags, want)
			}
		}
		replaced := slices.Contains(flags, "--localhost-registry-replacement=registry.example.com:5000")
		provided := slices.Contains(flags, "--image-credential-provider-bin-dir=/run/image-credential-provider/bin")
		volume := named(at(atelet, "spec", "template", "spec", "volumes"), "image-credential-provider-config")
		if replaced != test.registryReplacement || provided != test.credentialProvider || (volume != nil) != test.credentialProvider {
			t.Errorf("%s: atelet args = %v, credential provider volume %v", test.values, flags, volume)
		}
		if test.credentialProvider && at(volume, "hostPath", "path") != "/etc/image-credential-provider/config.json" {
			t.Errorf("%s: credential provider volume = %v", test.values, volume)
		}

		api := find(objects, "Deployment", "ate-api-server")
		for _, workload := range []map[string]any{api, atelet} {
			storage := variables(container(workload, "objectstore-plugin"))
			if storage["ATE_STORAGE_BACKEND"] != "s3" {
				t.Errorf("%s: %v stores snapshots in %q, want s3", test.values, at(workload, "metadata", "name"), storage["ATE_STORAGE_BACKEND"])
			}
			for name, want := range test.storage {
				if storage[name] != want {
					t.Errorf("%s: %v object storage %s = %q, want %q", test.values, at(workload, "metadata", "name"), name, storage[name], want)
				}
			}
			if _, set := storage["AWS_ACCESS_KEY_ID"]; set != (test.storageSecret != "") {
				t.Errorf("%s: %v object storage env = %v", test.values, at(workload, "metadata", "name"), storage)
			}
		}
		database := variables(container(api, "ate-api-server"))
		if database["ATE_API_POSTGRES_OWNER_CONNECTION_STRING"] != "secret "+test.database+" owner" ||
			database["ATE_API_POSTGRES_READ_WRITE_CONNECTION_STRING"] != "secret "+test.database+" readWrite" {
			t.Errorf("%s: API server env = %v, want the connection strings of %s", test.values, database, test.database)
		}
		if owners := args(container(api, "ate-api-server")); !slices.Contains(owners,
			"--authz-bootstrap-owners=system:serviceaccount:ate-system:ate-client,system:serviceaccount:aviary-system:aviary-supervisor") {
			t.Errorf("%s: API server args = %v, want the Supervisors as owners", test.values, owners)
		}
		egress := args(container(find(objects, "Deployment", "atenet-egress"), "ext-proc"))
		for _, want := range []string{"--credential-provider-name=aviary.io",
			"--credential-provider-address=aviary-supervisor.aviary-system.svc:50051",
			"--credential-provider-server-name=aviary-supervisor.aviary-system.svc"} {
			if !slices.Contains(egress, want) {
				t.Errorf("%s: egress gateway args = %v, want %s", test.values, egress, want)
			}
		}
		router := args(container(find(objects, "Deployment", "atenet-router"), "atenet-router"))
		if !slices.Contains(router, "--ingress-allowed-spiffe-ids=spiffe://cluster.local/ns/ate-system/sa/atenet-client,spiffe://cluster.local/ns/aviary-system/sa/aviary-supervisor") {
			t.Errorf("%s: router args = %v, want the Supervisors let in", test.values, router)
		}
	}
}

// Substrate's own Secrets come from the values when they name none, and the
// API server checks the tokens of the cluster's issuer.
func TestSubstrateSettings(t *testing.T) {
	objects := chart(t, "default")
	if find(objects, "Secret", "substrate-database") != nil || find(objects, "Secret", "substrate-object-storage") != nil {
		t.Error("chart makes Secrets of Substrate although the values name them or none")
	}
	authentication := at(find(objects, "ConfigMap", "ate-api-authentication"), "data", "authentication.yaml").(string)
	if !strings.Contains(authentication, "issuer: https://kubernetes.default.svc.cluster.local") ||
		!strings.Contains(authentication, "discoveryTokenFile:") {
		t.Errorf("authentication = %s, want the issuer inside the cluster, discovered with the token of the API server", authentication)
	}
	var keys map[string]any
	for _, object := range objects {
		if object["kind"] == "Job" && strings.HasPrefix(at(object, "metadata", "name").(string), "substrate-keys-") {
			keys = object
		}
	}
	if at(keys, "metadata", "namespace") != "ate-system" || at(keys, "spec", "template", "spec", "containers", 0, "image") != "aviary-substrate-keys:dev" {
		t.Errorf("keys Job = %v", keys)
	}

	objects = chart(t, "substrate")
	secret := find(objects, "Secret", "substrate-database")
	if !reflect.DeepEqual(at(secret, "stringData"), map[string]any{
		"owner": "postgresql://owner:p1@db.example.com/substrate", "readWrite": "postgresql://readwrite:p2@db.example.com/substrate"}) {
		t.Errorf("database Secret = %v", secret)
	}
	authentication = at(find(objects, "ConfigMap", "ate-api-authentication"), "data", "authentication.yaml").(string)
	if !strings.Contains(authentication, "issuer: https://oidc.example.com/id/0123") || strings.Contains(authentication, "discoveryTokenFile:") {
		t.Errorf("authentication = %s, want the issuer of the values, discovered publicly", authentication)
	}
	for _, name := range []string{"ate-api-server", "atelet"} {
		account := find(objects, "ServiceAccount", name)
		if at(account, "metadata", "annotations", "eks.amazonaws.com/role-arn") != "arn:aws:iam::111111111111:role/substrate" {
			t.Errorf("service account %s = %v, want the annotations of the values", name, at(account, "metadata"))
		}
	}
	if at(find(objects, "ConfigMap", "ate-otel-config"), "data", "OTEL_EXPORTER_OTLP_ENDPOINT") != "http://collector.example.com:4317" {
		t.Errorf("telemetry = %v", find(objects, "ConfigMap", "ate-otel-config"))
	}

	// An installation whose Substrate is installed apart.
	for _, object := range chart(t, "set") {
		if namespace := at(object, "metadata", "namespace"); namespace == "ate-system" || namespace == "podcertificate-controller-system" ||
			at(object, "metadata", "name") == "substrate-store" {
			t.Errorf("chart renders the %v %v of Substrate although it does not install it", object["kind"], at(object, "metadata", "name"))
		}
	}
}

// Without a location of the snapshots, the store of the chart keeps them in
// a volume, which it makes or which the values name, and only the API
// server and atelet reach it.
func TestSubstrateStore(t *testing.T) {
	for _, test := range []struct {
		values, claim  string
		made, together bool
	}{
		{values: "default", claim: "substrate-store", made: true, together: true},
		{values: "claim", claim: "snapshots"},
	} {
		objects := chart(t, test.values)
		store := find(objects, "Deployment", "substrate-store")
		pod := at(store, "spec", "template", "spec")
		if at(named(at(pod, "volumes"), "store"), "persistentVolumeClaim", "claimName") != test.claim {
			t.Errorf("%s: store volumes = %v, want the claim %s", test.values, at(pod, "volumes"), test.claim)
		}
		claim := find(objects, "PersistentVolumeClaim", "substrate-store")
		if (claim != nil) != test.made || (test.made && at(claim, "metadata", "annotations", "helm.sh/resource-policy") != "keep") {
			t.Errorf("%s: store claim = %v", test.values, claim)
		}
		if (at(pod, "affinity", "podAffinity") != nil) != test.together {
			t.Errorf("%s: store affinity = %v", test.values, at(pod, "affinity"))
		}
		if at(store, "metadata", "namespace") != nil {
			t.Errorf("%s: store namespace = %v, want that of the release", test.values, at(store, "metadata", "namespace"))
		}
		var namespaces []any
		for _, object := range objects {
			if object["kind"] == "Secret" && at(object, "metadata", "name") == "substrate-store" {
				namespaces = append(namespaces, at(object, "metadata", "namespace"))
			}
		}
		if !reflect.DeepEqual(namespaces, []any{"aviary-system", "ate-system"}) {
			t.Errorf("%s: store key in the namespaces %v, want the release's and ate-system", test.values, namespaces)
		}
		from := at(find(objects, "NetworkPolicy", "substrate-store"), "spec", "ingress", 0, "from", 0)
		if at(from, "namespaceSelector", "matchLabels", "kubernetes.io/metadata.name") != "ate-system" ||
			!reflect.DeepEqual(at(from, "podSelector", "matchExpressions", 0, "values"), []any{"ate-api-server", "atelet"}) {
			t.Errorf("%s: store reached from %v, want the API server and atelet", test.values, from)
		}
	}

	objects := chart(t, "substrate")
	if find(objects, "Deployment", "substrate-store") != nil || find(objects, "Secret", "substrate-store") != nil {
		t.Error("chart renders the store although the values name a location")
	}
}
