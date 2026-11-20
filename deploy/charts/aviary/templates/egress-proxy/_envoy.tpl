{{/*
Envoy configuration shared by the llm and forward listeners.
*/}}

{{/*
Access log: a JSON line per request on standard output. The argument is the
name of the listener.
*/}}
{{- define "aviary.egress-proxy.access-log" -}}
access_log:
  - name: envoy.access_loggers.stdout
    filter:
      not_health_check_filter: {}
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.access_loggers.stream.v3.StdoutAccessLog
      log_format:
        json_format:
          time: "%START_TIME%"
          route: {{ . }}
          turn: "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:turn)%"
          session: "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:session)%"
          agent: "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:agent)%"
          project: "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:project)%"
          # Who sent the message of the turn: the Aviary user ID and the
          # subject at the identity provider. Empty without a provider.
          user: "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:user)%"
          subject: "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:subject)%"
          {{- if eq . "llm" }}
          provider: "%DYNAMIC_METADATA(envoy.filters.http.ext_authz:provider)%"
          {{- end }}
          method: "%REQ(:METHOD)%"
          host: "%REQ(:AUTHORITY)%"
          path: "%PATH(NQ)%"
          status: "%RESPONSE_CODE%"
          details: "%RESPONSE_CODE_DETAILS%"
          request_bytes: "%BYTES_RECEIVED%"
          response_bytes: "%BYTES_SENT%"
          duration_ms: "%DURATION%"
{{- end }}

{{/*
ext_authz filter that asks the Supervisor which turn a request belongs to,
and refuses the request if there is none. The argument is true if the answer
also routes the request.
*/}}
{{- define "aviary.egress-proxy.ext-authz" -}}
- name: envoy.filters.http.ext_authz
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz
    transport_api_version: V3
    grpc_service:
      envoy_grpc: {cluster_name: supervisor}
      timeout: 5s
    # Otherwise a failed check is answered 403, as if the token were wrong.
    status_on_error: {code: ServiceUnavailable}
    {{- if . }}
    # Routes the request again with the headers that the Supervisor set.
    clear_route_cache: true
    {{- else }}
    # The answer only identifies the turn. Refusing header changes keeps the
    # key of a model provider from ever being added to a request to another
    # host.
    decoder_header_mutation_rules:
      disallow_all: true
      disallow_is_error: true
    {{- end }}
{{- end }}

{{/*
RBAC permissions that match the listed hosts in the given header. A host
without a port matches ports 443 and 80.
*/}}
{{- define "aviary.egress-proxy.hosts" -}}
{{- $header := .header }}
{{- range .hosts }}
- header: {name: {{ $header | quote }}, string_match: {exact: {{ . | quote }}, ignore_case: true}}
{{- if not (regexMatch ":[0-9]+$" .) }}
- header: {name: {{ $header | quote }}, string_match: {exact: {{ printf "%s:443" . | quote }}, ignore_case: true}}
- header: {name: {{ $header | quote }}, string_match: {exact: {{ printf "%s:80" . | quote }}, ignore_case: true}}
{{- end }}
{{- end }}
{{- end }}

{{/*
Router filter. Envoy adds no headers of its own to requests or responses.
*/}}
{{- define "aviary.egress-proxy.router" -}}
- name: envoy.filters.http.router
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
    suppress_envoy_headers: true
{{- end }}
