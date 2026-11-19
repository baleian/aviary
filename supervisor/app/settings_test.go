package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// settings are the variables of a Supervisor as the chart sets them.
var settings = map[string]string{
	templateStore:      "s3://sandboxes/templates",
	sandboxIdle:        "300",
	sandboxRetention:   "0",
	harnesses:          `{"opencode": {"image": "registry.example/opencode:1", "sandbox": {"cpu": "1", "memory": "2Gi", "disk": "4Gi"}}}`,
	maxConcurrentTurns: "50",
	maxQueuedTurns:     "500",
	maxQueueWait:       "120",
	maxStartup:         "90",
	maxToolOutput:      "65536",
	maxTurn:            "1800",
}

func TestDriverConfig(t *testing.T) {
	// set sets the variables, with some of them changed.
	set := func(t *testing.T, changed map[string]string) {
		for name, value := range settings {
			t.Setenv(name, value)
		}
		for name, value := range changed {
			t.Setenv(name, value)
		}
	}

	t.Run("as the chart sets them", func(t *testing.T) {
		set(t, nil)
		config, err := driverConfig()
		if err != nil {
			t.Fatal(err)
		}
		templates := config.Templates
		opencode := sandbox.Harness{Image: "registry.example/opencode:1", Sandbox: sandbox.Size{CPU: "1", Memory: "2Gi", Disk: "4Gi"}}
		if templates.Store != settings[templateStore] || len(templates.Harnesses) != 1 || templates.Harnesses["opencode"] != opencode {
			t.Errorf("the turns are run with %+v", config)
		}
		if config.MaxConcurrentTurns != 50 || config.MaxQueuedTurns != 500 || config.MaxQueueWait != 2*time.Minute ||
			config.MaxStartup != 90*time.Second || templates.MaxToolOutputBytes != 65536 || config.MaxTurn != 30*time.Minute ||
			config.Idle != 5*time.Minute || config.Retention != 0 {
			t.Errorf("the limits are %+v", config)
		}
	})

	t.Run("without the limits that there need not be", func(t *testing.T) {
		set(t, map[string]string{
			maxQueuedTurns: "0", maxQueueWait: "0", maxStartup: "0", maxToolOutput: "0", sandboxIdle: "0",
		})
		config, err := driverConfig()
		if err != nil {
			t.Fatal(err)
		}
		if config.MaxQueuedTurns != 0 || config.MaxQueueWait != 0 || config.MaxStartup != 0 || config.Templates.MaxToolOutputBytes != 0 ||
			config.Idle != 0 {
			t.Errorf("the limits are %+v", config)
		}
	})

	for _, wrong := range []struct{ name, value string }{
		{templateStore, ""},
		{harnesses, ""}, {harnesses, `{"opencode": `},
		{harnesses, `{"opencode": {"image": "registry.example/opencode:1"}}`},
		{harnesses, `{"opencode": {"sandbox": {"cpu": "1", "memory": "2Gi", "disk": "4Gi"}}}`},
		// The sandbox service puts no sandbox to sleep sooner than after
		// thirty seconds.
		{sandboxIdle, ""}, {sandboxIdle, "-1"}, {sandboxIdle, "29"}, {sandboxIdle, "5m"},
		// Nor does it keep one for less than a minute.
		{sandboxRetention, ""}, {sandboxRetention, "-1"}, {sandboxRetention, "59"},
		{maxConcurrentTurns, ""}, {maxConcurrentTurns, "0"}, {maxConcurrentTurns, "many"},
		{maxQueuedTurns, ""}, {maxQueuedTurns, "-1"},
		{maxQueueWait, ""}, {maxQueueWait, "-1"}, {maxQueueWait, "2m"},
		{maxStartup, ""}, {maxStartup, "-1"},
		{maxToolOutput, ""}, {maxToolOutput, "-1"},
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
