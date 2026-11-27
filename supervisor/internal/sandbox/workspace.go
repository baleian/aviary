package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The files of the workspace of a sandbox, which its runner serves. A path is
// relative to the workspace, with slashes between its elements, and empty for
// the workspace itself.

var (
	// ErrInvalidPath means that a path is not one below the workspace.
	ErrInvalidPath = errors.New("the path is not one below the workspace")
	// ErrIsADirectory means that a path names a directory, not a file.
	ErrIsADirectory = errors.New("the path names a directory")
	// ErrNotADirectory means that a path, or a part of it, names something
	// that is not a directory.
	ErrNotADirectory = errors.New("the path does not name a directory")
	// ErrNotPermitted means that the runner may not read or write what a path
	// names.
	ErrNotPermitted = errors.New("the runner may not access the file")
)

// reasons are the errors of the codes with which a runner refuses a request
// about its workspace.
var reasons = map[string]error{
	"invalid_path":    ErrInvalidPath,
	"not_found":       ErrNotFound,
	"is_a_directory":  ErrIsADirectory,
	"not_a_directory": ErrNotADirectory,
	"not_permitted":   ErrNotPermitted,
	"no_room":         ErrNoRoom,
}

// listingLimit is the largest answer about a workspace that is read, in
// bytes, apart from the files themselves.
const listingLimit = 16 << 20

// Entry is an entry of a directory of a workspace.
type Entry struct {
	Name string
	// Type is "file", "directory" or "other". A link has the type of what it
	// names, or "symlink" if that is missing or outside the workspace.
	Type string
	// Size is the size of a file, in bytes.
	Size *int64
	// Modified is when the entry was last changed.
	Modified time.Time
	// Target is the path that a link names.
	Target string
	// Path is where the entry is in the workspace, for one that a search
	// found.
	Path string
}

// reported is an entry as a runner reports it.
type reported struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size *int64 `json:"size"`
	// Modified is in milliseconds since the epoch.
	Modified int64  `json:"modified"`
	Target   string `json:"target"`
	Path     string `json:"path"`
}

// entry returns the entry that the runner reported, or an error if it is not
// one: the runner is not trusted.
func (t reported) entry() (Entry, error) {
	switch t.Type {
	case "file", "directory", "symlink", "other":
	default:
		return Entry{}, fmt.Errorf("runner reported an entry of type %q", t.Type)
	}
	if t.Name == "" || strings.ContainsAny(t.Name, "/\x00") || (t.Size != nil && *t.Size < 0) {
		return Entry{}, fmt.Errorf("runner reported an invalid entry %q", t.Name)
	}
	return Entry{Name: t.Name, Type: t.Type, Size: t.Size, Modified: time.UnixMilli(t.Modified).UTC(), Target: t.Target, Path: t.Path}, nil
}

func entries(all []reported) ([]Entry, error) {
	listed := make([]Entry, len(all))
	for i, each := range all {
		var err error
		if listed[i], err = each.entry(); err != nil {
			return nil, err
		}
	}
	return listed, nil
}

// List returns up to limit entries of the directory at the path: its
// directories first, and each kind by name. after is the next that an
// earlier listing of the directory returned, or empty, and next is where
// this one ends if the directory has more.
func (r *Runner) List(ctx context.Context, path, after string, limit int) (listed []Entry, next string, err error) {
	query := url.Values{"path": {path}, "limit": {strconv.Itoa(limit)}}
	if after != "" {
		query.Set("after", after)
	}
	var answer struct {
		Entries []reported `json:"entries"`
		Next    string     `json:"next"`
	}
	if err := r.ask(ctx, "/workspace?"+query.Encode(), &answer); err != nil {
		return nil, "", err
	}
	listed, err = entries(answer.Entries)
	return listed, answer.Next, err
}

// Search returns up to limit entries of the workspace whose names have the
// words of the query, each with its path, and whether the workspace has, or
// may have, more of them.
func (r *Runner) Search(ctx context.Context, query string, limit int) (found []Entry, truncated bool, err error) {
	asked := url.Values{"query": {query}, "limit": {strconv.Itoa(limit)}}
	var answer struct {
		Entries   []reported `json:"entries"`
		Truncated bool       `json:"truncated"`
	}
	if err := r.ask(ctx, "/workspace/search?"+asked.Encode(), &answer); err != nil {
		return nil, false, err
	}
	found, err = entries(answer.Entries)
	return found, answer.Truncated, err
}

// ask asks the runner about its workspace, and reads its answer into answer.
func (r *Runner) ask(ctx context.Context, asked string, answer any) error {
	ctx, cancel := context.WithTimeout(ctx, told)
	defer cancel()
	response, err := r.workspace(ctx, http.MethodGet, asked, nil, -1, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, listingLimit)).Decode(answer); err != nil {
		return fmt.Errorf("runner: read the answer to GET %s: %w", asked, err)
	}
	return nil
}

// Content is a file of a workspace as its runner sends it, or a range of it.
type Content struct {
	// Body is to be closed.
	Body io.ReadCloser
	// Size is the number of bytes of the body, or -1 if the runner did not
	// tell.
	Size int64
	// Range tells which bytes of the file the body is, as a Content-Range
	// header does, if the request asked for a range. Unsatisfiable is set if
	// the file has no such range: the body is then empty.
	Range         string
	Unsatisfiable bool
}

// Read opens the file at the path. ranged is the Range header of a request
// for a part of the file, or empty.
func (r *Runner) Read(ctx context.Context, path, ranged string) (*Content, error) {
	header := http.Header{}
	if ranged != "" {
		header.Set("Range", ranged)
	}
	asked := "/workspace/content?" + url.Values{"path": {path}}.Encode()
	response, err := r.workspace(ctx, http.MethodGet, asked, nil, -1, header)
	if err != nil {
		return nil, err
	}
	content := &Content{Body: response.Body, Size: response.ContentLength}
	if response.StatusCode != http.StatusOK {
		content.Range = response.Header.Get("Content-Range")
		content.Unsatisfiable = response.StatusCode == http.StatusRequestedRangeNotSatisfiable
	}
	return content, nil
}

// Write writes file, of size bytes, as the file at the path, in place of the
// one that is there, with the directories above it that are missing. It
// returns the entry of the file, and whether there was none before. It
// returns ErrNoRoom if the home has no room for the file.
func (r *Runner) Write(ctx context.Context, path string, file io.Reader, size int64) (written Entry, created bool, err error) {
	asked := "/workspace/content?" + url.Values{"path": {path}}.Encode()
	response, err := r.workspace(ctx, http.MethodPut, asked, file, size, nil)
	if err != nil {
		return Entry{}, false, err
	}
	defer response.Body.Close()
	var answer reported
	if err := json.NewDecoder(io.LimitReader(response.Body, listingLimit)).Decode(&answer); err != nil {
		return Entry{}, false, fmt.Errorf("runner: read the answer to PUT %s: %w", asked, err)
	}
	written, err = answer.entry()
	return written, response.StatusCode == http.StatusCreated, err
}

// Archive opens the directory at the path as a ZIP file, which the runner
// writes while it is read. The caller closes it. Reading fails if the runner
// does not send the whole of it.
func (r *Runner) Archive(ctx context.Context, path string) (io.ReadCloser, error) {
	asked := "/workspace/archive?" + url.Values{"path": {path}}.Encode()
	response, err := r.workspace(ctx, http.MethodGet, asked, nil, -1, nil)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// workspace sends a request about the workspace, and returns the response if
// the runner does not refuse it. The error of a refusal is that of its code.
func (r *Runner) workspace(ctx context.Context, method, asked string, body io.Reader, size int64, header http.Header) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, r.address+asked, body)
	if err != nil {
		return nil, err
	}
	r.name(request)
	for name, values := range header {
		request.Header[name] = values
	}
	if body != nil {
		request.ContentLength = size
	}
	response, err := r.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
		return response, nil
	}
	defer response.Body.Close()
	said, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	var refusal struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(said, &refusal) == nil {
		if reason, known := reasons[refusal.Code]; known {
			return nil, fmt.Errorf("runner: %s %s: %w", method, asked, reason)
		}
	}
	return nil, &Refusal{Who: "runner", Asked: method + " " + asked, Status: response.StatusCode, Said: strings.TrimSpace(string(said))}
}
