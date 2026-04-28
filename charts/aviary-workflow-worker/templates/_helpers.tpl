{{- define "aviary-workflow-worker.fullname" -}}
aviary-workflow-worker
{{- end -}}

{{- define "aviary-workflow-worker.labels" -}}
aviary/role: workflow-worker
app.kubernetes.io/name: aviary-workflow-worker
app.kubernetes.io/managed-by: helm
{{- end -}}

{{- define "aviary-workflow-worker.selectorLabels" -}}
aviary/role: workflow-worker
{{- end -}}
