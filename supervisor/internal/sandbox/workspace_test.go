package sandbox

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/internal/sandbox/runnertest"
)

// working starts a fake runner whose workspace has the files, and returns
// its home and a client of it.
func working(t *testing.T, files map[string]string) (*runnertest.Home, *Runner) {
	t.Helper()
	home := &runnertest.Home{}
	for path, content := range files {
		home.Put(path, content)
	}
	server := httptest.NewServer(runnertest.New(t).Runner("a-sandbox", home))
	t.Cleanup(server.Close)
	return home, &Runner{address: server.URL, target: "aviary/a-sandbox", http: server.Client(), quiet: time.Minute}
}

func names(entries []Entry) []string {
	listed := make([]string, len(entries))
	for i, entry := range entries {
		listed[i] = entry.Name
	}
	return listed
}

func TestADirectoryOfAWorkspaceIsListed(t *testing.T) {
	_, runner := working(t, map[string]string{
		"README.md": "# storefront", "b.txt": "bb", "src/main.go": "package main", "src/util/a.go": "a",
	})
	listed, next, err := runner.List(ctx, "", "", 10)
	if err != nil || next != "" || !slices.Equal(names(listed), []string{"src", "b.txt", "README.md"}) {
		t.Fatalf("List = %v, next %q, %v; want the directory first and the files by name", names(listed), next, err)
	}
	if directory := listed[0]; directory.Type != "directory" || directory.Size != nil {
		t.Errorf("the entry of a directory = %+v", directory)
	}
	if file := listed[1]; file.Type != "file" || file.Size == nil || *file.Size != 2 || !file.Modified.Equal(runnertest.Modified) {
		t.Errorf("the entry of a file = %+v, want its size 2 and when it was changed", file)
	}
	// A listing of a part of the entries tells where the next goes on.
	first, next, err := runner.List(ctx, "", "", 2)
	if err != nil || next == "" || !slices.Equal(names(first), []string{"src", "b.txt"}) {
		t.Fatalf("List of two = %v, next %q, %v", names(first), next, err)
	}
	rest, next, err := runner.List(ctx, "", next, 2)
	if err != nil || next != "" || !slices.Equal(names(rest), []string{"README.md"}) {
		t.Errorf("List of the rest = %v, next %q, %v", names(rest), next, err)
	}
	below, _, err := runner.List(ctx, "src", "", 10)
	if err != nil || !slices.Equal(names(below), []string{"util", "main.go"}) {
		t.Errorf("List of a directory below = %v, %v", names(below), err)
	}
	for path, want := range map[string]error{"missing": ErrNotFound, "b.txt": ErrNotADirectory, "../home": ErrInvalidPath} {
		if _, _, err := runner.List(ctx, path, "", 10); !errors.Is(err, want) {
			t.Errorf("List(%q): %v, want %v", path, err, want)
		}
	}
}

func TestAFileOfAWorkspaceIsReadAndWritten(t *testing.T) {
	home, runner := working(t, map[string]string{"README.md": "# storefront", "src/main.go": "package main"})
	read := func(path, ranged string) (*Content, string) {
		t.Helper()
		content, err := runner.Read(ctx, path, ranged)
		if err != nil {
			t.Fatalf("Read(%q, %q): %v", path, ranged, err)
		}
		defer content.Body.Close()
		body, err := io.ReadAll(content.Body)
		if err != nil {
			t.Fatal(err)
		}
		return content, string(body)
	}
	if content, body := read("README.md", ""); body != "# storefront" || content.Size != 12 || content.Range != "" {
		t.Errorf("Read = %q, %+v", body, content)
	}
	if content, body := read("README.md", "bytes=2-5"); body != "stor" || content.Size != 4 || content.Range != "bytes 2-5/12" {
		t.Errorf("Read of a range = %q, %+v", body, content)
	}
	if content, _ := read("README.md", "bytes=99-"); !content.Unsatisfiable || content.Range != "bytes */12" {
		t.Errorf("Read of a range that the file does not have = %+v", content)
	}
	for path, want := range map[string]error{"src": ErrIsADirectory, "missing.txt": ErrNotFound, "/etc/passwd": ErrInvalidPath} {
		if _, err := runner.Read(ctx, path, ""); !errors.Is(err, want) {
			t.Errorf("Read(%q): %v, want %v", path, err, want)
		}
	}

	written, created, err := runner.Write(ctx, "docs/보고서 (2).md", strings.NewReader("hello"), 5)
	if err != nil || !created || written.Name != "보고서 (2).md" || written.Type != "file" || *written.Size != 5 {
		t.Fatalf("Write = %+v, created %t, %v", written, created, err)
	}
	// Written again, a file replaces the one that is there.
	if _, created, err := runner.Write(ctx, "docs/보고서 (2).md", strings.NewReader("done"), 4); err != nil || created {
		t.Errorf("Write again: created %t, %v", created, err)
	}
	if got := home.Workspace()["docs/보고서 (2).md"]; got != "done" {
		t.Errorf("the file is %q, want it replaced", got)
	}
	for path, want := range map[string]error{"src": ErrIsADirectory, "README.md/more": ErrNotADirectory, "": ErrInvalidPath} {
		if _, _, err := runner.Write(ctx, path, strings.NewReader("no"), 2); !errors.Is(err, want) {
			t.Errorf("Write(%q): %v, want %v", path, err, want)
		}
	}
	home.Fill()
	if _, _, err := runner.Write(ctx, "large.bin", strings.NewReader("a"), 1); !errors.Is(err, ErrNoRoom) {
		t.Errorf("Write into a full home: %v, want ErrNoRoom", err)
	}
}

func TestAWorkspaceIsSearchedAndArchived(t *testing.T) {
	_, runner := working(t, map[string]string{
		"src/checkout/total.ts": "t", "src/subtotal.ts": "s", "docs/Totals.md": "d", "README.md": "r",
	})
	found, truncated, err := runner.Search(ctx, "TOTAL", 10)
	var paths []string
	for _, entry := range found {
		paths = append(paths, entry.Path)
	}
	if err != nil || truncated || !slices.Equal(paths, []string{"docs/Totals.md", "src/checkout/total.ts", "src/subtotal.ts"}) {
		t.Errorf("Search = %v, truncated %t, %v", paths, truncated, err)
	}
	if some, truncated, err := runner.Search(ctx, "total", 1); err != nil || !truncated || len(some) != 1 {
		t.Errorf("Search for one = %+v, truncated %t, %v", some, truncated, err)
	}

	archive, err := runner.Archive(ctx, "src")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	packed, err := io.ReadAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	unpacked, err := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, file := range unpacked.File {
		files = append(files, file.Name)
	}
	if !slices.Equal(files, []string{"src/checkout/total.ts", "src/subtotal.ts"}) {
		t.Errorf("the archive has %v", files)
	}
	if _, err := runner.Archive(ctx, "README.md"); !errors.Is(err, ErrNotADirectory) {
		t.Errorf("Archive of a file: %v, want ErrNotADirectory", err)
	}
}

func TestWhatARunnerSaysOfItsWorkspaceIsNotTakenAtItsWord(t *testing.T) {
	said := ""
	var target string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target = r.Header.Get("ate-target-actor")
		io.WriteString(w, said)
	}))
	defer server.Close()
	runner := &Runner{address: server.URL, target: "aviary/a-sandbox", http: server.Client(), quiet: time.Minute}
	for _, said = range []string{
		`{"entries": [{"name": "../etc", "type": "file", "modified": 0}]}`,
		`{"entries": [{"name": "", "type": "file", "modified": 0}]}`,
		`{"entries": [{"name": "a", "type": "socket", "modified": 0}]}`,
		`{"entries": [{"name": "a", "type": "file", "size": -1, "modified": 0}]}`,
		`not JSON`,
	} {
		if listed, _, err := runner.List(ctx, "", "", 10); err == nil {
			t.Errorf("List of %s = %+v, want an error", said, listed)
		}
	}
	// The router is told which sandbox the request is for.
	if target != "aviary/a-sandbox" {
		t.Errorf("the request named the sandbox %q", target)
	}
}
