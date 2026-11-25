// Package docs tests that the reference of the documentation names what the
// Supervisor has, and nothing else: the requests of the API and the metrics.
// `make test-docs` runs it in a checkout, where the pages and the code are.
package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	reference  = "../../docs/src/content/docs/reference"
	supervisor = "../../supervisor/internal"
)

// found returns what the pattern captures in the files of a directory whose
// paths end as given, sorted and each once. Tests are left out.
func found(t *testing.T, directory, ending string, pattern *regexp.Regexp) []string {
	t.Helper()
	var all []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ending) || strings.HasSuffix(path, "_test.go") {
			return err
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range pattern.FindAllSubmatch(text, -1) {
			all = append(all, string(match[1]))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(all)
	return slices.Compact(all)
}

// same fails the test for each name that the code or the pages have alone.
func same(t *testing.T, kind string, code, pages []string) {
	t.Helper()
	if len(code) == 0 {
		t.Fatalf("found no %s in the code", kind)
	}
	for _, name := range code {
		if !slices.Contains(pages, name) {
			t.Errorf("the reference does not have the %s %s", kind, name)
		}
	}
	for _, name := range pages {
		if !slices.Contains(code, name) {
			t.Errorf("the reference has the %s %s, and the Supervisor does not", kind, name)
		}
	}
}

func TestRequests(t *testing.T) {
	const request = `((?:GET|POST|PUT|PATCH|DELETE) /v1/[a-z{}/-]+)`
	// The code and the pages name the parameters of a path differently.
	parameter := regexp.MustCompile(`\{[a-z]+\}`)
	unnamed := func(requests []string) []string {
		for i, each := range requests {
			requests[i] = parameter.ReplaceAllString(each, "{}")
		}
		slices.Sort(requests)
		return slices.Compact(requests)
	}
	registered := found(t, filepath.Join(supervisor, "api"), "/api.go", regexp.MustCompile(`\.Handle\("`+request+`"`))
	documented := found(t, filepath.Join(reference, "api"), ".md", regexp.MustCompile(request))
	same(t, "request", unnamed(registered), unnamed(documented))
}

func TestMetrics(t *testing.T) {
	const metric = `(aviary_[a-z_]+)`
	served := found(t, filepath.Join(supervisor, "metrics"), ".go", regexp.MustCompile(`"`+metric+`"`))
	documented := found(t, reference, "/metrics.md", regexp.MustCompile(metric))
	same(t, "metric", served, documented)
}
