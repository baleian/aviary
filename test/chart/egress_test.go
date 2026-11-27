package chart

import (
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

// walk calls visit with every map in the YAML value, outermost first.
func walk(value any, visit func(map[string]any)) {
	switch value := value.(type) {
	case map[string]any:
		visit(value)
		for _, inner := range value {
			walk(inner, visit)
		}
	case []any:
		for _, inner := range value {
			walk(inner, visit)
		}
	}
}

// The egress gateway asks the Supervisors whom each connection of a sandbox
// is for, before the router of its CONNECT leg, and fails a connection that
// they do not answer for. Each JSON line of its access log says it, and the
// log writes every request of the legs that read them.
func TestEgressGatewayAttributesConnections(t *testing.T) {
	objects := chart(t, "default")
	var envoy map[string]any
	if err := yaml.Unmarshal([]byte(at(find(objects, "ConfigMap", "atenet-egress"), "data", "envoy.yaml").(string)), &envoy); err != nil {
		t.Fatal(err)
	}
	attributes := []string{"turn", "session", "agent", "project", "user", "subject"}

	var connect []any
	walk(envoy, func(object map[string]any) {
		if object["stat_prefix"] == "egress_connect" {
			connect, _ = object["http_filters"].([]any)
		}
	})
	var names []string
	for _, filter := range connect {
		name, _ := at(filter, "name").(string)
		if prefix, _ := at(filter, "typed_config", "stat_prefix").(string); prefix != "" {
			name += "/" + prefix
		}
		names = append(names, name)
	}
	identity := slices.Index(names, "envoy.filters.http.ext_proc/egress_identity")
	asks := slices.Index(names, "envoy.filters.http.ext_proc/aviary_attribution")
	if identity < 0 || asks < identity || len(names) < asks+3 || names[asks+1] != "envoy.filters.http.set_filter_state" ||
		names[len(names)-1] != "envoy.filters.http.router" {
		t.Fatalf("filters of the CONNECT leg = %v, want the Supervisors asked after the identity of the actor, before the router", names)
	}
	processor := at(connect[asks], "typed_config")
	if at(processor, "grpc_service", "envoy_grpc", "cluster_name") != "aviary_supervisor" || at(processor, "failure_mode_allow") != false ||
		at(processor, "grpc_service", "retry_policy", "num_retries") != 3 {
		t.Errorf("ext_proc that asks the Supervisors = %v", processor)
	}
	var kept []string
	for _, state := range at(connect[asks+1], "typed_config", "on_request_headers").([]any) {
		key, _ := at(state, "object_key").(string)
		kept = append(kept, key)
	}
	if want := []string{"dev.aviary.turn", "dev.aviary.session", "dev.aviary.agent", "dev.aviary.project", "dev.aviary.user", "dev.aviary.subject"}; !slices.Equal(kept, want) {
		t.Errorf("filter state of the answer = %v, want %v", kept, want)
	}

	var supervisors map[string]any
	walk(envoy, func(object map[string]any) {
		if object["name"] == "aviary_supervisor" && object["type"] == "STRICT_DNS" {
			supervisors = object
		}
	})
	endpoint := at(supervisors, "load_assignment", "endpoints", 0, "lb_endpoints", 0, "endpoint", "address", "socket_address")
	if at(endpoint, "address") != "aviary-supervisor.aviary-system.svc" || at(endpoint, "port_value") != 50051 ||
		at(supervisors, "transport_socket", "typed_config", "sni") != "aviary-supervisor.aviary-system.svc" {
		t.Errorf("cluster of the Supervisors = %v", supervisors)
	}

	lines, samples := 0, 0
	walk(envoy, func(object map[string]any) {
		if format, ok := object["json_format"].(map[string]any); ok && format["actor"] != nil {
			lines++
			for _, name := range attributes {
				if format[name] != "%FILTER_STATE(dev.aviary."+name+":PLAIN)%" {
					t.Errorf("access log of leg %v has %s %v", format["leg"], name, format[name])
				}
			}
		}
		if sampled, ok := object["percent_sampled"].(map[string]any); ok {
			samples++
			if sampled["numerator"] != 100 {
				t.Errorf("access log samples %v of the requests, want all", sampled)
			}
		}
	})
	if lines != 4 || samples != 2 {
		t.Errorf("%d access logs with the actor and %d sampled ones, want 4 and 2", lines, samples)
	}

	annotations, _ := at(find(objects, "Deployment", "atenet-egress"), "spec", "template", "metadata", "annotations").(map[string]any)
	if checksum, _ := annotations["checksum/attribution"].(string); len(checksum) != 64 {
		t.Errorf("annotations of the egress gateway = %v, want a checksum of what attributes its connections", annotations)
	}
}
