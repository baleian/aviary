{{- define "aviary-admin.fullname" -}}
aviary-admin
{{- end -}}

{{- define "aviary-admin.labels" -}}
aviary/role: admin
app.kubernetes.io/name: aviary-admin
app.kubernetes.io/managed-by: helm
{{- end -}}

{{- define "aviary-admin.selectorLabels" -}}
aviary/role: admin
{{- end -}}
