{{/*
What the egress gateway adds to the configuration of its release, so that
each line of its access log says whom what a sandbox sent is for: the turn,
session, agent and project, and the user who sent the turn's message. The
gateway asks the Supervisors when a sandbox opens a connection, for the lines
of a connection that it passes on unread, and again for each request that it
reads, for the line of the request. A connection or a request that they do
not answer for fails. helm.awk puts these templates into the configuration.
*/}}

{{/* The fields of an answer. */}}
{{- define "substrate.attributionNames" -}}
turn session agent project user username subject
{{- end }}

{{/*
The filter that asks, with the actor that the gateway authenticated. It sends
no header, since on a request that the gateway reads it runs after the
request's secrets have been put into them.
*/}}
{{- define "substrate.attributionProcessor" -}}
- name: envoy.filters.http.ext_proc
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
    stat_prefix: aviary_attribution
    grpc_service:
      envoy_grpc:
        cluster_name: aviary_supervisor
      timeout: 2s
      retry_policy:
        retry_on: connect-failure,refused-stream,reset,unavailable
        num_retries: 3
        retry_back_off:
          base_interval: 0.1s
          max_interval: 1s
    failure_mode_allow: false
    message_timeout: 2s
    request_attributes:
    - filter_state['dev.ate.actor.identity']
    forward_rules:
      disallowed_headers:
        patterns:
        - safe_regex:
            regex: ".*"
    processing_mode:
      request_header_mode: SEND
      response_header_mode: SKIP
      request_body_mode: NONE
      response_body_mode: NONE
      request_trailer_mode: SKIP
      response_trailer_mode: SKIP
    mutation_rules:
      disallow_all: true
      disallow_is_error: true
    metadata_options:
      receiving_namespaces:
        untyped:
        - aviary
{{- end }}

{{/*
The filters of the CONNECT leg, before its router, which keep the answer for
the connection that the leg opens.
*/}}
{{- define "substrate.attributionFilters" -}}
{{ include "substrate.attributionProcessor" . }}
- name: envoy.filters.http.set_filter_state
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.set_filter_state.v3.Config
    on_request_headers:
    {{- range splitList " " (include "substrate.attributionNames" .) }}
    - object_key: dev.aviary.{{ . }}
      factory_key: envoy.hashable_string
      skip_if_empty: true
      shared_with_upstream: ONCE
      format_string:
        text_format_source:
          inline_string: "%DYNAMIC_METADATA(aviary:{{ . }})%"
        omit_empty_values: true
    {{- end }}
{{- end }}

{{/* The fields of a JSON line of the access log of a connection, after its actor. */}}
{{- define "substrate.attributionFields" -}}
{{- range splitList " " (include "substrate.attributionNames" .) }}
{{ . }}: "%FILTER_STATE(dev.aviary.{{ . }}:PLAIN)%"
{{- end }}
{{- end }}

{{/* The fields of a JSON line of the access log of a request, after its actor. */}}
{{- define "substrate.attributionRequestFields" -}}
{{- range splitList " " (include "substrate.attributionNames" .) }}
{{ . }}: "%DYNAMIC_METADATA(aviary:{{ . }})%"
{{- end }}
{{- end }}

{{/* The cluster of the Supervisors, which the filter asks. */}}
{{- define "substrate.attributionCluster" -}}
- name: aviary_supervisor
  type: STRICT_DNS
  lb_policy: ROUND_ROBIN
  connect_timeout: 1s
  typed_extension_protocol_options:
    envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
      "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
      explicit_http_config:
        http2_protocol_options: {}
  transport_socket:
    name: envoy.transport_sockets.tls
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
      sni: {{ include "substrate.supervisor" . }}
      common_tls_context:
        tls_certificate_sds_secret_configs:
        - name: podidentity_client_cert
          sds_config:
            resource_api_version: V3
            path_config_source:
              path: /etc/envoy/sds-podidentity-cert.yaml
        combined_validation_context:
          default_validation_context:
            match_typed_subject_alt_names:
            - san_type: DNS
              matcher:
                exact: {{ include "substrate.supervisor" . }}
          validation_context_sds_secret_config:
            name: servicedns_validation_context
            sds_config:
              resource_api_version: V3
              path_config_source:
                path: /etc/envoy/sds-servicedns-validation.yaml
  load_assignment:
    cluster_name: aviary_supervisor
    endpoints:
    - lb_endpoints:
      - endpoint:
          address:
            socket_address:
              address: {{ include "substrate.supervisor" . }}
              port_value: {{ include "substrate.credentialProviderPort" . }}
{{- end }}

{{/*
The share of the requests of the MITM and cleartext legs that the access log
writes in full, which the release samples at 1%.
*/}}
{{- define "substrate.requestLogPercent" -}}
100
{{- end }}

{{/* Restarts the gateway when what these templates add changes. */}}
{{- define "substrate.attributionChecksum" -}}
{{- print (include "substrate.attributionFilters" .) (include "substrate.attributionFields" .) (include "substrate.attributionRequestFields" .) (include "substrate.attributionCluster" .) (include "substrate.requestLogPercent" .) | sha256sum -}}
{{- end }}
