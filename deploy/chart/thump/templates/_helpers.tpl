{{- if gt (int .Values.replicas) 1 }}
{{- fail "thump beats are single-replica by invariant (in-memory ledger); see values.yaml" }}
{{- end }}
{{/*
One release, one pod, one chart — no chart-per-beat ceremony (W0-2). These
helpers just keep the labels identical across every template that needs them.
*/}}

{{- define "thump.labels" -}}
app.kubernetes.io/name: thump
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "thump.selectorLabels" -}}
app.kubernetes.io/name: thump
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* beat-scoped selector: pass the beat name as the context's `.component` */}}
{{- define "thump.beatSelectorLabels" -}}
{{ include "thump.selectorLabels" . }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}
