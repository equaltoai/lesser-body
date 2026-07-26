package agentcontent

import (
	"bytes"
	"errors"
	"text/template"
)

const FiveBodiesMarkdownTemplate = `# Agent soul

## Identity

{{.Identity.Summary}}
{{- with .Identity.Notes}}

### Identity notes
{{range .}}
- {{.}}
{{- end}}
{{- end}}

## Philosophy

{{.Philosophy.Summary}}
{{- with .Philosophy.Notes}}

### Philosophy notes
{{range .}}
- {{.}}
{{- end}}
{{- end}}

## Discipline

{{.Discipline.Summary}}
{{- with .Discipline.Notes}}

### Discipline notes
{{range .}}
- {{.}}
{{- end}}
{{- end}}

## Boundaries

{{.Boundaries.Summary}}
{{- with .Boundaries.Notes}}

### Boundaries notes
{{range .}}
- {{.}}
{{- end}}
{{- end}}

## Soul

{{.Soul.Summary}}
{{- with .Soul.Notes}}

### Soul notes
{{range .}}
- {{.}}
{{- end}}
{{- end}}

### Refusals
{{range $index, $refusal := .Soul.Refusals}}
{{addOne $index}}. **Bypass:** {{$refusal.Bypass}}
   **Invariant:** {{$refusal.Invariant}}
   **Closest safe path:** {{$refusal.ClosestSafePath}}
{{end}}`

var fiveBodiesMarkdownTemplate = template.Must(template.New("panonomous-five-bodies.v2").
	Funcs(template.FuncMap{"addOne": func(value int) int { return value + 1 }}).
	Parse(FiveBodiesMarkdownTemplate))

// RenderFiveBodiesMarkdown applies the one canonical Body-owned deterministic
// Markdown template used for Ptah declaration application and Ba rendering.
func RenderFiveBodiesMarkdown(fiveBodies *FiveBodies) (string, error) {
	if fiveBodies == nil {
		return "", errors.New("five_bodies is required")
	}
	var rendered bytes.Buffer
	if err := fiveBodiesMarkdownTemplate.Execute(&rendered, fiveBodies); err != nil {
		return "", err
	}
	return rendered.String(), nil
}

// RenderSoulDocument selects typed rendering when a v2 five-body overlay is
// present and otherwise preserves the canonical Markdown body byte-for-byte.
func RenderSoulDocument(document *SoulDocument) (string, error) {
	if document == nil {
		return "", errors.New("agent soul document is required")
	}
	if document.Structure != nil && document.Structure.FiveBodies != nil {
		return RenderFiveBodiesMarkdown(document.Structure.FiveBodies)
	}
	return document.Body, nil
}
