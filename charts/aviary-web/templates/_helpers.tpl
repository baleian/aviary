{{- define "aviary-web.fullname" -}}
aviary-web
{{- end -}}

{{- define "aviary-web.labels" -}}
aviary/role: web
app.kubernetes.io/name: aviary-web
app.kubernetes.io/managed-by: helm
{{- end -}}

{{- define "aviary-web.selectorLabels" -}}
aviary/role: web
{{- end -}}
