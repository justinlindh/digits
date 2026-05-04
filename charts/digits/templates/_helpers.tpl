{{/*
Expand the name of the chart.
*/}}
{{- define "digits.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "digits.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "digits.labels" -}}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/part-of: {{ include "digits.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Signald labels.
*/}}
{{- define "digits.signald.labels" -}}
app.kubernetes.io/name: signald
{{ include "digits.labels" . }}
{{- end }}

{{/*
Signald selector labels.
*/}}
{{- define "digits.signald.selectorLabels" -}}
app.kubernetes.io/name: signald
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Admind labels.
*/}}
{{- define "digits.admind.labels" -}}
app.kubernetes.io/name: admind
{{ include "digits.labels" . }}
{{- end }}

{{/*
Admind selector labels.
*/}}
{{- define "digits.admind.selectorLabels" -}}
app.kubernetes.io/name: admind
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Signald image.
*/}}
{{- define "digits.signald.image" -}}
{{ .Values.signald.image.repository }}:{{ .Values.signald.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- end }}

{{/*
Admind image.
*/}}
{{- define "digits.admind.image" -}}
{{ .Values.admind.image.repository }}:{{ .Values.admind.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- end }}

{{/*
Observability env vars (OTEL + Pyroscope). Pass a dict with "serviceName" and "ctx".
Usage: include "digits.observability.env" (dict "serviceName" "signald" "ctx" .)
*/}}
{{- define "digits.observability.env" -}}
{{- if .ctx.Values.observability.otelEndpoint }}
- name: OTEL_SERVICE_NAME
  value: {{ .serviceName | quote }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ .ctx.Values.observability.otelEndpoint | quote }}
- name: OTEL_EXPORTER_OTLP_PROTOCOL
  value: {{ .ctx.Values.observability.otelProtocol | quote }}
- name: OTEL_EXPORTER_OTLP_INSECURE
  value: {{ .ctx.Values.observability.otelInsecure | quote }}
{{- if .ctx.Values.observability.otelResourceAttributes }}
- name: OTEL_RESOURCE_ATTRIBUTES
  value: {{ .ctx.Values.observability.otelResourceAttributes | quote }}
{{- end }}
{{- end }}
{{- if .ctx.Values.observability.pyroscopeEndpoint }}
- name: PYROSCOPE_SERVER_ADDRESS
  value: {{ .ctx.Values.observability.pyroscopeEndpoint | quote }}
{{- end }}
{{- end }}
