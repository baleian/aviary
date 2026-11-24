package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/internal/depot"
	"aviary/supervisor/internal/driver"
)

// attaching lets the messages of an installation carry files, which a depot
// of the test keeps in the directory that is returned: up to three files of
// a mebibyte each.
func attaching(t *testing.T) (func(*driver.Config), string) {
	t.Helper()
	dir := t.TempDir()
	objects := runDepot(t, dir)
	return func(config *driver.Config) {
		config.Depot = objects.Client
		config.MaxAttachmentBytes = 1 << 20
		config.MaxAttachments = 3
	}, dir
}

// attachment is an attachment as the API returns it.
type attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
}

// declined is the error that the API returns.
type declined struct{ Code, Message string }

// upload uploads the content as a file of the session, and returns the
// status and the answer: the attachment, or why it was refused.
func (s *supervisor) upload(session, name, mediaType string, content io.Reader) (int, attachment, declined) {
	s.t.Helper()
	request, err := http.NewRequest("POST", s.api+"/v1/sessions/"+session+"/attachments?name="+url.QueryEscape(name), content)
	if err != nil {
		s.t.Fatal(err)
	}
	if mediaType != "" {
		request.Header.Set("Content-Type", mediaType)
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
		s.t.Fatal(err)
	}
	var uploaded attachment
	var refused declined
	into := any(&uploaded)
	if response.StatusCode != http.StatusCreated {
		into = &refused
	}
	if err := json.Unmarshal(answer, into); err != nil {
		s.t.Fatalf("upload %s: decode response %d %s: %v", name, response.StatusCode, answer, err)
	}
	return response.StatusCode, uploaded, refused
}

// attach uploads a file of the session with the content, and returns it.
func (s *supervisor) attach(session, name, content string) attachment {
	s.t.Helper()
	status, uploaded, refused := s.upload(session, name, "text/plain", strings.NewReader(content))
	if status != http.StatusCreated {
		s.t.Fatalf("upload %s: status %d %+v, want 201", name, status, refused)
	}
	return uploaded
}

// download returns the status, the header and the body of the attachment.
func (s *supervisor) download(id string) (int, http.Header, string) {
	s.t.Helper()
	request, err := http.NewRequest("GET", s.api+"/v1/attachments/"+id, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return response.StatusCode, response.Header, string(body)
}

// kept returns the keys of the attachments in the depot's directory.
func kept(t *testing.T, dir string) []string {
	t.Helper()
	keys := []string{}
	found, err := filepath.Glob(filepath.Join(dir, "attachments", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range found {
		keys = append(keys, strings.TrimPrefix(path, dir+"/"))
	}
	return keys
}

// carrying sends the message with the attachments, and returns the status
// and the answer as it is.
func (s *supervisor) carrying(session, text string, attachments ...string) (int, map[string]any) {
	s.t.Helper()
	body, err := json.Marshal(map[string]any{"text": text, "attachments": attachments})
	if err != nil {
		s.t.Fatal(err)
	}
	var answer map[string]any
	status, _ := s.request("POST", "/v1/sessions/"+session+"/messages", string(body), &answer)
	return status, answer
}

// carried returns the attachments of a turn as the API returned them.
func carried(t *testing.T, turn map[string]any) []attachment {
	t.Helper()
	encoded, err := json.Marshal(turn["attachments"])
	if err != nil {
		t.Fatal(err)
	}
	var attachments []attachment
	if err := json.Unmarshal(encoded, &attachments); err != nil {
		t.Fatalf("attachments of the turn %s: %v", encoded, err)
	}
	return attachments
}

// A file is uploaded for a session and kept in the depot. A message carries
// the files that it names, its turn lists them, and each can be downloaded
// until the session ends.
func TestAttachmentsAreUploadedAndCarriedByAMessage(t *testing.T) {
	t.Parallel()
	attachments, dir := attaching(t)
	aviary := install(t, attachments)
	s := aviary.supervisor()
	session := s.session()

	report := strings.Repeat("%PDF-1.7 ", 1000)
	status, pdf, refused := s.upload(session, "보고서 2026.pdf", "application/pdf", strings.NewReader(report))
	if status != http.StatusCreated || pdf.ID == "" || pdf.Name != "보고서 2026.pdf" || pdf.MediaType != "application/pdf" || pdf.Size != int64(len(report)) {
		t.Fatalf("upload = %d %+v %+v, want 201 with the file as it was sent", status, pdf, refused)
	}
	// A file whose size is not known beforehand is taken as well.
	status, notes, refused := s.upload(session, "notes.txt", "", io.NopCloser(strings.NewReader("to do")))
	if status != http.StatusCreated || notes.Size != 5 || notes.MediaType != "application/octet-stream" {
		t.Fatalf("upload of an unknown size and kind = %d %+v %+v, want 201 as application/octet-stream", status, notes, refused)
	}
	for _, uploaded := range []attachment{pdf, notes} {
		key := "attachments/" + session + "/" + uploaded.ID
		if stored, err := os.ReadFile(filepath.Join(dir, key)); err != nil || int64(len(stored)) != uploaded.Size {
			t.Errorf("the depot has %d bytes at %s, %v; want the %d of the file", len(stored), key, err, uploaded.Size)
		}
	}

	status, accepted := s.carrying(session, "Summarize the report.", notes.ID, pdf.ID)
	if status != http.StatusAccepted {
		t.Fatalf("message with attachments = %d %v, want 202", status, accepted)
	}
	want := []attachment{notes, pdf}
	for what, turn := range map[string]func() map[string]any{
		"the answer to the message": func() map[string]any { return accepted },
		"the turn": func() map[string]any {
			var read map[string]any
			if status, _ := s.request("GET", "/v1/turns/"+accepted["id"].(string), "", &read); status != http.StatusOK {
				t.Fatalf("GET turn: status %d", status)
			}
			return read
		},
		"the turns of the session": func() map[string]any {
			var list struct{ Turns []map[string]any }
			if status, _ := s.request("GET", "/v1/sessions/"+session+"/turns", "", &list); status != http.StatusOK || len(list.Turns) != 1 {
				t.Fatalf("GET turns: status %d, %d turns", status, len(list.Turns))
			}
			return list.Turns[0]
		},
	} {
		if got := carried(t, turn()); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("attachments of %s = %+v, want %+v", what, got, want)
		}
	}

	status, header, body := s.download(pdf.ID)
	if status != http.StatusOK || body != report {
		t.Fatalf("download = %d with %d bytes, want 200 with the %d of the file", status, len(body), len(report))
	}
	for name, value := range map[string]string{
		"Content-Type":   "application/pdf",
		"Content-Length": fmt.Sprint(len(report)),
		// A browser saves the file, and does not open it at the address of
		// the API.
		"Content-Disposition":     "attachment; filename*=utf-8''%EB%B3%B4%EA%B3%A0%EC%84%9C%202026.pdf",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
	} {
		if header.Get(name) != value {
			t.Errorf("download header %s = %q, want %q", name, header.Get(name), value)
		}
	}
	if status, _, _ := s.download("00000000-0000-0000-0000-000000000000"); status != http.StatusNotFound {
		t.Errorf("download of an unknown attachment: status %d, want 404", status)
	}

	// A file is kept only until its session ends.
	aviary.given(accepted["id"].(string)).report(finished("end_turn", nil))
	if status, _ := s.request("DELETE", "/v1/sessions/"+session, "", nil); status != http.StatusOK {
		t.Fatalf("end session: status %d, want 200", status)
	}
	if status, _, _ := s.download(pdf.ID); status != http.StatusNotFound {
		t.Errorf("download from an ended session: status %d, want 404", status)
	}
	if status, _, refused := s.upload(session, "late.txt", "text/plain", strings.NewReader("late")); status != http.StatusConflict || refused.Code != "session_ended" {
		t.Errorf("upload to an ended session = %d %+v, want 409 session_ended", status, refused)
	}
	if status, _, refused := s.upload("00000000-0000-0000-0000-000000000000", "a.txt", "text/plain", strings.NewReader("a")); status != http.StatusNotFound {
		t.Errorf("upload to an unknown session = %d %+v, want 404", status, refused)
	}
}

// A file is kept under the last element of the name that it was uploaded
// with, without what a file name should not have, and as what the uploader
// said that it is.
func TestAttachmentNamesAndKinds(t *testing.T) {
	t.Parallel()
	attachments, _ := attaching(t)
	s := install(t, attachments).supervisor()
	session := s.session()
	long := strings.Repeat("가", 100)
	for given, want := range map[string]string{
		"report.pdf":               "report.pdf",
		"../../etc/passwd":         "passwd",
		`C:\Users\me\Q3 plan.xlsx`: "Q3 plan.xlsx",
		"  spaced.txt  ":           "spaced.txt",
		".env":                     "env",
		"...hidden":                "hidden",
		"tab\there\x00.txt":        "tabhere.txt",
		// A name is cut before its extension, and not within a character.
		long + ".docx": strings.Repeat("가", 65) + ".docx",
	} {
		status, uploaded, refused := s.upload(session, given, "text/plain", strings.NewReader("a"))
		if status != http.StatusCreated || uploaded.Name != want {
			t.Errorf("upload as %q = %d %q %+v, want 201 as %q", given, status, uploaded.Name, refused, want)
		}
	}
	for _, given := range []string{"", "/", "..", " . ", "dir/"} {
		if status, _, refused := s.upload(session, given, "text/plain", strings.NewReader("a")); status != http.StatusBadRequest || refused.Code != "invalid_request" {
			t.Errorf("upload as %q = %d %+v, want 400 invalid_request", given, status, refused)
		}
	}
	for said, want := range map[string]string{
		"image/png":                     "image/png",
		"Text/Plain; charset=utf-8":     "text/plain",
		"":                              "application/octet-stream",
		"nonsense":                      "application/octet-stream",
		"a/" + strings.Repeat("b", 200): "application/octet-stream",
	} {
		status, uploaded, refused := s.upload(session, "a", said, strings.NewReader("a"))
		if status != http.StatusCreated || uploaded.MediaType != want {
			t.Errorf("upload as %q = %d %q %+v, want 201 as %q", said, status, uploaded.MediaType, refused, want)
		}
	}
}

// A file that is larger than an attachment may be is refused, before it is
// read if it says so, and nothing of it stays in the depot. A message
// carries no more files than it may, and needs a text only without files.
func TestAttachmentLimits(t *testing.T) {
	t.Parallel()
	attachments, dir := attaching(t)
	aviary := install(t, attachments)
	s := aviary.supervisor()
	session := s.session()

	type limits struct {
		MaxAttachmentBytes int64 `json:"max_attachment_bytes"`
		MaxAttachments     int   `json:"max_attachments"`
	}
	var told limits
	if status, _ := s.request("GET", "/v1/limits", "", &told); status != http.StatusOK || told != (limits{1 << 20, 3}) {
		t.Errorf("GET /v1/limits = %d %+v, want a mebibyte and three files", status, told)
	}

	most := strings.Repeat("a", 1<<20)
	if status, uploaded, refused := s.upload(session, "most.bin", "", strings.NewReader(most)); status != http.StatusCreated || uploaded.Size != 1<<20 {
		t.Fatalf("upload of the largest file = %d %+v %+v, want 201", status, uploaded, refused)
	}
	// One byte more is too large, whether the request says its size or not.
	for what, larger := range map[string]io.Reader{
		"says that it is too large": strings.NewReader(most + "b"),
		"is too large":              io.NopCloser(strings.NewReader(most + "b")),
	} {
		if status, _, refused := s.upload(session, "larger.bin", "", larger); status != http.StatusRequestEntityTooLarge || refused.Code != "attachment_too_large" {
			t.Errorf("upload that %s = %d %+v, want 413 attachment_too_large", what, status, refused)
		}
	}
	if left := kept(t, dir); len(left) != 1 {
		t.Errorf("the depot has %v after refused uploads, want only the file that was taken", left)
	}

	var files []string
	for i := range 4 {
		files = append(files, s.attach(session, fmt.Sprintf("%d.txt", i), "a").ID)
	}
	if status, answer := s.carrying(session, "look", files...); status != http.StatusBadRequest || answer["code"] != "too_many_attachments" {
		t.Errorf("message with four attachments = %d %v, want 400 too_many_attachments", status, answer)
	}
	if status, answer := s.carrying(session, ""); status != http.StatusBadRequest || answer["code"] != "invalid_request" {
		t.Errorf("message without text and attachments = %d %v, want 400 invalid_request", status, answer)
	}
	status, accepted := s.carrying(session, "", files[:3]...)
	if status != http.StatusAccepted || len(carried(t, accepted)) != 3 || accepted["text"] != "" {
		t.Errorf("message of three attachments and no text = %d %v, want 202", status, accepted)
	}

	// An installation that takes no attachments says so.
	plain := install(t).supervisor()
	other := plain.session()
	// Nor does one without a depot take any, whatever its limits are.
	bare := install(t, func(config *driver.Config) { config.MaxAttachmentBytes, config.MaxAttachments = 1<<20, 3 }).supervisor()
	for what, without := range map[string]*supervisor{"attachments": plain, "a depot": bare} {
		told = limits{-1, -1}
		if status, _ := without.request("GET", "/v1/limits", "", &told); status != http.StatusOK || told != (limits{}) {
			t.Errorf("GET /v1/limits of an installation without %s = %d %+v, want zeros", what, status, told)
		}
	}
	if status, _, refused := plain.upload(other, "a.txt", "text/plain", strings.NewReader("a")); status != http.StatusForbidden || refused.Code != "attachments_disabled" {
		t.Errorf("upload to an installation without attachments = %d %+v, want 403 attachments_disabled", status, refused)
	}
	if status, answer := plain.carrying(other, "look", files[0]); status != http.StatusBadRequest || answer["code"] != "too_many_attachments" {
		t.Errorf("message with an attachment to an installation without attachments = %d %v, want 400 too_many_attachments", status, answer)
	}
}

// A message carries only attachments of its session that no message carries
// yet. A message that names another is refused whole.
func TestMessageRefusesAttachmentsThatAreNotItsOwn(t *testing.T) {
	t.Parallel()
	attachments, _ := attaching(t)
	aviary := install(t, attachments)
	s := aviary.supervisor()
	session, other := s.session(), s.session()
	mine, sent, foreign := s.attach(session, "mine.txt", "a"), s.attach(session, "sent.txt", "b"), s.attach(other, "foreign.txt", "c")
	status, first := s.carrying(session, "one", sent.ID)
	if status != http.StatusAccepted {
		t.Fatalf("message with an attachment = %d %v", status, first)
	}
	aviary.given(first["id"].(string)).report(finished("end_turn", nil))

	for what, named := range map[string][]string{
		"an unknown attachment":                      {mine.ID, "00000000-0000-0000-0000-000000000000"},
		"an attachment of another session":           {foreign.ID},
		"an attachment that another message carries": {mine.ID, sent.ID},
		"an attachment twice":                        {mine.ID, mine.ID},
	} {
		if status, answer := s.carrying(session, "two", named...); status != http.StatusBadRequest || answer["code"] != "unknown_attachment" {
			t.Errorf("message with %s = %d %v, want 400 unknown_attachment", what, status, answer)
		}
	}
	if status, answer := s.request("POST", "/v1/sessions/"+session+"/messages", `{"text": "two", "attachments": ["not-an-id"]}`, nil); status != http.StatusBadRequest {
		t.Errorf("message with an attachment that is no ID = %d %v, want 400", status, answer)
	}
	var list struct{ Turns []map[string]any }
	if status, _ := s.request("GET", "/v1/sessions/"+session+"/turns", "", &list); status != http.StatusOK || len(list.Turns) != 1 {
		t.Errorf("the session has %d turns after refused messages (status %d), want its one turn", len(list.Turns), status)
	}
	// Two files of one name get different names in the message.
	again := s.attach(session, "mine.txt", "d")
	status, second := s.carrying(session, "two", mine.ID, again.ID)
	if status != http.StatusAccepted {
		t.Fatalf("message after refused ones = %d %v, want 202", status, second)
	}
	if got := carried(t, second); len(got) != 2 || got[0].Name != "mine.txt" || got[1].Name != "mine (2).txt" {
		t.Errorf("attachments of one name in a message = %+v, want the second renamed", got)
	}
}

// A file that no message carries is deleted after a while, and so are the
// files of a session some time after it has ended.
func TestAttachmentsAreDeletedWhenUnsentOrLeft(t *testing.T) {
	t.Parallel()
	attachments, dir := attaching(t)
	aviary := install(t, attachments, func(config *driver.Config) {
		config.UnsentFor = 300 * time.Millisecond
		// Attachments are looked at only every few sweeps.
		config.Sweep = 20 * time.Millisecond
	})
	s := aviary.supervisor()
	session := s.session()
	sent, unsent := s.attach(session, "sent.txt", "a"), s.attach(session, "unsent.txt", "b")
	status, accepted := s.carrying(session, "look", sent.ID)
	if status != http.StatusAccepted {
		t.Fatalf("message with an attachment = %d %v", status, accepted)
	}
	key := func(of attachment) string { return "attachments/" + session + "/" + of.ID }
	eventually(t, patience, "the unsent attachment is still in the depot", func() bool {
		left := kept(t, dir)
		return len(left) == 1 && left[0] == key(sent)
	})
	if status, _, _ := s.download(unsent.ID); status != http.StatusNotFound {
		t.Errorf("download of a deleted attachment: status %d, want 404", status)
	}
	if status, answer := s.carrying(session, "again", unsent.ID); status != http.StatusConflict && (status != http.StatusBadRequest || answer["code"] != "unknown_attachment") {
		t.Errorf("message with a deleted attachment = %d %v, want it refused", status, answer)
	}
	// The file that a message carries stays for as long as the session.
	time.Sleep(500 * time.Millisecond)
	if status, _, body := s.download(sent.ID); status != http.StatusOK || body != "a" {
		t.Errorf("download of a carried attachment after a while = %d %q, want 200", status, body)
	}

	aviary.given(accepted["id"].(string)).report(finished("end_turn", nil))
	if status, _ := s.request("DELETE", "/v1/sessions/"+session, "", nil); status != http.StatusOK {
		t.Fatalf("end session: status %d, want 200", status)
	}
	eventually(t, patience, "the attachments of the ended session are still in the depot", func() bool {
		_, err := os.Stat(filepath.Join(dir, "attachments", session))
		return os.IsNotExist(err)
	})
	// The turn still names what its message carried.
	var read map[string]any
	if status, _ := s.request("GET", "/v1/turns/"+accepted["id"].(string), "", &read); status != http.StatusOK || len(carried(t, read)) != 1 {
		t.Errorf("turn of the ended session = %d %v, want it with its attachment", status, read)
	}
}

// full is a depot that has no room.
type full struct{ driver.Depot }

func (full) Put(_ context.Context, key string, body io.Reader) (int64, error) {
	_, _ = io.Copy(io.Discard, body)
	return 0, fmt.Errorf("depot: PUT %s: %w", key, depot.ErrFull)
}

// An upload for which the installation has no room is refused as such.
func TestAnUploadIsRefusedWhenTheDepotIsFull(t *testing.T) {
	t.Parallel()
	attachments, dir := attaching(t)
	s := install(t, attachments, func(config *driver.Config) { config.Depot = full{config.Depot} }).supervisor()
	status, _, refused := s.upload(s.session(), "a.txt", "text/plain", bytes.NewReader([]byte("a")))
	if status != http.StatusInsufficientStorage || refused.Code != "storage_full" {
		t.Errorf("upload to a full depot = %d %+v, want 507 storage_full", status, refused)
	}
	if left := kept(t, dir); len(left) != 0 {
		t.Errorf("the depot has %v after a refused upload", left)
	}
}

// The files that a message carries are in the home of the session's sandbox
// when its turn is given to the runner, under the number of the turn, and
// stay there for the turns that follow.
func TestTheFilesOfAMessageAreInTheSandboxBeforeItsTurn(t *testing.T) {
	t.Parallel()
	attachments, _ := attaching(t)
	aviary := install(t, attachments)
	s := aviary.supervisor()
	session := s.session()
	notes, again := s.attach(session, "notes.txt", "to do"), s.attach(session, "notes.txt", "done")
	status, first := s.carrying(session, "look", notes.ID, again.ID)
	if status != http.StatusAccepted {
		t.Fatalf("message with attachments = %d %v", status, first)
	}
	runner := aviary.given(first["id"].(string))
	home := aviary.service.Home(runner.Sandbox)
	want := map[string]string{"1/notes.txt": "to do", "1/notes (2).txt": "done"}
	if got := home.Files(); !maps.Equal(got, want) {
		t.Errorf("files of the home when the runner has the turn = %q, want %q", got, want)
	}
	runner.report(finished("end_turn", nil))

	// A message without files brings none.
	runner = aviary.given(s.send(session, "and now").ID)
	if got := home.Files(); !maps.Equal(got, want) {
		t.Errorf("files of the home after a message without files = %q, want %q", got, want)
	}
	runner.report(finished("end_turn", nil))

	report := s.attach(session, "notes.txt", "a report")
	status, third := s.carrying(session, "", report.ID)
	if status != http.StatusAccepted {
		t.Fatalf("message of an attachment = %d %v", status, third)
	}
	aviary.given(third["id"].(string))
	want["3/notes.txt"] = "a report"
	if got := home.Files(); !maps.Equal(got, want) {
		t.Errorf("files of the home after the third message = %q, want %q", got, want)
	}
}

// A turn whose files do not fit into the home of its sandbox fails and says
// so, without reaching its runner. The session takes the next message.
func TestATurnFailsIfItsFilesDoNotFitIntoTheSandbox(t *testing.T) {
	t.Parallel()
	attachments, _ := attaching(t)
	aviary := install(t, attachments)
	s := aviary.supervisor()
	session := s.session()
	runner := aviary.given(s.send(session, "hello").ID)
	runner.report(finished("end_turn", nil))
	aviary.service.Home(runner.Sandbox).Fill()

	status, accepted := s.carrying(session, "look", s.attach(session, "large.bin", "a").ID)
	if status != http.StatusAccepted {
		t.Fatalf("message with an attachment = %d %v", status, accepted)
	}
	id := accepted["id"].(string)
	eventually(t, patience, "the turn whose file does not fit has not ended", func() bool {
		return s.turn(id).EndedAt != nil
	})
	failed := s.turn(id)
	if failed.State != "failed" || failed.Failure["reason"] != "attachment_failed" || !strings.Contains(failed.Failure["message"], "large.bin") || failed.StartedAt != nil {
		t.Errorf("turn whose file does not fit = %+v, want it failed as attachment_failed, naming the file, before it started", failed)
	}
	if got := aviary.service.Home(runner.Sandbox).Files(); len(got) != 0 {
		t.Errorf("files of the full home = %q, want none", got)
	}
	aviary.given(s.send(session, "and now").ID).report(finished("end_turn", nil))
}
