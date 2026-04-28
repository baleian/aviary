{{- define "aviary-api.fullname" -}}
aviary-api
{{- end -}}

{{- define "aviary-api.labels" -}}
aviary/role: api
app.kubernetes.io/name: aviary-api
app.kubernetes.io/managed-by: helm
{{- end -}}

{{- define "aviary-api.selectorLabels" -}}
aviary/role: api
{{- end -}}
