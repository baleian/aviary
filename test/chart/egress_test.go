package chart

import (
	"maps"
	"slices"
	"strings"
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

// filters returns the HTTP filters of the HTTP connection manager with the
// stat prefix, each as its name and the stat prefix of its own, if any.
func filters(envoy map[string]any, prefix string) ([]any, []string) {
	var chain []any
	walk(envoy, func(object map[string]any) {
		if object["stat_prefix"] == prefix {
			chain, _ = object["http_filters"].([]any)
		}
	})
	var names []string
	for _, filter := range chain {
		name, _ := at(filter, "name").(string)
		if prefix, _ := at(filter, "typed_config", "stat_prefix").(string); prefix != "" {
			name += "/" + prefix
		}
		names = append(names, name)
	}
	return chain, names
}

// The egress gateway asks the Supervisors whom a sandbox's connection is for,
// before the router of its CONNECT leg, and again for each request of the legs
// that read requests, after their policy, and fails a connection or a request
// that they do not answer for. Each JSON line of its access log says it, and
// the log writes every request of the legs that read them.
func TestEgressGatewayAttributesConnectionsAndRequests(t *testing.T) {
	objects := chart(t, "default")
	var envoy map[string]any
	if err := yaml.Unmarshal([]byte(at(find(objects, "ConfigMap", "atenet-egress"), "data", "envoy.yaml").(string)), &envoy); err != nil {
		t.Fatal(err)
	}
	attributes := []string{"turn", "session", "agent", "project", "user", "username", "subject"}
	asked := func(processor any) {
		t.Helper()
		if at(processor, "grpc_service", "envoy_grpc", "cluster_name") != "aviary_supervisor" || at(processor, "failure_mode_allow") != false ||
			at(processor, "grpc_service", "retry_policy", "num_retries") != 3 ||
			at(processor, "forward_rules", "disallowed_headers", "patterns", 0, "safe_regex", "regex") != ".*" {
			t.Errorf("ext_proc that asks the Supervisors = %v", processor)
		}
	}

	connect, names := filters(envoy, "egress_connect")
	identity := slices.Index(names, "envoy.filters.http.ext_proc/egress_identity")
	asks := slices.Index(names, "envoy.filters.http.ext_proc/aviary_attribution")
	if identity < 0 || asks < identity || len(names) < asks+3 || names[asks+1] != "envoy.filters.http.set_filter_state" ||
		names[len(names)-1] != "envoy.filters.http.router" {
		t.Fatalf("filters of the CONNECT leg = %v, want the Supervisors asked after the identity of the actor, before the router", names)
	}
	asked(at(connect[asks], "typed_config"))
	var kept []string
	for _, state := range at(connect[asks+1], "typed_config", "on_request_headers").([]any) {
		key, _ := at(state, "object_key").(string)
		kept = append(kept, strings.TrimPrefix(key, "dev.aviary."))
	}
	if !slices.Equal(kept, attributes) {
		t.Errorf("filter state of the answer = %v, want dev.aviary. and %v", kept, attributes)
	}
	for _, leg := range []string{"mitm_http", "mitm_cleartext"} {
		chain, names := filters(envoy, leg)
		policy := slices.IndexFunc(names, func(name string) bool { return strings.HasPrefix(name, "envoy.filters.http.ext_proc/egress_policy") })
		asks := slices.Index(names, "envoy.filters.http.ext_proc/aviary_attribution")
		if policy < 0 || asks < policy || names[len(names)-1] != "envoy.filters.http.router" {
			t.Fatalf("filters of %s = %v, want the Supervisors asked after the policy", leg, names)
		}
		asked(at(chain[asks], "typed_config"))
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

	legs, samples := map[string]bool{}, 0
	walk(envoy, func(object map[string]any) {
		if format, ok := object["json_format"].(map[string]any); ok && format["actor"] != nil {
			leg, _ := format["leg"].(string)
			legs[leg] = true
			for _, name := range attributes {
				want := "%FILTER_STATE(dev.aviary." + name + ":PLAIN)%"
				if leg == "mitm" || leg == "cleartext" {
					want = "%DYNAMIC_METADATA(aviary:" + name + ")%"
				}
				if format[name] != want {
					t.Errorf("access log of leg %s has %s %v, want %s", leg, name, format[name], want)
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
	if want := map[string]bool{"clienthello": true, "mitm": true, "cleartext": true, "passthrough": true}; !maps.Equal(legs, want) || samples != 2 {
		t.Errorf("access logs with the actor of the legs %v and %d sampled ones, want %v and 2", legs, samples, want)
	}

	annotations, _ := at(find(objects, "Deployment", "atenet-egress"), "spec", "template", "metadata", "annotations").(map[string]any)
	if checksum, _ := annotations["checksum/attribution"].(string); len(checksum) != 64 {
		t.Errorf("annotations of the egress gateway = %v, want a checksum of what attributes its connections", annotations)
	}
}
