package runnertest

import (
	"archive/zip"
	"cmp"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The workspace of a home: its files by their paths. A directory is there as
// long as a file is below it.

// Modified is when a fake runner says that every entry was last changed.
var Modified = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// Put puts a file into the workspace of the home, as an agent does.
func (h *Home) Put(path, content string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.workspace == nil {
		h.workspace = map[string]string{}
	}
	h.workspace[path] = content
}

// Workspace returns the files of the workspace of the home, by their paths.
func (h *Home) Workspace() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return maps.Clone(h.workspace)
}

// Link puts a link into the workspace of the home, at the top of it. The
// target is the path of a file in the workspace, or anything else for a link
// that names nothing there.
func (h *Home) Link(name, target string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.links == nil {
		h.links = map[string]string{}
	}
	h.links[name] = target
}

func (h *Home) linked() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return maps.Clone(h.links)
}

// Deny keeps the runner from the file at the path, as the permissions of a
// file do: it may neither read it nor write it.
func (h *Home) Deny(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.denied == nil {
		h.denied = map[string]bool{}
	}
	h.denied[path] = true
}

func (h *Home) denies(path string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.denied[path]
}

// Cut has the runner break off the archives of the workspace that it sends,
// as one whose sandbox goes away meanwhile.
func (h *Home) Cut() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cut = true
}

func (h *Home) cuts() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cut
}

// entry is an entry as a runner tells it.
type entry struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Size     *int   `json:"size,omitempty"`
	Modified int64  `json:"modified"`
	Target   string `json:"target,omitempty"`
	Path     string `json:"path,omitempty"`
}

func refuse(w http.ResponseWriter, status int, code string) {
	answer(w, status, map[string]string{"code": code})
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// inside reports whether the path is one below the workspace.
func inside(path string) bool {
	return path == "" || !slices.ContainsFunc(strings.Split(path, "/"), func(name string) bool {
		return name == "" || name == "." || name == ".."
	})
}

// kindOf returns "file" or "directory" for what the files have at the path,
// or "" for nothing. The caller holds the lock of the home.
func kindOf(files map[string]string, path string) string {
	if _, file := files[path]; file {
		return "file"
	}
	for other := range files {
		if path == "" || strings.HasPrefix(other, path+"/") {
			return "directory"
		}
	}
	if path == "" {
		return "directory"
	}
	return ""
}

// described returns the entry of the name at the path.
func described(files map[string]string, path, name string) entry {
	told := entry{Name: name, Type: kindOf(files, path), Modified: Modified.UnixMilli()}
	if content, file := files[path]; file {
		size := len(content)
		told.Size = &size
	}
	return told
}

// at returns the workspace and the path of the request, if what is there is
// of the kind. Otherwise it refuses the request as a runner does.
func (rn *Runner) at(w http.ResponseWriter, r *http.Request, kind string) (map[string]string, string, bool) {
	path := r.URL.Query().Get("path")
	files := rn.home.Workspace()
	switch found := kindOf(files, path); {
	case !inside(path):
		refuse(w, http.StatusBadRequest, "invalid_path")
	case found == "":
		refuse(w, http.StatusNotFound, "not_found")
	case rn.home.denies(path):
		refuse(w, http.StatusForbidden, "not_permitted")
	case found != kind && kind == "file":
		refuse(w, http.StatusConflict, "is_a_directory")
	case found != kind:
		refuse(w, http.StatusConflict, "not_a_directory")
	default:
		return files, path, true
	}
	return nil, "", false
}

// place orders the entries of a directory: the directories first, and each
// kind by name.
func place(e entry) string {
	kind := "f/"
	if e.Type == "directory" {
		kind = "d/"
	}
	return kind + strings.ToLower(e.Name) + "/" + e.Name
}

func (rn *Runner) list(w http.ResponseWriter, r *http.Request) {
	files, path, found := rn.at(w, r, "directory")
	if !found {
		return
	}
	names := map[string]bool{}
	for other := range files {
		if below, is := strings.CutPrefix(other, path+"/"); is || path == "" {
			if path == "" {
				below = other
			}
			name, _, _ := strings.Cut(below, "/")
			names[name] = true
		}
	}
	var entries []entry
	for name := range names {
		entries = append(entries, described(files, strings.TrimPrefix(path+"/"+name, "/"), name))
	}
	// A link is listed as the file that it names, or as a link that names
	// nothing.
	if path == "" {
		for name, target := range rn.home.linked() {
			named := described(files, target, name)
			if named.Type != "file" {
				named = entry{Name: name, Type: "symlink", Modified: Modified.UnixMilli()}
			}
			named.Target = target
			entries = append(entries, named)
		}
	}
	slices.SortFunc(entries, func(a, b entry) int { return cmp.Compare(place(a), place(b)) })
	if after := r.URL.Query().Get("after"); after != "" {
		kind, name, _ := strings.Cut(after, "/")
		if kind != "d" && kind != "f" {
			refuse(w, http.StatusBadRequest, "invalid_path")
			return
		}
		from := kind + "/" + strings.ToLower(name) + "/" + name
		entries = slices.DeleteFunc(entries, func(e entry) bool { return place(e) <= from })
	}
	listing := struct {
		Entries []entry `json:"entries"`
		Next    string  `json:"next,omitempty"`
	}{Entries: entries}
	if limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); limit > 0 && len(entries) > limit {
		last := entries[limit-1]
		listing.Entries, listing.Next = entries[:limit], place(last)[:2]+last.Name
	}
	answer(w, http.StatusOK, listing)
}

func (rn *Runner) content(w http.ResponseWriter, r *http.Request) {
	files, path, found := rn.at(w, r, "file")
	if found {
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader(files[path]))
	}
}

// write writes the body as the file of the path, unless the home is full.
func (rn *Runner) write(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	content, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	home := rn.home
	home.mu.Lock()
	defer home.mu.Unlock()
	above := false
	for other := range home.workspace {
		above = above || strings.HasPrefix(path, other+"/")
	}
	_, had := home.workspace[path]
	switch {
	case path == "" || !inside(path):
		refuse(w, http.StatusBadRequest, "invalid_path")
	case home.denied[path]:
		refuse(w, http.StatusForbidden, "not_permitted")
	case home.full:
		refuse(w, http.StatusInsufficientStorage, "no_room")
	case !had && kindOf(home.workspace, path) == "directory":
		refuse(w, http.StatusConflict, "is_a_directory")
	case above:
		refuse(w, http.StatusConflict, "not_a_directory")
	default:
		if home.workspace == nil {
			home.workspace = map[string]string{}
		}
		home.workspace[path] = string(content)
		status := http.StatusCreated
		if had {
			status = http.StatusOK
		}
		answer(w, status, described(home.workspace, path, path[strings.LastIndex(path, "/")+1:]))
	}
}

func (rn *Runner) search(w http.ResponseWriter, r *http.Request) {
	wanted := strings.ToLower(r.URL.Query().Get("query"))
	if wanted == "" {
		refuse(w, http.StatusBadRequest, "invalid_path")
		return
	}
	files := rn.home.Workspace()
	paths := map[string]bool{}
	for file := range files {
		for end := 0; end >= 0; {
			next := strings.Index(file[end:], "/")
			path := file
			if next >= 0 {
				path = file[:end+next]
				end += next + 1
			} else {
				end = -1
			}
			paths[path] = true
		}
	}
	var entries []entry
	for path := range paths {
		name := path[strings.LastIndex(path, "/")+1:]
		if strings.Contains(strings.ToLower(name), wanted) {
			found := described(files, path, name)
			found.Path = path
			entries = append(entries, found)
		}
	}
	slices.SortFunc(entries, func(a, b entry) int {
		begins := func(e entry) int {
			if strings.HasPrefix(strings.ToLower(e.Name), wanted) {
				return 0
			}
			return 1
		}
		return cmp.Or(cmp.Compare(begins(a), begins(b)), cmp.Compare(strings.Count(a.Path, "/"), strings.Count(b.Path, "/")), cmp.Compare(a.Path, b.Path))
	})
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	truncated := limit > 0 && len(entries) > limit
	if truncated {
		entries = entries[:limit]
	}
	answer(w, http.StatusOK, struct {
		Entries   []entry `json:"entries"`
		Truncated bool    `json:"truncated"`
	}{append([]entry{}, entries...), truncated})
}

func (rn *Runner) archive(w http.ResponseWriter, r *http.Request) {
	files, path, found := rn.at(w, r, "directory")
	if !found {
		return
	}
	name := "workspace"
	if path != "" {
		name = path[strings.LastIndex(path, "/")+1:]
	}
	w.Header().Set("Content-Type", "application/zip")
	if rn.home.cuts() {
		io.WriteString(w, "PK")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	archive := zip.NewWriter(w)
	for _, file := range slices.Sorted(maps.Keys(files)) {
		below, is := strings.CutPrefix(file, path+"/")
		if path == "" {
			below, is = file, true
		}
		if !is {
			continue
		}
		if to, err := archive.Create(name + "/" + below); err == nil {
			io.WriteString(to, files[file])
		}
	}
	archive.Close()
}
