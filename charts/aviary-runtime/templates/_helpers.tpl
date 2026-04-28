{{- define "aviary-runtime.name" -}}
{{ .Values.name | default "default" }}
{{- end -}}

{{- define "aviary-runtime.fullname" -}}
aviary-env-{{ include "aviary-runtime.name" . }}
{{- end -}}

{{- define "aviary-runtime.labels" -}}
aviary/role: agent-runtime
aviary/environment: {{ include "aviary-runtime.name" . }}
{{- end -}}

{{- define "aviary-runtime.selectorLabels" -}}
aviary/role: agent-runtime
aviary/environment: {{ include "aviary-runtime.name" . }}
{{- end -}}
