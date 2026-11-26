{{/* The labels of the workers of the sandboxes, by which the templates of
the sandboxes select them, as JSON. */}}
{{- define "aviary.pool" -}}
{{ dict "aviary.io/pool" .Release.Namespace | toJson }}
{{- end }}
