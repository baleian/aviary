package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox"
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

// AttachmentLimits returns the largest size of a file that a message
// carries, in bytes, and the largest number of files of one message. Both are
// 0 if the installation takes no attachments.
func (d *Driver) AttachmentLimits() (size int64, files int) {
	if d.config.Depot == nil || d.config.MaxAttachments == 0 {
		return 0, 0
	}
	return d.config.MaxAttachmentBytes, d.config.MaxAttachments
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

// AttachmentPath returns where the agent finds a file that the message of a
// turn carries: the path below the attachments of the home, by the sequence
// number of the turn, so that the files of different messages do not replace
// each other.
func AttachmentPath(seq int, name string) string {
	return strconv.Itoa(seq) + "/" + name
}

// deliver writes the files that the message of the turn carries into the
// home of the sandbox, before the turn is given to the runner. It can be done
// again: a file replaces the one that is there. Its error is
// sandbox.ErrNoRoom if the home has no room for a file.
func (d *Driver) deliver(ctx context.Context, turn store.FollowedTurn, runner *sandbox.Runner) error {
	carried, err := d.store.TurnAttachments(ctx, []uuid.UUID{turn.ID})
	if err != nil {
		return fmt.Errorf("read attachments of turn: %w", err)
	}
	for _, attachment := range carried[turn.ID] {
		if d.config.Depot == nil {
			return errors.New("the installation has no depot")
		}
		file, size, err := d.config.Depot.Get(ctx, attachmentKey(turn.SessionID, attachment.ID))
		if err != nil {
			return fmt.Errorf("read attachment %s: %w", attachment.Name, err)
		}
		if size == attachment.Size {
			err = runner.PutFile(ctx, AttachmentPath(turn.Seq, attachment.Name), file, size)
		} else {
			err = fmt.Errorf("the depot has %d bytes of %d", size, attachment.Size)
		}
		file.Close()
		if err != nil {
			return fmt.Errorf("write attachment %s into the sandbox: %w", attachment.Name, err)
		}
	}
	return nil
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
