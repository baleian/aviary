{{/*
The templates that templates/upstream.yaml includes in the manifests of the
release, where kustomize/kustomization.yaml leaves AVIARY_VALUE_<name> or
AVIARY_INCLUDE_<name>.
*/}}

{{/* An image of the release, by its name: (list $ "atelet"). */}}
{{- define "substrate.image" -}}
{{- $root := index . 0 -}}
{{- $name := index . 1 -}}
{{- $digest := required (printf "substrate.images.digests.%s is required" $name) (index $root.Values.images.digests $name) -}}
{{- printf "%s/%s:%s@%s" $root.Values.images.registry $name $root.Values.images.tag $digest -}}
{{- end }}

{{/* The host of the Supervisors, which are the credential provider aviary.io. */}}
{{- define "substrate.supervisor" -}}
{{- printf "aviary-supervisor.%s.svc" .Release.Namespace -}}
{{- end }}

{{- define "substrate.credentialProviderAddress" -}}
{{- printf "%s:50051" (include "substrate.supervisor" .) -}}
{{- end }}

{{- define "substrate.credentialProviderServerName" -}}
{{- include "substrate.supervisor" . -}}
{{- end }}

{{/* The callers that own every atespace: kubectl ate in ate-system, and the Supervisors. */}}
{{- define "substrate.bootstrapOwners" -}}
{{- printf "system:serviceaccount:ate-system:ate-client,system:serviceaccount:%s:aviary-supervisor" .Release.Namespace -}}
{{- end }}

{{/* The callers that the router lets in: kubectl ate, and the Supervisors. */}}
{{- define "substrate.ingressAllowedSpiffeIds" -}}
{{- printf "spiffe://cluster.local/ns/ate-system/sa/atenet-client,spiffe://cluster.local/ns/%s/sa/aviary-supervisor" .Release.Namespace -}}
{{- end }}

{{/* Restart the API server and atelet when what they read at start changes. */}}
{{- define "substrate.configChecksum" -}}
{{- print (include (print .Template.BasePath "/config.yaml") .) (include (print .Template.BasePath "/object-storage.yaml") .) | sha256sum -}}
{{- end }}

{{- define "substrate.objectStorageChecksum" -}}
{{- include (print .Template.BasePath "/object-storage.yaml") . | sha256sum -}}
{{- end }}

{{- define "substrate.databaseEnv" -}}
{{- $secret := .Values.database.secret | default "substrate-database" }}
- name: ATE_API_POSTGRES_OWNER_CONNECTION_STRING
  valueFrom:
    secretKeyRef:
      name: {{ $secret }}
      key: owner
- name: ATE_API_POSTGRES_READ_WRITE_CONNECTION_STRING
  valueFrom:
    secretKeyRef:
      name: {{ $secret }}
      key: readWrite
{{- end }}

{{/* The location of the snapshots: that of the values, or the store of the chart. */}}
{{- define "substrate.location" -}}
{{- .Values.objectStorage.location | default "s3://aviary/" -}}
{{- end }}

{{/* The Secret of the access key of the object storage, if any. */}}
{{- define "substrate.objectStorageSecret" -}}
{{- if not .Values.objectStorage.location -}}
substrate-store
{{- else if .Values.objectStorage.secret -}}
{{- .Values.objectStorage.secret -}}
{{- else if .Values.objectStorage.accessKeyId -}}
substrate-object-storage
{{- end -}}
{{- end }}

{{- define "substrate.objectStorageEnv" -}}
{{- $endpoint := .Values.objectStorage.endpoint }}
{{- $pathStyle := .Values.objectStorage.pathStyle }}
{{- if not .Values.objectStorage.location }}
{{- $endpoint = printf "http://substrate-store.%s.svc:7070" .Release.Namespace }}
{{- $pathStyle = true }}
{{- end }}
- name: AWS_REGION
  value: {{ .Values.objectStorage.region | quote }}
{{- with $endpoint }}
- name: AWS_ENDPOINT_URL
  value: {{ . | quote }}
{{- end }}
- name: AWS_S3_USE_PATH_STYLE
  value: {{ $pathStyle | quote }}
{{- with include "substrate.objectStorageSecret" . }}
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

{{- define "substrate.serviceAccountAnnotations" -}}
{{- with .Values.serviceAccountAnnotations }}
{{- toYaml . }}
{{- end }}
{{- end }}

{{- define "substrate.ateletArgs" -}}
- --snapshot-upload-delay={{ .Values.snapshots.uploadDelay }}
- --snapshot-detach-delay={{ .Values.snapshots.detachDelay }}
{{- if include "substrate.imageCredentialProvider" . }}
- --image-credential-provider-config=/run/image-credential-provider/config.yaml
- --image-credential-provider-bin-dir=/run/image-credential-provider/bin
{{- end }}
{{- with .Values.atelet.localhostRegistry }}
- --localhost-registry-replacement={{ . }}
{{- end }}
{{- end }}

{{- define "substrate.imageCredentialProvider" -}}
{{- with .Values.atelet.imageCredentialProvider -}}
{{- if and .binDir .config }}true{{ end -}}
{{- end -}}
{{- end }}

{{- define "substrate.ateletCredentialProviderMounts" -}}
{{- if include "substrate.imageCredentialProvider" . }}
- name: image-credential-provider-bin
  mountPath: /run/image-credential-provider/bin
  readOnly: true
- name: image-credential-provider-config
  mountPath: /run/image-credential-provider/config.yaml
  readOnly: true
{{- end }}
{{- end }}

{{- define "substrate.ateletCredentialProviderVolumes" -}}
{{- if include "substrate.imageCredentialProvider" . }}
- name: image-credential-provider-bin
  hostPath:
    path: {{ .Values.atelet.imageCredentialProvider.binDir }}
    type: Directory
- name: image-credential-provider-config
  hostPath:
    path: {{ .Values.atelet.imageCredentialProvider.config }}
    type: File
{{- end }}
{{- end }}
