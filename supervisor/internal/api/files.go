package api

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/sandbox"
)

// The workspace of a session is the directory of its sandbox in which its
// agent works. The runner of the sandbox, which is woken for them, reads and
// writes its files. A path is relative to the workspace, with slashes
// between its elements, and empty for the workspace itself. A session
// without a sandbox has no files: its workspace lists as empty, and it gets a
// sandbox with the first file that is written there.

var (
	errInvalidPath      = &apiError{status: http.StatusBadRequest, Code: "invalid_request", Message: "path must be a relative path below the workspace, such as src/main.go"}
	errIsADirectory     = &apiError{status: http.StatusBadRequest, Code: "is_a_directory", Message: "the path names a directory"}
	errNotADirectory    = &apiError{status: http.StatusBadRequest, Code: "not_a_directory", Message: "the path does not name a directory"}
	errFileTooLarge     = &apiError{status: http.StatusRequestEntityTooLarge, Code: "file_too_large", Message: "the file is larger than a file of a workspace may be"}
	errNoSuchRange      = &apiError{status: http.StatusRequestedRangeNotSatisfiable, Code: "range_not_satisfiable", Message: "the file has no such range"}
	errFileNotPermitted = &apiError{status: http.StatusForbidden, Code: "not_permitted", Message: "the sandbox may not read or write the file"}
	errWorkspaceFull    = &apiError{status: http.StatusInsufficientStorage, Code: "storage_full", Message: "the sandbox has no room for the file"}
	errNoRoom           = &apiError{status: http.StatusServiceUnavailable, Code: "no_room", Message: "no worker has room for the sandbox of the session; retry later", retryAfter: 5}
	errSandboxNotReady  = &apiError{status: http.StatusServiceUnavailable, Code: "sandbox_unavailable", Message: "the sandbox of the session is not available now; retry later", retryAfter: 5}
)

const (
	// Default numbers of entries of a listing of a directory and of a search,
	// and the maximum of both.
	defaultListedFiles = 200
	defaultFoundFiles  = 100
	maxListedFiles     = 1000
	// maxPath is the longest path below a workspace, and maxName the longest
	// name of a file, in bytes.
	maxPath = 4096
	maxName = 255
)

// The Content-Range headers of a part of a file, and of a file that has no
// such part.
var (
	partOfFile   = regexp.MustCompile(`^bytes \d+-\d+/\d+$`)
	noPartOfFile = regexp.MustCompile(`^bytes \*/\d+$`)
)

// fileEntry is an entry of a workspace as the API returns it.
type fileEntry struct {
	Name string `json:"name"`
	// Type is "file", "directory", "other", or "symlink" for a link that
	// names nothing in the workspace. Another link has the type of what it
	// names.
	Type string `json:"type"`
	// Size is the size of a file, in bytes.
	Size       *int64    `json:"size,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
	// Target is the path that a link names.
	Target string `json:"target,omitempty"`
	// Path is where an entry that a search found is in the workspace.
	Path string `json:"path,omitempty"`
}

func fileEntries(listed []sandbox.Entry) []fileEntry {
	entries := make([]fileEntry, len(listed))
	for i, entry := range listed {
		entries[i] = fileEntry{
			Name: entry.Name, Type: entry.Type, Size: entry.Size, ModifiedAt: entry.Modified,
			Target: entry.Target, Path: entry.Path,
		}
	}
	return entries
}

// workspacePath returns the query parameter path: a path below the
// workspace, or empty for the workspace itself.
func workspacePath(r *http.Request) (string, error) {
	path := r.URL.Query().Get("path")
	if path == "" {
		return "", nil
	}
	if len(path) > maxPath || !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
		return "", errInvalidPath
	}
	for name := range strings.SplitSeq(path, "/") {
		if name == "" || name == "." || name == ".." {
			return "", errInvalidPath
		}
	}
	return path, nil
}

// filePath is like workspacePath for a request about a file, whose path is
// not empty.
func filePath(r *http.Request) (string, error) {
	path, err := workspacePath(r)
	if err == nil && path == "" {
		err = invalid("path must name a file")
	}
	return path, err
}

// fileRefusal returns the refusal of the API for an error of a session's
// workspace, or the error as it is.
func fileRefusal(err error) error {
	switch {
	case errors.Is(err, driver.ErrNoSandbox), errors.Is(err, sandbox.ErrNotFound):
		return errNotFound
	case errors.Is(err, sandbox.ErrInvalidPath):
		return errInvalidPath
	case errors.Is(err, sandbox.ErrIsADirectory):
		return errIsADirectory
	case errors.Is(err, sandbox.ErrNotADirectory):
		return errNotADirectory
	case errors.Is(err, sandbox.ErrNotPermitted):
		return errFileNotPermitted
	case errors.Is(err, sandbox.ErrNoRoom):
		return errWorkspaceFull
	case errors.Is(err, sandbox.ErrNoWorker):
		return errNoRoom
	case errors.Is(err, sandbox.ErrCrashed), errors.Is(err, sandbox.ErrOperating):
		return errSandboxNotReady
	}
	return err
}

// asDownload makes the response a download of a file with the name: a
// browser does not open at the address of the API what an agent wrote.
func asDownload(w http.ResponseWriter, name, mediaType string) {
	header := w.Header()
	header.Set("Content-Type", mediaType)
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "sandbox")
}

// pass passes what the runner sends on to the client. The status is already
// sent, so a response that fails is cut off: the client knows then that it
// does not have all of it.
func pass(w http.ResponseWriter, r *http.Request, from io.Reader) {
	if _, err := io.Copy(w, from); err != nil {
		// A client that went away is not an error.
		if r.Context().Err() == nil {
			slog.ErrorContext(r.Context(), "send file of workspace", "path", r.URL.Path, "error", err)
		}
		panic(http.ErrAbortHandler)
	}
}

// listFiles lists a directory of the workspace: its directories first, and
// each kind by name. The query parameter after is the next of an earlier
// listing of the directory, which goes on from there.
func (a *api) listFiles(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	path, err := workspacePath(r)
	if err != nil {
		return err
	}
	limit, err := queryNumber(r, "limit", defaultListedFiles, maxListedFiles)
	if err != nil {
		return err
	}
	after := r.URL.Query().Get("after")
	if len(after) > maxPath || !utf8.ValidString(after) {
		return invalid("after must be the next of a listing of the directory")
	}
	listing := struct {
		Entries []fileEntry `json:"entries"`
		Next    string      `json:"next,omitempty"`
	}{Entries: []fileEntry{}}
	workspace, err := a.driver.Workspace(r.Context(), sessionID, false)
	switch {
	case errors.Is(err, driver.ErrNoSandbox) && path == "":
		respond(w, http.StatusOK, listing)
		return nil
	case err != nil:
		return fileRefusal(err)
	}
	defer workspace.Close()
	listed, next, err := workspace.List(r.Context(), path, after, limit)
	switch {
	case errors.Is(err, sandbox.ErrInvalidPath):
		// The path is one below the workspace.
		return invalid("after must be the next of a listing of the directory")
	case err != nil:
		return fileRefusal(err)
	}
	listing.Entries, listing.Next = fileEntries(listed), next
	respond(w, http.StatusOK, listing)
	return nil
}

// searchFiles finds the entries of the workspace whose names have the query,
// whatever the case of their letters.
func (a *api) searchFiles(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	query := r.URL.Query().Get("query")
	if query == "" || len(query) > maxName || !utf8.ValidString(query) {
		return invalid("query must be 1 to " + strconv.Itoa(maxName) + " bytes of a name")
	}
	limit, err := queryNumber(r, "limit", defaultFoundFiles, maxListedFiles)
	if err != nil {
		return err
	}
	finding := struct {
		Entries   []fileEntry `json:"entries"`
		Truncated bool        `json:"truncated"`
	}{Entries: []fileEntry{}}
	workspace, err := a.driver.Workspace(r.Context(), sessionID, false)
	switch {
	case errors.Is(err, driver.ErrNoSandbox):
		respond(w, http.StatusOK, finding)
		return nil
	case err != nil:
		return fileRefusal(err)
	}
	defer workspace.Close()
	found, truncated, err := workspace.Search(r.Context(), query, limit)
	if err != nil {
		return fileRefusal(err)
	}
	finding.Entries, finding.Truncated = fileEntries(found), truncated
	respond(w, http.StatusOK, finding)
	return nil
}

// readFile sends a file of the workspace, or the range of it that the
// request asks for, as a download.
func (a *api) readFile(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	path, err := filePath(r)
	if err != nil {
		return err
	}
	workspace, err := a.driver.Workspace(r.Context(), sessionID, false)
	if err != nil {
		return fileRefusal(err)
	}
	defer workspace.Close()
	content, err := workspace.Read(r.Context(), path, r.Header.Get("Range"))
	if err != nil {
		return fileRefusal(err)
	}
	defer content.Body.Close()
	// What the runner says of the range is passed on only in the form that
	// such a header has.
	header := w.Header()
	status := http.StatusOK
	switch {
	case content.Unsatisfiable:
		if noPartOfFile.MatchString(content.Range) {
			header.Set("Content-Range", content.Range)
		}
		return errNoSuchRange
	case content.Range != "":
		if !partOfFile.MatchString(content.Range) {
			return fmt.Errorf("runner sent a part of a file as %q", content.Range)
		}
		header.Set("Content-Range", content.Range)
		status = http.StatusPartialContent
	}
	asDownload(w, path[strings.LastIndex(path, "/")+1:], "application/octet-stream")
	header.Set("Accept-Ranges", "bytes")
	if content.Size >= 0 {
		header.Set("Content-Length", strconv.FormatInt(content.Size, 10))
	}
	w.WriteHeader(status)
	pass(w, r, content.Body)
	return nil
}

// writeFile writes the body of the request as a file of the workspace, in
// place of the one that is there, with the directories above it that are
// missing.
func (a *api) writeFile(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	path, err := filePath(r)
	if err != nil {
		return err
	}
	// A file that is refused wakes no sandbox.
	switch size := r.ContentLength; {
	case size < 0:
		return errLengthRequired
	case size > a.driver.MaxFileBytes():
		return errFileTooLarge
	}
	workspace, err := a.driver.Workspace(r.Context(), sessionID, true)
	if err != nil {
		return fileRefusal(err)
	}
	defer workspace.Close()
	written, created, err := workspace.Write(r.Context(), path, r.Body, r.ContentLength)
	if err != nil {
		return fileRefusal(err)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	respond(w, status, fileEntries([]sandbox.Entry{written})[0])
	return nil
}

// archiveFiles sends a directory of the workspace as a ZIP file, which the
// runner writes while it is sent. The paths in it begin with the name of the
// directory, or with "workspace".
func (a *api) archiveFiles(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	path, err := workspacePath(r)
	if err != nil {
		return err
	}
	name := "workspace"
	if path != "" {
		name = path[strings.LastIndex(path, "/")+1:]
	}
	workspace, err := a.driver.Workspace(r.Context(), sessionID, false)
	if err != nil {
		return fileRefusal(err)
	}
	defer workspace.Close()
	archive, err := workspace.Archive(r.Context(), path)
	if err != nil {
		return fileRefusal(err)
	}
	defer archive.Close()
	asDownload(w, name+".zip", "application/zip")
	w.WriteHeader(http.StatusOK)
	pass(w, r, archive)
	return nil
}
