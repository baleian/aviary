{{- define "aviary-supervisor.fullname" -}}
aviary-supervisor
{{- end -}}

{{- define "aviary-supervisor.labels" -}}
aviary/role: supervisor
app.kubernetes.io/name: aviary-supervisor
app.kubernetes.io/managed-by: helm
{{- end -}}

{{- define "aviary-supervisor.selectorLabels" -}}
aviary/role: supervisor
{{- end -}}
