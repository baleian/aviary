package main

import (
	"strings"
	"testing"
	"time"
)

// settings are the variables of a Supervisor as the chart sets them.
var settings = map[string]string{
	gatewayURL:         "http://aviary-supervisor.aviary-system:9090",
	runnerImage:        "registry.example/runner:1",
	llmBaseURL:         "http://aviary-egress-proxy.aviary-system:8080/v1",
	llmModel:           "a-model",
	proxyURL:           "http://aviary-egress-proxy.aviary-system:3128",
	harnesses:          `{"opencode": {"image": "registry.example/opencode:1", "resources": {"limits": {"memory": "4Gi"}}}}`,
	maxConcurrentTurns: "50",
	maxQueuedTurns:     "500",
	maxQueueWait:       "120",
	maxReconnect:       "60",
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
		opencode := config.Harnesses["opencode"]
		if config.GatewayURL != settings[gatewayURL] || config.RunnerImage != settings[runnerImage] ||
			config.LLMBaseURL != settings[llmBaseURL] || config.LLMModel != "a-model" || config.ProxyURL != settings[proxyURL] ||
			len(config.Harnesses) != 1 || opencode.Image != "registry.example/opencode:1" ||
			opencode.Resources.Limits.Memory().String() != "4Gi" || len(opencode.Resources.Limits) != 1 {
			t.Errorf("the turns are run with %+v", config)
		}
		if config.MaxConcurrentTurns != 50 || config.MaxQueuedTurns != 500 || config.MaxQueueWait != 2*time.Minute ||
			config.MaxReconnect != time.Minute || config.MaxStartup != 90*time.Second ||
			config.MaxToolOutputBytes != 65536 || config.MaxTurn != 30*time.Minute {
			t.Errorf("the limits are %+v", config)
		}
	})

	t.Run("without the limits that there need not be", func(t *testing.T) {
		set(t, map[string]string{
			llmModel: "", maxQueuedTurns: "0", maxQueueWait: "0", maxReconnect: "0", maxStartup: "0", maxToolOutput: "0",
		})
		config, err := driverConfig()
		if err != nil {
			t.Fatal(err)
		}
		if config.LLMModel != "" || config.MaxQueuedTurns != 0 || config.MaxQueueWait != 0 || config.MaxReconnect != 0 ||
			config.MaxStartup != 0 || config.MaxToolOutputBytes != 0 {
			t.Errorf("the limits are %+v", config)
		}
	})

	for _, wrong := range []struct{ name, value string }{
		{gatewayURL, ""}, {runnerImage, ""}, {llmBaseURL, ""}, {proxyURL, ""},
		{harnesses, ""}, {harnesses, `{"opencode": `},
		{maxConcurrentTurns, ""}, {maxConcurrentTurns, "0"}, {maxConcurrentTurns, "many"},
		{maxQueuedTurns, ""}, {maxQueuedTurns, "-1"},
		{maxQueueWait, ""}, {maxQueueWait, "-1"}, {maxQueueWait, "2m"},
		{maxReconnect, ""}, {maxReconnect, "-1"},
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
