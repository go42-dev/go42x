# Project: {{ .project.name }}

<context>
  <language>{{ .project.language }}</language>
  {{ if .project.tags -}}
  <tags>
    {{- range .project.tags }}
    - {{ . }}
    {{- end }}
  </tags>
  {{- end }}
  {{ if .project.metadata -}}
  <metadata>
    {{- range $key, $value := .project.metadata }}
    <{{ $key }}>{{ $value }}</{{ $key }}>
    {{- end }}
  </metadata>
  {{- end }}
</context>

{{ .project.description }}

## Instructions

{{ .chunks }}

## Operational Modes

User can specify the operational mode by saying "Switch to [mode name] mode".

You are allowed to operate only in one of the following modes at any given time:

{{ .modes }}

## Workflows

User can specify the workflow by saying "Use [workflow name] workflow".

You are allowed to execute only one of the following workflows at any given time:

{{ .workflows }}
