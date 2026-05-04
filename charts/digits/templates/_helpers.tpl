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
