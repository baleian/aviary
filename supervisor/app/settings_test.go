package app

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/resource"

	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
)

// settings is a valid environment of a Supervisor, in the form that the chart
// sets.
var settings = map[string]string{
	sandboxAtespace:    "aviary",
	sandboxPool:        `{"aviary.io/pool": "sandboxes"}`,
	sandboxSnapshot:    "memory",
	sandboxHosts:       `["https://*", "http://pypi.example.com"]`,
	sandboxIdle:        "300",
	sandboxRetention:   "0",
	substrateAPI:       "api.ate-system.svc.cluster.local:443",
	substrateAuthority: "api.ate-system.svc",
	substrateToken:     "/var/run/secrets/substrate/token",
	substrateCA:        "/var/run/secrets/substrate/ca.pem",
	substrateRouter:    "https://atenet-router.ate-system.svc.cluster.local:443",
	routerName:         "atenet-router.ate-system.svc",
	substrateIdentity:  "/var/run/secrets/substrate/identity.pem",
	objectStorage:      "s3://aviary/",
	harnesses: `{"opencode": {"image": "registry.example/opencode@sha256:0123",
		"resources": {"limits": {"cpu": "2", "memory": "4Gi"}}}}`,
	maxConcurrentTurns: "50",
	maxQueuedTurns:     "500",
	maxQueueWait:       "120",
	maxStartup:         "90",
	maxToolOutput:      "65536",
	maxAttachmentBytes: "67108864",
	maxAttachments:     "10",
	maxTurn:            "1800",
}

// set sets the environment to settings, overridden by changed.
func set(t *testing.T, changed map[string]string) {
	for name, value := range settings {
		t.Setenv(name, value)
	}
	for name, value := range changed {
		t.Setenv(name, value)
	}
}

func TestDriverConfig(t *testing.T) {
	t.Run("valid settings", func(t *testing.T) {
		set(t, nil)
		config, err := driverConfig(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		opencode := config.Harnesses["opencode"]
		if len(config.Harnesses) != 1 || opencode.Image != "registry.example/opencode@sha256:0123" ||
			!opencode.Resources.Limits.Cpu().Equal(resource.MustParse("2")) || !opencode.Resources.Limits.Memory().Equal(resource.MustParse("4Gi")) {
			t.Errorf("unexpected harnesses in config %+v", config)
		}
		if config.MaxConcurrentTurns != 50 || config.MaxQueuedTurns != 500 || config.MaxQueueWait != 2*time.Minute ||
			config.MaxStartup != 90*time.Second || config.MaxTurn != 30*time.Minute || config.Idle != 5*time.Minute || config.Retention != 0 {
			t.Errorf("unexpected limits in config %+v", config)
		}
		if hosts := []sandbox.Host{{TLS: true, Name: "*", Port: 443}, {Name: "pypi.example.com", Port: 80}}; !slices.Equal(config.Hosts, hosts) {
			t.Errorf("Hosts = %+v, want %+v", config.Hosts, hosts)
		}
		if config.Objects == nil || config.Localhost != "" {
			t.Errorf("unexpected object storage or local host in config %+v", config)
		}
		if config.MaxAttachmentBytes != 64<<20 || config.MaxAttachments != 10 {
			t.Errorf("unexpected limits of attachments in config %+v", config)
		}
	})

	t.Run("optional limits disabled", func(t *testing.T) {
		set(t, map[string]string{
			maxQueuedTurns: "0", maxQueueWait: "0", maxStartup: "0", maxAttachments: "0",
			sandboxIdle: "0", sandboxRetention: "1", sandboxHosts: "[]", objectStorage: "",
		})
		config, err := driverConfig(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if config.MaxQueuedTurns != 0 || config.MaxQueueWait != 0 || config.MaxStartup != 0 || config.MaxAttachments != 0 ||
			config.Idle != 0 || config.Retention != time.Second || len(config.Hosts) != 0 || config.Objects != nil {
			t.Errorf("unexpected limits in config %+v", config)
		}
	})

	for _, wrong := range []struct{ name, value string }{
		{harnesses, ""}, {harnesses, `{"opencode": `},
		{harnesses, `{"opencode": {"resources": {"limits": {"cpu": "1"}}}}`},
		{harnesses, `{"opencode": {"image": "registry.example/opencode@sha256:0123", "resources": {"limits": {"cpu": "lots"}}}}`},
		{sandboxHosts, ""}, {sandboxHosts, `"https://*"`}, {sandboxHosts, `["*"]`}, {sandboxHosts, `["https://10.0.0.1"]`},
		{sandboxIdle, ""}, {sandboxIdle, "-1"}, {sandboxIdle, "5m"},
		{objectStorage, "aviary"}, {objectStorage, "gs://aviary/"},
		{sandboxRetention, ""}, {sandboxRetention, "-1"},
		{maxConcurrentTurns, ""}, {maxConcurrentTurns, "0"}, {maxConcurrentTurns, "many"},
		{maxQueuedTurns, ""}, {maxQueuedTurns, "-1"},
		{maxQueueWait, ""}, {maxQueueWait, "-1"}, {maxQueueWait, "2m"},
		{maxStartup, ""}, {maxStartup, "-1"},
		{maxAttachmentBytes, ""}, {maxAttachmentBytes, "0"}, {maxAttachmentBytes, "64Mi"},
		{maxAttachments, ""}, {maxAttachments, "-1"},
		{maxTurn, ""}, {maxTurn, "0"},
		{modelsLocalhost, "host.example.com:8000"},
	} {
		t.Run(wrong.name+"="+wrong.value, func(t *testing.T) {
			set(t, map[string]string{wrong.name: wrong.value})
			// The error names the invalid setting.
			if config, err := driverConfig(t.Context()); err == nil || !strings.Contains(err.Error(), wrong.name) {
				t.Errorf("driverConfig = %+v, %v; want an error naming the setting", config, err)
			}
		})
	}
}

func TestSandboxSettings(t *testing.T) {
	set(t, nil)
	harnesses := map[string]sandbox.Harness{"opencode": {Image: "registry.example/opencode@sha256:0123"}}
	made, err := sandboxSettings(harnesses)
	if err != nil {
		t.Fatal(err)
	}
	want := sandbox.Settings{
		API: settings[substrateAPI], Authority: settings[substrateAuthority], TokenFile: settings[substrateToken], CAFile: settings[substrateCA],
		Router: settings[substrateRouter], RouterName: settings[routerName], IdentityFile: settings[substrateIdentity],
		Atespace: "aviary", Pool: map[string]string{"aviary.io/pool": "sandboxes"}, Snapshot: "memory", MaxToolOutputBytes: 65536, Harnesses: harnesses,
	}
	if !reflect.DeepEqual(made, want) {
		t.Errorf("sandboxSettings() = %+v, want %+v", made, want)
	}
	// 0 is a valid tool output limit.
	set(t, map[string]string{maxToolOutput: "0"})
	if made, err := sandboxSettings(harnesses); err != nil || made.MaxToolOutputBytes != 0 {
		t.Errorf("sandboxSettings() = %+v, %v; want no tool output limit", made, err)
	}
	for _, wrong := range []struct{ name, value string }{
		{sandboxAtespace, ""}, {sandboxSnapshot, ""}, {substrateAPI, ""}, {substrateRouter, ""}, {substrateIdentity, ""},
		{sandboxPool, ""}, {sandboxPool, `["sandboxes"]`},
		{maxToolOutput, ""}, {maxToolOutput, "-1"},
	} {
		set(t, map[string]string{wrong.name: wrong.value})
		if made, err := sandboxSettings(harnesses); err == nil || !strings.Contains(err.Error(), wrong.name) {
			t.Errorf("%s=%q: sandboxSettings() = %+v, %v; want an error naming the setting", wrong.name, wrong.value, made, err)
		}
	}
}

// The host that stands in for the local host of a provider's URL is a host
// name, and is optional.
func TestLocalhostOfProviders(t *testing.T) {
	for _, host := range []string{"", "host.example.com", "aviary-host.aviary-system"} {
		t.Setenv(modelsLocalhost, host)
		if got, err := localhostOfProviders(); err != nil || got != host {
			t.Errorf("%q = %q, %v; want it as it is", host, got, err)
		}
	}
	for _, wrong := range []string{
		"localhost",
		"host.example.com:8000",
		"192.0.2.10",
		"2001:db8::254",
		"http://host.example.com",
		"host.example.com/v1",
		"user@host.example.com",
		"a host",
	} {
		t.Setenv(modelsLocalhost, wrong)
		if got, err := localhostOfProviders(); err == nil || !strings.Contains(err.Error(), modelsLocalhost) {
			t.Errorf("%q = %q, %v; want an error that names the setting", wrong, got, err)
		}
	}
}

// allowedSecretStores reads the allowed hosts and the cache duration, and
// serves the kinds of the given plugins.
func TestSecretStoreSettings(t *testing.T) {
	t.Setenv(secretStoreHosts, `["secrets.example.com:8200"]`)
	t.Setenv(secretCacheSeconds, "60")
	plugins := []secret.Plugin{scripted{}}
	stores, err := allowedSecretStores(plugins)
	if err != nil {
		t.Fatal(err)
	}
	defer stores.Close()
	// A store of a known kind is checked against the allowed hosts.
	at := func(address string) json.RawMessage {
		return json.RawMessage(`{"address": "` + address + `", "takes": "a-credential"}`)
	}
	if err := stores.Check(context.Background(), "scripted", at("https://secrets.example.com:8200"), "a-credential"); err != nil {
		t.Errorf("Check of a store at an allowed host: %v", err)
	}
	err = stores.Check(context.Background(), "scripted", at("https://other.example.com:8200"), "a-credential")
	if err == nil || !strings.Contains(err.Error(), "supervisor.secretStores.allowedHosts") {
		t.Errorf("Check of a store at another host = %v, want an error naming the allowed hosts", err)
	}
	if err := stores.Check(context.Background(), "another", at("https://secrets.example.com:8200"), "a-credential"); err == nil || !strings.Contains(err.Error(), "scripted") {
		t.Errorf("Check of an unknown kind = %v, want an error listing the known kinds", err)
	}

	// Values are cached for the configured seconds, and 0 disables the cache.
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
			t.Errorf("cache %s seconds: Read after the value changed = %q, %v", seconds, read, err)
		}
	}

	// Invalid settings are refused.
	for _, wrong := range []string{"", "-1", "soon", "1.5"} {
		t.Setenv(secretCacheSeconds, wrong)
		if stores, err := allowedSecretStores(plugins); err == nil || !strings.Contains(err.Error(), secretCacheSeconds) {
			t.Errorf("cache seconds %q: allowedSecretStores() = %+v, %v; want an error naming the setting", wrong, stores, err)
		}
	}
	t.Setenv(secretCacheSeconds, "60")
	for _, wrong := range []string{"", "secrets.example.com", `["https://secrets.example.com"]`} {
		t.Setenv(secretStoreHosts, wrong)
		if stores, err := allowedSecretStores(plugins); err == nil {
			t.Errorf("hosts %q: allowedSecretStores() = %+v, want an error", wrong, stores)
		}
	}
}
