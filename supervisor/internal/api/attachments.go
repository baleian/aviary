package api

import (
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"aviary/supervisor/store"
)

var (
	errAttachmentTooLarge = &apiError{status: http.StatusRequestEntityTooLarge, Code: "attachment_too_large", Message: "the file is larger than an attachment may be"}
	errTooManyAttachments = &apiError{status: http.StatusBadRequest, Code: "too_many_attachments", Message: "the message carries more attachments than a message may"}
	errUnknownAttachment  = &apiError{status: http.StatusBadRequest, Code: "unknown_attachment", Message: "an attachment is not one of the session that no message carries yet"}
	errNoAttachments      = &apiError{status: http.StatusForbidden, Code: "attachments_disabled", Message: "the installation takes no attachments"}
	errStorageFull        = &apiError{status: http.StatusInsufficientStorage, Code: "storage_full", Message: "the installation has no room for the file"}
)

// maxFileName is the longest name of an attachment, in bytes. It leaves
// room, within the limit of a file name, for the number that a message gives
// a name that it has twice.
const maxFileName = 200

// getLimits tells what the installation sets for the files that messages
// carry, so that a client can refuse a file before it uploads it.
func (a *api) getLimits(w http.ResponseWriter, _ *http.Request) error {
	size, files := a.driver.AttachmentLimits()
	respond(w, http.StatusOK, struct {
		MaxAttachmentBytes int64 `json:"max_attachment_bytes"`
		MaxAttachments     int   `json:"max_attachments"`
	}{size, files})
	return nil
}

// uploadAttachment keeps the body of the request as a file for a message of
// the session to carry. The query parameter name is the name of the file,
// and the Content-Type header says what the file is.
func (a *api) uploadAttachment(w http.ResponseWriter, r *http.Request) error {
	sessionID, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	name := fileName(r.URL.Query().Get("name"))
	if name == "" {
		return invalid("name must be the name of the file")
	}
	attachment, err := a.driver.Attach(r.Context(), sessionID, callerOf(r).id, name, mediaType(r.Header.Get("Content-Type")), r.ContentLength, r.Body)
	if err != nil {
		return err
	}
	respond(w, http.StatusCreated, attachment)
	return nil
}

// downloadAttachment sends the file of the attachment, always as a download:
// a browser does not open at the address of the API what a user uploaded.
func (a *api) downloadAttachment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "attachment")
	if err != nil {
		return err
	}
	attachment, file, err := a.driver.Attachment(r.Context(), id)
	if err != nil {
		return err
	}
	defer file.Close()
	header := w.Header()
	header.Set("Content-Type", attachment.MediaType)
	header.Set("Content-Length", strconv.FormatInt(attachment.Size, 10))
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Name}))
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "sandbox")
	// The status is already sent, so a failed write can only be logged.
	if _, err := io.Copy(w, file); err != nil {
		slog.ErrorContext(r.Context(), "send attachment", "attachment", id, "error", err)
	}
	return nil
}

// fileName returns the name under which an uploaded file is kept: the last
// element of the given path, without control characters and without leading
// dots, and cut before its extension if it is too long. It is empty if
// nothing of the given name is left.
func fileName(given string) string {
	if at := strings.LastIndexAny(given, `/\`); at >= 0 {
		given = given[at+1:]
	}
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, given)
	name = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(name), "."))
	if len(name) <= maxFileName {
		return name
	}
	extension := path.Ext(name)
	if len(extension) > 16 {
		extension = ""
	}
	stem := name[:maxFileName-len(extension)]
	// A character is not cut in two.
	for !utf8.ValidString(stem) {
		stem = stem[:len(stem)-1]
	}
	return strings.TrimSpace(stem) + extension
}

// mediaType returns what the header says that an uploaded file is, without
// its parameters, or application/octet-stream if it says nothing usable.
func mediaType(header string) string {
	said, _, err := mime.ParseMediaType(header)
	if err != nil || said == "" || len(said) > 127 || !strings.Contains(said, "/") {
		return "application/octet-stream"
	}
	return said
}

// withAttachment passes on the requests of callers that may do the act on
// the session of the attachment named by the path value attachment.
func (a *api) withAttachment(do act, next handler) http.Handler {
	return handler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r, "attachment")
		if err != nil {
			return err
		}
		attachment, err := a.store.Attachment(r.Context(), id)
		if err != nil {
			return err
		}
		session, err := a.store.Session(r.Context(), attachment.SessionID)
		if err != nil {
			return err
		}
		if err := a.mayWith(r, session, attachment.CreatedBy, do); err != nil {
			return err
		}
		return next(w, r)
	})
}

// attached returns the turn as the answer to its message: with the
// attachments that the message carries, under the names that it gave them.
func attached(turn store.Turn, attachments []store.Attachment) any {
	return struct {
		store.Turn
		Attachments []store.Attachment `json:"attachments,omitempty"`
	}{turn, attachments}
}
