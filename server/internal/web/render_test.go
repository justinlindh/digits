package web

import (
	"strings"
	"testing"
)

func TestRenderNotes(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantContain []string
		wantAbsent  []string
	}{
		{
			"plain paragraphs",
			"Hello world.\n\nSecond paragraph.",
			[]string{"<p>Hello world.</p>", "<p>Second paragraph.</p>"},
			nil,
		},
		{
			"emphasis",
			"This is *important* and **critical**.",
			[]string{"<em>important</em>", "<strong>critical</strong>"},
			nil,
		},
		{
			"raw script is escaped",
			"hello <script>alert('xss')</script> world",
			[]string{"&lt;script&gt;"},
			[]string{"<script>"},
		},
		{
			"raw html tag is escaped",
			`<div onclick="boom()">evil</div>`,
			[]string{"&lt;div"},
			[]string{`onclick="boom()"`},
		},
		{
			"empty",
			"",
			nil,
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(renderNotes(tt.in))
			for _, w := range tt.wantContain {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q:\n%s", w, got)
				}
			}
			for _, w := range tt.wantAbsent {
				if strings.Contains(got, w) {
					t.Errorf("output contained forbidden %q:\n%s", w, got)
				}
			}
		})
	}
}
