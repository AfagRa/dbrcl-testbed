{{- define "dbrcl.image" -}}
{{ .root.Values.image.registry }}/{{ .name }}:{{ .root.Values.image.tag }}
{{- end -}}

{{/* Pod labels: app (+ track) + Datadog unified service tags.
     Args: root, app, service, version, track (optional) */}}
{{- define "dbrcl.podLabels" -}}
app: {{ .app }}
{{- if .track }}
track: {{ .track }}
{{- end }}
tags.datadoghq.com/env: {{ .root.Values.ddEnv }}
tags.datadoghq.com/service: {{ .service }}
tags.datadoghq.com/version: {{ .version | quote }}
{{- end -}}

{{/* Annotations for the chart-default kubernetes-pods scrape job. Arg: port */}}
{{- define "dbrcl.scrape" -}}
prometheus.io/scrape: "true"
prometheus.io/port: {{ . | quote }}
prometheus.io/path: /metrics
{{- end -}}

{{- define "dbrcl.securityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
{{- end -}}
