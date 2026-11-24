package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// A file that a message carries is uploaded before the message, and is an
// object of the depot until its session ends. The session records that the
// depot may have such objects before the first is written, so that none is
// left behind: what is written but not recorded is deleted with the rest
// when the session has ended.

var (
	// ErrNoAttachments means that the installation takes no attachments.
	ErrNoAttachments = errors.New("the installation takes no attachments")
	// ErrAttachmentTooLarge means that a file is larger than an attachment
	// may be.
	ErrAttachmentTooLarge = errors.New("attachment is too large")
	// ErrTooManyAttachments means that a message names more attachments than
	// one may carry.
	ErrTooManyAttachments = errors.New("too many attachments")
)

// attachmentKey returns the key of the attachment in the depot. The
// attachments of a session are under attachmentsOf(session).
func attachmentKey(session, attachment uuid.UUID) string {
	return attachmentsOf(session) + "/" + attachment.String()
}

func attachmentsOf(session uuid.UUID) string {
	return "attachments/" + session.String()
}

// Attach keeps the file for a message of the session to carry, and returns
// its record. by is the user who uploads it, or uuid.Nil if requests are not
// authenticated, and declared is the size that the uploader gave, or -1. It
// returns ErrNoAttachments if the installation takes none,
// ErrAttachmentTooLarge if the file is larger than MaxAttachmentBytes, and
// the errors of store.ExpectAttachments.
func (d *Driver) Attach(ctx context.Context, sessionID, by uuid.UUID, name, mediaType string, declared int64, file io.Reader) (store.Attachment, error) {
	if d.config.Depot == nil || d.config.MaxAttachments == 0 {
		return store.Attachment{}, ErrNoAttachments
	}
	// A file that says that it is too large is refused before it is read.
	if declared > d.config.MaxAttachmentBytes {
		return store.Attachment{}, ErrAttachmentTooLarge
	}
	if err := d.store.ExpectAttachments(ctx, sessionID); err != nil {
		return store.Attachment{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return store.Attachment{}, err
	}
	key := attachmentKey(sessionID, id)
	// One byte more than a file may have tells a file that is too large.
	size, err := d.config.Depot.Put(ctx, key, io.LimitReader(file, d.config.MaxAttachmentBytes+1))
	if err != nil {
		return store.Attachment{}, fmt.Errorf("write attachment: %w", err)
	}
	if size > d.config.MaxAttachmentBytes {
		err = ErrAttachmentTooLarge
	}
	var attachment store.Attachment
	if err == nil {
		attachment, err = d.store.CreateAttachment(ctx, store.Attachment{
			ID: id, SessionID: sessionID, Name: name, MediaType: mediaType, Size: size, CreatedBy: by,
		})
	}
	if err != nil {
		// The request may be over. What stays is deleted with the session's
		// other attachments.
		if failure := d.config.Depot.Delete(context.WithoutCancel(ctx), key); failure != nil {
			slog.ErrorContext(ctx, "delete attachment that was not recorded", "session", sessionID, "attachment", id, "error", failure)
		}
		return store.Attachment{}, err
	}
	return attachment, nil
}

// Attachment returns the attachment and its file, which the caller closes.
// It returns store.ErrNotFound if there is no such attachment, or if its
// session has ended: the file is kept only until then.
func (d *Driver) Attachment(ctx context.Context, id uuid.UUID) (store.Attachment, io.ReadCloser, error) {
	attachment, err := d.store.Attachment(ctx, id)
	if err != nil {
		return store.Attachment{}, nil, err
	}
	session, err := d.store.Session(ctx, attachment.SessionID)
	if err != nil {
		return store.Attachment{}, nil, err
	}
	if session.EndedAt != nil || d.config.Depot == nil {
		return store.Attachment{}, nil, store.ErrNotFound
	}
	file, size, err := d.config.Depot.Get(ctx, attachmentKey(attachment.SessionID, id))
	if err != nil {
		return store.Attachment{}, nil, fmt.Errorf("read attachment: %w", err)
	}
	if size != attachment.Size {
		file.Close()
		return store.Attachment{}, nil, fmt.Errorf("the depot has %d bytes of an attachment of %d", size, attachment.Size)
	}
	return attachment, file, nil
}

// discardAttachments deletes the attachments that no message has carried
// for UnsentFor, and those that sessions have left when they ended. The
// latter are deleted only UnsentFor after the session ended, when no upload
// that began before can still be writing.
func (d *Driver) discardAttachments(ctx context.Context) {
	if d.config.Depot == nil {
		return
	}
	before := time.Now().Add(-d.config.UnsentFor)
	unsent, err := d.store.UnsentAttachments(ctx, before, leftAtOnce)
	if err != nil {
		slog.ErrorContext(ctx, "list unsent attachments", "error", err)
		return
	}
	for _, attachment := range unsent {
		// The record goes first: a message may carry the attachment by now,
		// and then it stays.
		deleted, err := d.store.DeleteAttachment(ctx, attachment.ID)
		if err != nil || !deleted {
			if err != nil {
				slog.ErrorContext(ctx, "delete unsent attachment", "attachment", attachment.ID, "error", err)
			}
			continue
		}
		if err := d.config.Depot.Delete(ctx, attachmentKey(attachment.SessionID, attachment.ID)); err != nil {
			slog.ErrorContext(ctx, "delete unsent attachment", "session", attachment.SessionID, "attachment", attachment.ID, "error", err)
		}
	}
	left, err := d.store.LeftAttachments(ctx, before, leftAtOnce)
	if err != nil {
		slog.ErrorContext(ctx, "list attachments of ended sessions", "error", err)
		return
	}
	for _, session := range left {
		if err := d.config.Depot.Delete(ctx, attachmentsOf(session)); err != nil {
			slog.ErrorContext(ctx, "delete attachments of ended session", "session", session, "error", err)
			continue
		}
		if err := d.store.ForgetAttachments(ctx, session); err != nil {
			slog.ErrorContext(ctx, "forget attachments of ended session", "session", session, "error", err)
		}
	}
}
