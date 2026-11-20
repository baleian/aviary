package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/resource"

	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// settings are the variables of a Supervisor as the chart sets them.
var settings = map[string]string{
	sandboxNamespace: "aviary-runtime",
	sandboxVolume:    "10Gi",
	sandboxIdle:      "300",
	sandboxRetention: "0",
	modelsURL:        "http://aviary-egress-proxy.aviary-system:8080",
	proxyURL:         "http://aviary-egress-proxy.aviary-system:3128",
	harnesses: `{"opencode": {"image": "registry.example/opencode:1",
		"resources": {"requests": {"cpu": "250m", "memory": "1Gi"}, "limits": {"memory": "4Gi"}}}}`,
	maxConcurrentTurns: "50",
	maxQueuedTurns:     "500",
	maxQueueWait:       "120",
	maxStartup:         "90",
	maxToolOutput:      "65536",
	maxTurn:            "1800",
}

// set sets the variables, with some of them changed.
func set(t *testing.T, changed map[string]string) {
	for name, value := range settings {
		t.Setenv(name, value)
	}
	for name, value := range changed {
		t.Setenv(name, value)
	}
}

func TestDriverConfig(t *testing.T) {
	t.Run("as the chart sets them", func(t *testing.T) {
		set(t, nil)
		config, err := driverConfig()
		if err != nil {
			t.Fatal(err)
		}
		opencode := config.Harnesses["opencode"]
		if len(config.Harnesses) != 1 || opencode.Image != "registry.example/opencode:1" ||
			!opencode.Resources.Requests.Cpu().Equal(resource.MustParse("250m")) || !opencode.Resources.Limits.Memory().Equal(resource.MustParse("4Gi")) {
			t.Errorf("the turns are run with %+v", config)
		}
		if config.MaxConcurrentTurns != 50 || config.MaxQueuedTurns != 500 || config.MaxQueueWait != 2*time.Minute ||
			config.MaxStartup != 90*time.Second || config.MaxTurn != 30*time.Minute || config.Idle != 5*time.Minute || config.Retention != 0 {
			t.Errorf("the limits are %+v", config)
		}
		if config.Models.String() != settings[modelsURL] {
			t.Errorf("the harnesses reach their models at %s", config.Models)
		}
	})

	t.Run("without the limits that there need not be", func(t *testing.T) {
		set(t, map[string]string{maxQueuedTurns: "0", maxQueueWait: "0", maxStartup: "0", sandboxIdle: "0", sandboxRetention: "1"})
		config, err := driverConfig()
		if err != nil {
			t.Fatal(err)
		}
		if config.MaxQueuedTurns != 0 || config.MaxQueueWait != 0 || config.MaxStartup != 0 || config.Idle != 0 || config.Retention != time.Second {
			t.Errorf("the limits are %+v", config)
		}
	})

	for _, wrong := range []struct{ name, value string }{
		{modelsURL, ""}, {modelsURL, "aviary-egress-proxy:8080"}, {modelsURL, "https://aviary-egress-proxy:8080"},
		{harnesses, ""}, {harnesses, `{"opencode": `},
		{harnesses, `{"opencode": {"resources": {"requests": {"cpu": "1"}}}}`},
		{harnesses, `{"opencode": {"image": "registry.example/opencode:1", "resources": {"requests": {"cpu": "lots"}}}}`},
		{sandboxIdle, ""}, {sandboxIdle, "-1"}, {sandboxIdle, "5m"},
		{sandboxRetention, ""}, {sandboxRetention, "-1"},
		{maxConcurrentTurns, ""}, {maxConcurrentTurns, "0"}, {maxConcurrentTurns, "many"},
		{maxQueuedTurns, ""}, {maxQueuedTurns, "-1"},
		{maxQueueWait, ""}, {maxQueueWait, "-1"}, {maxQueueWait, "2m"},
		{maxStartup, ""}, {maxStartup, "-1"},
		{maxTurn, ""}, {maxTurn, "0"},
	} {
		t.Run(wrong.name+"="+wrong.value, func(t *testing.T) {
			set(t, map[string]string{wrong.name: wrong.value})
			// A Supervisor with a setting that it cannot use does not start,
			// and says which setting it is.
			if config, err := driverConfig(); err == nil || !strings.Contains(err.Error(), wrong.name) {
				t.Errorf("the turns are run with %+v: %v", config, err)
			}
		})
	}
}

func TestSandboxSettings(t *testing.T) {
	set(t, map[string]string{sandboxClass: "fast"})
	made, err := sandboxSettings()
	if err != nil {
		t.Fatal(err)
	}
	if made.Namespace != "aviary-runtime" || !made.Volume.Equal(resource.MustParse("10Gi")) || made.StorageClass != "fast" || made.MaxToolOutputBytes != 65536 ||
		made.Proxy.String() != settings[proxyURL] || made.Models.String() != settings[modelsURL] {
		t.Errorf("the sandboxes are made with %+v", made)
	}
	// Without a class, the volumes are of the default class of the cluster.
	set(t, map[string]string{sandboxClass: "", maxToolOutput: "0"})
	if made, err := sandboxSettings(); err != nil || made.StorageClass != "" || made.MaxToolOutputBytes != 0 {
		t.Errorf("the sandboxes are made with %+v: %v", made, err)
	}

	for _, wrong := range []struct{ name, value string }{
		{sandboxNamespace, ""},
		{sandboxVolume, ""}, {sandboxVolume, "large"}, {sandboxVolume, "0"},
		{maxToolOutput, ""}, {maxToolOutput, "-1"},
		{proxyURL, ""}, {proxyURL, "http://"}, {modelsURL, ""},
	} {
		set(t, map[string]string{wrong.name: wrong.value})
		if made, err := sandboxSettings(); err == nil || !strings.Contains(err.Error(), wrong.name) {
			t.Errorf("with %s=%q, the sandboxes are made with %+v: %v", wrong.name, wrong.value, made, err)
		}
	}
}

// The Supervisor is told at which hosts the projects may have secret stores
// and for how long it is to use what it read from one, and asks the stores
// of the kinds that it was built with.
func TestSecretStoreSettings(t *testing.T) {
	t.Setenv(secretStoreHosts, `["secrets.example.com:8200"]`)
	t.Setenv(secretCacheSeconds, "60")
	plugins := []secret.Plugin{scripted{}}
	stores, err := allowedSecretStores(plugins)
	if err != nil {
		t.Fatal(err)
	}
	defer stores.Close()
	// A store of a kind that the build knows is held against the list.
	at := func(address string) json.RawMessage {
		return json.RawMessage(`{"address": "` + address + `", "takes": "a-credential"}`)
	}
	if err := stores.Check(context.Background(), "scripted", at("https://secrets.example.com:8200"), "a-credential"); err != nil {
		t.Errorf("a store at a host that is listed is checked as: %v", err)
	}
	err = stores.Check(context.Background(), "scripted", at("https://other.example.com:8200"), "a-credential")
	if err == nil || !strings.Contains(err.Error(), "supervisor.secretStores.allowedHosts") {
		t.Errorf("a store at a host that is not listed is checked as: %v", err)
	}
	if err := stores.Check(context.Background(), "another", at("https://secrets.example.com:8200"), "a-credential"); err == nil || !strings.Contains(err.Error(), "scripted") {
		t.Errorf("a store of a kind that the build does not know is checked as: %v", err)
	}

	// A value that was read from a store is used for as long as the
	// installation says, and with zero the store is read every time.
	const address = "https://secrets.example.com:8200"
	holding := &shelves{}
	registered := store.SecretStore{ID: uuid.New(), Kind: "scripted", Settings: at(address), UpdatedAt: time.Now()}
	for seconds, remembered := range map[string]string{"60": "the-first", "0": "the-second"} {
		t.Setenv(secretCacheSeconds, seconds)
		stores, err := allowedSecretStores([]secret.Plugin{scripted{holding}})
		if err != nil {
			t.Fatal(err)
		}
		defer stores.Close()
		holding.put(address, "a/path", "a-key", "the-first")
		if _, err := stores.Read(context.Background(), registered, "a-credential", "a/path", "a-key"); err != nil {
			t.Fatal(err)
		}
		holding.put(address, "a/path", "a-key", "the-second")
		if read, err := stores.Read(context.Background(), registered, "a-credential", "a/path", "a-key"); err != nil || read != remembered {
			t.Errorf("with %s seconds, the value that the store has changed was read as %q: %v", seconds, read, err)
		}
	}

	// A Supervisor with a setting that it cannot use does not start.
	for _, wrong := range []string{"", "-1", "soon", "1.5"} {
		t.Setenv(secretCacheSeconds, wrong)
		if stores, err := allowedSecretStores(plugins); err == nil || !strings.Contains(err.Error(), secretCacheSeconds) {
			t.Errorf("with %q seconds, the stores are %+v: %v", wrong, stores, err)
		}
	}
	t.Setenv(secretCacheSeconds, "60")
	for _, wrong := range []string{"", "secrets.example.com", `["https://secrets.example.com"]`} {
		t.Setenv(secretStoreHosts, wrong)
		if stores, err := allowedSecretStores(plugins); err == nil {
			t.Errorf("with the hosts %q, the stores are %+v", wrong, stores)
		}
	}
}
