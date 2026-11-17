{{/*
The parts of Envoy's configuration that its two listeners share.
*/}}

{{/*
The port of the model endpoint: the one in llm.url, or else the usual one of
its scheme.
*/}}
{{- define "aviary.llm.port" -}}
{{- $url := urlParse .Values.llm.url -}}
{{- with regexFind ":[0-9]+$" $url.host -}}
{{- trimPrefix ":" . -}}
{{- else -}}
{{- ternary 443 80 (eq $url.scheme "https") -}}
{{- end -}}
{{- end }}

{{/*
The record of a request, a line of JSON on standard output. The argument is
the name of the listener.
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
The filter that asks the Supervisor which turn a request comes from, and
refuses the request if the Supervisor names none. The argument tells
whether the answer of the Supervisor also says where the request goes.
*/}}
{{- define "aviary.egress-proxy.ext-authz" -}}
- name: envoy.filters.http.ext_authz
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz
    transport_api_version: V3
    grpc_service:
      envoy_grpc: {cluster_name: supervisor}
      timeout: 5s
    # Without it, a request that the proxy could not ask a Supervisor about
    # is answered as forbidden, as if its token were wrong.
    status_on_error: {code: ServiceUnavailable}
    {{- if . }}
    # The route of the request is chosen again, with what the Supervisor
    # answered.
    clear_route_cache: true
    {{- else }}
    # The answer tells whose request it is and nothing more. What the
    # Supervisor adds to a model request, the key of a model endpoint, is
    # never to go to another host: Envoy takes no change of the request from
    # the answer, and refuses a request whose answer holds one.
    decoder_header_mutation_rules:
      disallow_all: true
      disallow_is_error: true
    {{- end }}
{{- end }}

{{/*
The rules that match a request to one of the listed hosts. The arguments are
the list and the header that holds the host of a request. A host name stands
for the host on the ports of HTTPS and HTTP.
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
The last filter, which passes the request on. Envoy adds no headers of its
own to it.
*/}}
{{- define "aviary.egress-proxy.router" -}}
- name: envoy.filters.http.router
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
    suppress_envoy_headers: true
{{- end }}
