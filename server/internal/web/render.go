package web

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// escapeHTMLRenderer overrides the default raw-HTML renderers so that raw
// HTML in markdown input is HTML-escaped rather than replaced with comments.
// This gives safe, visible output for untrusted release note content.
type escapeHTMLRenderer struct{}

func (e *escapeHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(gast.KindRawHTML, e.renderRawHTML)
	reg.Register(gast.KindHTMLBlock, e.renderHTMLBlock)
}

func (e *escapeHTMLRenderer) renderRawHTML(
	w util.BufWriter, source []byte, node gast.Node, entering bool,
) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkSkipChildren, nil
	}
	n := node.(*gast.RawHTML)
	for i := range n.Segments.Len() {
		seg := n.Segments.At(i)
		_, _ = w.WriteString(template.HTMLEscapeString(string(seg.Value(source))))
	}
	return gast.WalkSkipChildren, nil
}

func (e *escapeHTMLRenderer) renderHTMLBlock(
	w util.BufWriter, source []byte, node gast.Node, entering bool,
) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkContinue, nil
	}
	n := node.(*gast.HTMLBlock)
	for i := range n.Lines().Len() {
		line := n.Lines().At(i)
		_, _ = w.WriteString(template.HTMLEscapeString(string(line.Value(source))))
	}
	if n.HasClosure() {
		closure := n.ClosureLine
		_, _ = w.WriteString(template.HTMLEscapeString(string(closure.Value(source))))
	}
	return gast.WalkContinue, nil
}

// notesMD is the markdown renderer for release notes. Raw HTML is escaped
// rather than rendered, so untrusted input from GitHub release bodies is
// safe to pipe through it.
var notesMD = goldmark.New(
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
		renderer.WithNodeRenderers(
			util.Prioritized(&escapeHTMLRenderer{}, 1),
		),
	),
)

// renderNotes converts release-note markdown to safe HTML for inclusion
// in a Go template. Returns an empty string for empty input.
func renderNotes(s string) template.HTML {
	if s == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := notesMD.Convert([]byte(s), &buf); err != nil {
		// Fall back to plain-escaped text on render failure.
		return template.HTML(template.HTMLEscapeString(s))
	}
	return template.HTML(buf.String())
}
