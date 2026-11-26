{{/* The labels of the workers of the sandboxes, by which the templates of
the sandboxes select them, as JSON. */}}
{{- define "aviary.pool" -}}
{{ dict "aviary.io/pool" .Release.Namespace | toJson }}
{{- end }}

{{/* The object storage of the files that messages carry, which is the one
that Substrate keeps its snapshots in. Without Substrate in the chart,
messages carry files only if substrate.objectStorage names a location. */}}
{{- define "aviary.objectStorageEnv" -}}
{{- if .Values.substrate.install -}}
{{- $substrate := dict "Values" .Values.substrate "Release" .Release -}}
- name: AVIARY_OBJECT_STORAGE
  value: {{ include "substrate.location" $substrate | quote }}
{{ include "substrate.objectStorageEnv" $substrate }}
{{- else -}}
{{- $storage := .Values.substrate.objectStorage | default dict -}}
{{- with $storage.location -}}
- name: AVIARY_OBJECT_STORAGE
  value: {{ . | quote }}
- name: AWS_REGION
  value: {{ $storage.region | default "us-east-1" | quote }}
{{- with $storage.endpoint }}
- name: AWS_ENDPOINT_URL
  value: {{ . | quote }}
{{- end }}
- name: AWS_S3_USE_PATH_STYLE
  value: {{ $storage.pathStyle | default false | quote }}
{{- with $storage.secret }}
- name: AWS_ACCESS_KEY_ID
  valueFrom:
    secretKeyRef:
      name: {{ . }}
      key: accessKeyId
- name: AWS_SECRET_ACCESS_KEY
  valueFrom:
    secretKeyRef:
      name: {{ . }}
      key: secretAccessKey
{{- end }}
{{- end }}
{{- end }}
{{- end }}
