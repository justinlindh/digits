package web

import (
	"html/template"
	"testing"
)

// TestTemplatePagesParse parses every page template the same way NewHandler
// does. It runs without the integration build tag (no DB needed) so template
// syntax errors are caught by the fast unit suite, not only at server startup.
func TestTemplatePagesParse(t *testing.T) {
	funcMap := TemplateFuncs()
	pages := []string{
		"dashboard.html",
		"phones.html",
		"phone-detail.html",
		"calls.html",
		"settings.html",
		"onboard.html",
		"links.html",
	}
	for _, page := range pages {
		t.Run(page, func(t *testing.T) {
			_, err := template.New("").Funcs(funcMap).ParseFS(templateFS,
				"templates/_partials.html",
				"templates/_changelog.html",
				"templates/layout-v2.html",
				"templates/layout-dialup.html",
				"templates/layout-answering-machine.html",
				"templates/"+page,
			)
			if err != nil {
				t.Fatalf("parse %s: %v", page, err)
			}
		})
	}
}
