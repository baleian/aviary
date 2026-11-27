package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/internal/sandbox/runnertest"
)

// fileEntry is an entry of a workspace as the API returns it.
type fileEntry struct {
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Size       *int64    `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	Target     string    `json:"target"`
	Path       string    `json:"path"`
}

// fileListing is the answer to a listing of a directory, or to a search.
type fileListing struct {
	Entries   []fileEntry `json:"entries"`
	Next      string      `json:"next"`
	Truncated bool        `json:"truncated"`
}

func (l fileListing) names() []string {
	names := make([]string, len(l.Entries))
	for i, entry := range l.Entries {
		names[i] = entry.Name
	}
	return names
}

// unsized is a body whose length the request does not tell.
type unsized struct{ io.Reader }

// files sends a request about the files of the session: what is the rest of
// its address, and header the names and values of its header. It returns the
// status, the header and the body of the answer.
func (s *supervisor) files(method, session, what string, body io.Reader, header ...string) (int, http.Header, string) {
	s.t.Helper()
	request, err := http.NewRequest(method, s.api+"/v1/sessions/"+session+"/files"+what, body)
	if err != nil {
		s.t.Fatal(err)
	}
	for i := 0; i < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatalf("%s files%s: read the answer: %v", method, what, err)
	}
	return response.StatusCode, response.Header, string(answer)
}

// listed returns the answer to a listing or a search, which must succeed.
func (s *supervisor) listed(session, what string) fileListing {
	s.t.Helper()
	status, _, body := s.files("GET", session, what, nil)
	var listing fileListing
	if err := json.Unmarshal([]byte(body), &listing); err != nil || status != http.StatusOK {
		s.t.Fatalf("GET files%s = %d %s, %v", what, status, body, err)
	}
	return listing
}

// code returns the code of an answer that refuses.
func code(body string) string {
	var refusal declined
	_ = json.Unmarshal([]byte(body), &refusal)
	return refusal.Code
}

// saved returns the name under which the answer says to save its file.
func saved(header http.Header) string {
	disposition, parameters, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil || disposition != "attachment" {
		return ""
	}
	return parameters["filename"]
}

// worked sends the session a message, plays its runner through the turn, and
// returns the name of its sandbox.
func (s *supervisor) worked(session string) string {
	s.t.Helper()
	r := s.given(s.send(session, "hello").ID)
	r.report(started("harness-session"))
	r.report(finished("end_turn", nil))
	return r.Sandbox
}

func TestTheFilesOfAWorkspace(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session()
	home := aviary.service.Home(s.worked(session))
	for path, content := range map[string]string{
		"README.md": "# storefront", "notes.txt": "to do", "src/main.go": "package main", "src/checkout/total.go": "package checkout",
	} {
		home.Put(path, content)
	}

	// A directory lists its directories first, and each kind by name.
	listing := s.listed(session, "")
	if !slices.Equal(listing.names(), []string{"src", "notes.txt", "README.md"}) || listing.Next != "" {
		t.Fatalf("the workspace lists %v, next %q", listing.names(), listing.Next)
	}
	if directory := listing.Entries[0]; directory.Type != "directory" || directory.Size != nil {
		t.Errorf("the entry of a directory = %+v", directory)
	}
	if file := listing.Entries[1]; file.Type != "file" || file.Size == nil || *file.Size != 5 || !file.ModifiedAt.Equal(runnertest.Modified) || file.Path != "" {
		t.Errorf("the entry of a file = %+v, want its size and when it was changed", file)
	}
	first := s.listed(session, "?limit=2")
	if !slices.Equal(first.names(), []string{"src", "notes.txt"}) || first.Next == "" {
		t.Fatalf("the first two of the workspace = %v, next %q", first.names(), first.Next)
	}
	if rest := s.listed(session, "?limit=2&after="+url.QueryEscape(first.Next)); !slices.Equal(rest.names(), []string{"README.md"}) || rest.Next != "" {
		t.Errorf("the rest of the workspace = %v, next %q", rest.names(), rest.Next)
	}
	if below := s.listed(session, "?path=src"); !slices.Equal(below.names(), []string{"checkout", "main.go"}) {
		t.Errorf("a directory below lists %v", below.names())
	}

	// A search finds the entries by their names, and tells where they are.
	found := s.listed(session, "/search?query=TOTAL")
	if len(found.Entries) != 1 || found.Entries[0].Path != "src/checkout/total.go" || found.Entries[0].Name != "total.go" || found.Truncated {
		t.Errorf("the search found %+v, truncated %t", found.Entries, found.Truncated)
	}
	if all := s.listed(session, "/search?query=o"); len(all.Entries) != 4 || all.Truncated {
		t.Errorf("a search found %v, truncated %t; want the four names with the letter", all.names(), all.Truncated)
	}
	if some := s.listed(session, "/search?query=o&limit=1"); len(some.Entries) != 1 || !some.Truncated {
		t.Errorf("a search for one found %+v, truncated %t", some.Entries, some.Truncated)
	}

	// A file is sent as a download, whatever it is.
	status, header, body := s.files("GET", session, "/content?path=README.md", nil)
	if status != http.StatusOK || body != "# storefront" || header.Get("Content-Length") != "12" {
		t.Errorf("the file = %d %q, length %s", status, body, header.Get("Content-Length"))
	}
	if saved(header) != "README.md" || header.Get("Content-Type") != "application/octet-stream" ||
		header.Get("X-Content-Type-Options") != "nosniff" || header.Get("Content-Security-Policy") != "sandbox" || header.Get("Accept-Ranges") != "bytes" {
		t.Errorf("the header of a file = %v, want that of a download", header)
	}
	status, header, body = s.files("GET", session, "/content?path=README.md", nil, "Range", "bytes=2-5")
	if status != http.StatusPartialContent || body != "stor" || header.Get("Content-Range") != "bytes 2-5/12" || header.Get("Content-Length") != "4" {
		t.Errorf("a range of the file = %d %q, %v", status, body, header)
	}
	status, header, body = s.files("GET", session, "/content?path=README.md", nil, "Range", "bytes=99-")
	if status != http.StatusRequestedRangeNotSatisfiable || code(body) != "range_not_satisfiable" || header.Get("Content-Range") != "bytes */12" {
		t.Errorf("a range that the file does not have = %d %s, %v", status, body, header)
	}

	// A file that is written replaces the one that is there.
	report := "docs/보고서 (2).md"
	at := "/content?path=" + url.QueryEscape(report)
	var written fileEntry
	status, _, body = s.files("PUT", session, at, strings.NewReader("hello"))
	if err := json.Unmarshal([]byte(body), &written); err != nil || status != http.StatusCreated ||
		written.Name != "보고서 (2).md" || written.Type != "file" || written.Size == nil || *written.Size != 5 {
		t.Fatalf("PUT of a new file = %d %s, %v", status, body, err)
	}
	if status, _, body := s.files("PUT", session, at, strings.NewReader("done")); status != http.StatusOK {
		t.Errorf("PUT of the file again = %d %s, want 200", status, body)
	}
	if got := home.Workspace()[report]; got != "done" {
		t.Errorf("the file in the sandbox is %q, want what was written last", got)
	}
	if status, header, body := s.files("GET", session, at, nil); status != http.StatusOK || body != "done" || saved(header) != "보고서 (2).md" {
		t.Errorf("the written file = %d %q, saved as %q", status, body, saved(header))
	}
	// A file may be empty.
	if status, _, body := s.files("PUT", session, "/content?path=docs/.keep", strings.NewReader("")); status != http.StatusCreated {
		t.Errorf("PUT of an empty file = %d %s, want 201", status, body)
	}
	if status, header, body := s.files("GET", session, "/content?path=docs/.keep", nil); status != http.StatusOK || body != "" || header.Get("Content-Length") != "0" {
		t.Errorf("the empty file = %d %q, length %s", status, body, header.Get("Content-Length"))
	}

	// A directory is sent as a ZIP file, under its name.
	for path, want := range map[string][]string{
		"src": {"src/checkout/total.go", "src/main.go"},
		"":    {"workspace/README.md", "workspace/docs/.keep", "workspace/docs/보고서 (2).md", "workspace/notes.txt", "workspace/src/checkout/total.go", "workspace/src/main.go"},
	} {
		status, header, body := s.files("GET", session, "/archive?path="+path, nil)
		unpacked, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
		if err != nil || status != http.StatusOK {
			t.Fatalf("the archive of %q = %d, %v", path, status, err)
		}
		var names []string
		for _, file := range unpacked.File {
			names = append(names, file.Name)
		}
		if !slices.Equal(names, want) {
			t.Errorf("the archive of %q has %v, want %v", path, names, want)
		}
		if name, _, _ := strings.Cut(want[0], "/"); saved(header) != name+".zip" || header.Get("Content-Type") != "application/zip" || header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("the header of the archive of %q = %v", path, header)
		}
	}

	// An archive that the sandbox does not finish is cut off, so that it is
	// not taken for all of it.
	home.Cut()
	response, err := httpClient.Get(s.api + "/v1/sessions/" + session + "/files/archive")
	if err == nil {
		_, err = io.ReadAll(response.Body)
		response.Body.Close()
	}
	if err == nil {
		t.Errorf("an archive that the sandbox broke off was read to its end, status %d; want the response cut off", response.StatusCode)
	}

	// A link tells what it names, and is listed as that if it is a file of
	// the workspace.
	home.Link("latest", "notes.txt")
	home.Link("cache", "/var/cache")
	linked := s.listed(session, "")
	if !slices.Equal(linked.names(), []string{"docs", "src", "cache", "latest", "notes.txt", "README.md"}) {
		t.Fatalf("the workspace with links lists %v", linked.names())
	}
	if nowhere := linked.Entries[2]; nowhere.Type != "symlink" || nowhere.Target != "/var/cache" || nowhere.Size != nil {
		t.Errorf("the entry of a link that names nothing in the workspace = %+v", nowhere)
	}
	if latest := linked.Entries[3]; latest.Type != "file" || latest.Target != "notes.txt" || latest.Size == nil || *latest.Size != 5 {
		t.Errorf("the entry of a link to a file = %+v, want the file with what the link names", latest)
	}

	var limits struct {
		MaxFileBytes int64 `json:"max_file_bytes"`
	}
	if status, _ := s.request("GET", "/v1/limits", "", &limits); status != http.StatusOK || limits.MaxFileBytes != 1<<20 {
		t.Errorf("GET /v1/limits = %d %+v, want a mebibyte for a file", status, limits)
	}
}

func TestFilesThatAreRefused(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session()
	name := s.worked(session)
	home := aviary.service.Home(name)
	home.Put("README.md", "# storefront")
	home.Put("src/main.go", "package main")
	home.Put("secret.key", "a key")
	home.Deny("secret.key")

	type refusal struct {
		method, what, body string
		status             int
		code               string
	}
	// What the API refuses by itself, without asking the sandbox.
	unasked := []refusal{
		// A path stays below the workspace, and has one form.
		{"GET", "?path=../home", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?path=/etc", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?path=src//main.go", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?path=src/./main.go", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?path=src/", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?path=src%00", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?path=src%ff", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?path=" + strings.Repeat("a/", 2048) + "a", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?limit=0", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?limit=1001", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?after=d/%ff", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "?after=d/" + strings.Repeat("a", 4096), "", http.StatusBadRequest, "invalid_request"},
		{"GET", "/search", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "/search?query=%ff", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "/search?query=" + strings.Repeat("a", 256), "", http.StatusBadRequest, "invalid_request"},
		{"GET", "/search?query=a&limit=1001", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "/content", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "/content?path=../README.md", "", http.StatusBadRequest, "invalid_request"},
		{"GET", "/archive?path=..", "", http.StatusBadRequest, "invalid_request"},
		{"PUT", "/content", "a", http.StatusBadRequest, "invalid_request"},
		{"PUT", "/content?path=../a.txt", "a", http.StatusBadRequest, "invalid_request"},
		{"PUT", "/content?path=larger.bin", strings.Repeat("a", 1<<20+1), http.StatusRequestEntityTooLarge, "file_too_large"},
	}
	check := func(refused refusal) {
		t.Helper()
		what := refused.what
		if len(what) > 80 {
			what = what[:80] + "…"
		}
		status, header, body := s.files(refused.method, session, refused.what, strings.NewReader(refused.body))
		if status != refused.status || code(body) != refused.code || header.Get("Content-Disposition") != "" {
			t.Errorf("%s files%s = %d %s, want %d %s", refused.method, what, status, body, refused.status, refused.code)
		}
	}
	for _, refused := range append(unasked,
		refusal{"GET", "?path=missing", "", http.StatusNotFound, "not_found"},
		refusal{"GET", "?path=README.md", "", http.StatusBadRequest, "not_a_directory"},
		refusal{"GET", "?after=README.md", "", http.StatusBadRequest, "invalid_request"},
		refusal{"GET", "/content?path=src", "", http.StatusBadRequest, "is_a_directory"},
		refusal{"GET", "/content?path=missing.txt", "", http.StatusNotFound, "not_found"},
		refusal{"GET", "/content?path=secret.key", "", http.StatusForbidden, "not_permitted"},
		refusal{"PUT", "/content?path=secret.key", "a", http.StatusForbidden, "not_permitted"},
		refusal{"GET", "/archive?path=README.md", "", http.StatusBadRequest, "not_a_directory"},
		refusal{"GET", "/archive?path=missing", "", http.StatusNotFound, "not_found"},
		refusal{"PUT", "/content?path=src", "a", http.StatusBadRequest, "is_a_directory"},
		refusal{"PUT", "/content?path=README.md/more", "a", http.StatusBadRequest, "not_a_directory"},
	) {
		check(refused)
	}
	// The largest file is taken, and one whose length is not told is not.
	if status, _, body := s.files("PUT", session, "/content?path=largest.bin", strings.NewReader(strings.Repeat("a", 1<<20))); status != http.StatusCreated {
		t.Errorf("PUT of the largest file = %d %s, want 201", status, body)
	}
	if status, _, body := s.files("PUT", session, "/content?path=untold.bin", unsized{strings.NewReader("a")}); status != http.StatusLengthRequired || code(body) != "length_required" {
		t.Errorf("PUT of a file without its length = %d %s, want 411 length_required", status, body)
	}
	home.Fill()
	if status, _, body := s.files("PUT", session, "/content?path=more.txt", strings.NewReader("a")); status != http.StatusInsufficientStorage || code(body) != "storage_full" {
		t.Errorf("PUT into a full home = %d %s, want 507 storage_full", status, body)
	}
	if has := home.Workspace(); len(has) != 4 || has["largest.bin"] == "" || has["secret.key"] != "a key" {
		t.Errorf("the workspace has %d files after the refusals, want the three that it had as they were and the largest", len(has))
	}

	// What the API refuses by itself does not wake the sandbox.
	if err := aviary.service.Suspend(context.Background(), name, false); err != nil {
		t.Fatal(err)
	}
	for _, refused := range unasked {
		check(refused)
	}
	if status, _, body := s.files("PUT", session, "/content?path=untold.bin", unsized{strings.NewReader("a")}); status != http.StatusLengthRequired {
		t.Errorf("PUT of a file without its length = %d %s, want 411", status, body)
	}
	if state := aviary.service.State(name); state != sandbox.Asleep {
		t.Errorf("the sandbox is %s after requests that the API refuses by itself, want it asleep", state)
	}

	// A session that has ended has no files, and a session that does not
	// exist none either.
	if status, _ := s.request("DELETE", "/v1/sessions/"+session, "", nil); status != http.StatusOK {
		t.Fatalf("DELETE session: status %d", status)
	}
	for _, what := range []string{"", "/search?query=a", "/content?path=README.md", "/archive"} {
		if status, _, body := s.files("GET", session, what, nil); status != http.StatusConflict || code(body) != "session_ended" {
			t.Errorf("GET files%s of a session that has ended = %d %s, want 409 session_ended", what, status, body)
		}
		if status, _, _ := s.files("GET", uuid.NewString(), what, nil); status != http.StatusNotFound {
			t.Errorf("GET files%s of no session = %d, want 404", what, status)
		}
	}
	if status, _, body := s.files("PUT", session, "/content?path=a.txt", strings.NewReader("a")); status != http.StatusConflict || code(body) != "session_ended" {
		t.Errorf("PUT of a file of a session that has ended = %d %s, want 409 session_ended", status, body)
	}
}

func TestASandboxIsWokenForItsFiles(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session()
	name := s.worked(session)
	aviary.service.Home(name).Put("README.md", "# storefront")
	asleep := func() {
		t.Helper()
		if err := aviary.service.Suspend(context.Background(), name, false); err != nil {
			t.Fatal(err)
		}
	}

	asleep()
	woken := aviary.service.Woken(name)
	if listing := s.listed(session, ""); !slices.Equal(listing.names(), []string{"README.md"}) {
		t.Errorf("the workspace of a sandbox that slept lists %v", listing.names())
	}
	if aviary.service.Woken(name) != woken+1 || aviary.service.State(name) != sandbox.Running {
		t.Errorf("the sandbox was woken %d times and is %s, want once and running", aviary.service.Woken(name)-woken, aviary.service.State(name))
	}

	// Without room for the sandbox, the caller is told to ask again.
	unavailable := func(what, want string) {
		t.Helper()
		for _, request := range []struct{ method, what string }{{"GET", ""}, {"GET", "/content?path=README.md"}, {"PUT", "/content?path=a.txt"}} {
			status, header, body := s.files(request.method, session, request.what, strings.NewReader("a"))
			if status != http.StatusServiceUnavailable || code(body) != want || header.Get("Retry-After") != "5" {
				t.Errorf("%s files%s of a sandbox %s = %d %s, retry after %q; want 503 %s and when to ask again",
					request.method, request.what, what, status, body, header.Get("Retry-After"), want)
			}
		}
	}
	asleep()
	aviary.service.Room(1)
	busy := s.given(s.send(s.session(), "work").ID)
	busy.report(started("harness-session"))
	unavailable("without room", "no_room")
	busy.report(finished("end_turn", nil))
	if listing := s.listed(session, ""); len(listing.Entries) != 1 {
		t.Errorf("the workspace lists %v once an idle sandbox can make room", listing.names())
	}
	aviary.service.Room(0)

	// So for a sandbox that has crashed, until it is reverted.
	aviary.service.Crash(name, "out of memory")
	unavailable("that has crashed", "sandbox_unavailable")

	// And for one with another operation in progress, if that does not end
	// soon.
	patient := install(t, func(config *driver.Config) { config.Sweep = 200 * time.Millisecond })
	s = patient.supervisor()
	session = s.session()
	name = s.worked(session)
	patient.service.Home(name).Put("README.md", "# storefront")
	if err := patient.service.Suspend(context.Background(), name, false); err != nil {
		t.Fatal(err)
	}
	patient.service.Operate(name, 2)
	if listing := s.listed(session, ""); len(listing.Entries) != 1 {
		t.Errorf("the workspace lists %v after another operation on its sandbox", listing.names())
	}
	if err := patient.service.Suspend(context.Background(), name, false); err != nil {
		t.Fatal(err)
	}
	patient.service.Operate(name, 1<<30)
	unavailable("with another operation", "sandbox_unavailable")
}

func TestASessionWithoutASandboxHasNoFiles(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session()

	// The workspace is empty, and reading it makes no sandbox.
	if status, _, body := s.files("GET", session, "", nil); status != http.StatusOK || strings.TrimSpace(body) != `{"entries":[]}` {
		t.Errorf("the workspace of a session without a sandbox = %d %s, want an empty listing", status, body)
	}
	if status, _, body := s.files("GET", session, "/search?query=a", nil); status != http.StatusOK || strings.TrimSpace(body) != `{"entries":[],"truncated":false}` {
		t.Errorf("a search of a session without a sandbox = %d %s, want nothing found", status, body)
	}
	for _, what := range []string{"?path=src", "/content?path=README.md", "/archive", "/archive?path=src"} {
		if status, _, body := s.files("GET", session, what, nil); status != http.StatusNotFound || code(body) != "not_found" {
			t.Errorf("GET files%s of a session without a sandbox = %d %s, want 404 not_found", what, status, body)
		}
	}
	if made := aviary.service.Made(); len(made) != 0 {
		t.Fatalf("reading the files made the sandboxes %v", made)
	}

	// The first file that is written makes the sandbox.
	if status, _, body := s.files("PUT", session, "/content?path=data/plans.json", strings.NewReader("{}")); status != http.StatusCreated {
		t.Fatalf("PUT of a file of a session without a sandbox = %d %s, want 201", status, body)
	}
	name := sandbox.Name(uuid.MustParse(session))
	if made := aviary.made(name); made.Harness != "opencode" || aviary.service.State(name) != sandbox.Running {
		t.Errorf("the sandbox was made for %q and is %s, want the harness of the agent and running", made.Harness, aviary.service.State(name))
	}
	if got := aviary.service.Home(name).Workspace()["data/plans.json"]; got != "{}" {
		t.Errorf("the file in the sandbox is %q", got)
	}
	var record struct {
		Sandbox string `json:"sandbox"`
	}
	if status, _ := s.request("GET", "/v1/sessions/"+session, "", &record); status != http.StatusOK || record.Sandbox != "awake" {
		t.Errorf("the session's sandbox = %d %q, want awake", status, record.Sandbox)
	}
	if listing := s.listed(session, "?path=data"); !slices.Equal(listing.names(), []string{"plans.json"}) {
		t.Errorf("the workspace lists %v after the file was written", listing.names())
	}
	// The session's first turn runs in that sandbox.
	if runner := s.given(s.send(session, "hello").ID); runner.Sandbox != name {
		t.Errorf("the turn was given to the sandbox %s, want %s", runner.Sandbox, name)
	}
}
