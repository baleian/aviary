package storetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// attachment records a file of the name that was uploaded for a message of
// the session.
func (s supervisor) attachment(session uuid.UUID, name string) store.Attachment {
	s.t.Helper()
	if err := s.ExpectAttachments(ctx, session); err != nil {
		s.t.Fatalf("ExpectAttachments: %v", err)
	}
	recorded, err := s.CreateAttachment(ctx, store.Attachment{
		ID: uuid.New(), SessionID: session, Name: name, MediaType: "text/plain", Size: int64(len(name)),
	})
	if err != nil {
		s.t.Fatalf("CreateAttachment: %v", err)
	}
	return recorded
}

// names returns the names of the attachments.
func names(attachments []store.Attachment) []string {
	names := []string{}
	for _, attachment := range attachments {
		names = append(names, attachment.Name)
	}
	return names
}

// A file that is uploaded for a message of a session is recorded with what
// the uploader said of it, and no message carries it yet. A session that has
// ended takes none.
func (suite suite) testAttachments(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	user, err := st.UserOf(ctx, "subject-of-an-uploader", "uploader@example.com", "Uploader")
	if err != nil {
		t.Fatal(err)
	}
	uploader := user.ID
	if err := st.ExpectAttachments(ctx, session); err != nil {
		t.Fatal(err)
	}
	file := store.Attachment{
		ID: uuid.New(), SessionID: session, Name: "보고서 2026.pdf", MediaType: "application/pdf", Size: 5 << 30, CreatedBy: uploader,
		// A file is no attachment of a turn before a message carries it.
		TurnID: uuid.New(),
	}
	recorded, err := st.CreateAttachment(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	read, err := st.Attachment(ctx, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	for what, got := range map[string]store.Attachment{"CreateAttachment": recorded, "Attachment": read} {
		if got.ID != file.ID || got.SessionID != session || got.TurnID != uuid.Nil || got.Name != file.Name ||
			got.MediaType != file.MediaType || got.Size != file.Size || got.CreatedBy != uploader || !recent(&got.CreatedAt) {
			t.Errorf("%s = %+v, want %+v of no turn, created now", what, got, file)
		}
	}
	// A request that was not authenticated uploads for nobody.
	if anonymous := st.attachment(session, "notes.txt"); anonymous.CreatedBy != uuid.Nil {
		t.Errorf("attachment of an unauthenticated request was created by %s", anonymous.CreatedBy)
	}

	if _, err := st.Attachment(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Attachment of an unknown ID: %v, want ErrNotFound", err)
	}
	unknown := uuid.New()
	if err := st.ExpectAttachments(ctx, unknown); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ExpectAttachments of an unknown session: %v, want ErrNotFound", err)
	}
	if _, err := st.CreateAttachment(ctx, store.Attachment{ID: uuid.New(), SessionID: unknown, Name: "a", MediaType: "text/plain"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CreateAttachment for an unknown session: %v, want ErrNotFound", err)
	}
	if _, err := st.EndSession(ctx, session, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.ExpectAttachments(ctx, session); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("ExpectAttachments of an ended session: %v, want ErrSessionEnded", err)
	}
	late := uuid.New()
	if _, err := st.CreateAttachment(ctx, store.Attachment{ID: late, SessionID: session, Name: "a", MediaType: "text/plain"}); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("CreateAttachment for an ended session: %v, want ErrSessionEnded", err)
	}
	if _, err := st.Attachment(ctx, late); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("attachment that an ended session refused: %v, want ErrNotFound", err)
	}
}

// A message carries its attachments in the order in which it names them, and
// those with the name of an earlier one get another name.
func (suite suite) testTurnAttachments(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	capture, report, again := st.attachment(session, "image.png"), st.attachment(session, "report.pdf"), st.attachment(session, "image.png")
	kept := st.attachment(session, "kept.txt")
	turn, _, err := st.CreateTurn(ctx, session, uuid.Nil, "look", []uuid.UUID{again.ID, report.ID, capture.ID}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	st.end(turn.ID, true, store.Failure{})
	plain, _, err := st.CreateTurn(ctx, session, uuid.Nil, "and now", nil, 0, 50)
	if err != nil {
		t.Fatal(err)
	}

	carried, err := st.TurnAttachments(ctx, []uuid.UUID{plain.ID, turn.ID, uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if len(carried) != 1 {
		t.Errorf("TurnAttachments = %v, want only the turn whose message carries attachments", carried)
	}
	got := carried[turn.ID]
	if want := []string{"image.png", "report.pdf", "image (2).png"}; !slices.Equal(names(got), want) {
		t.Fatalf("names of the turn's attachments = %q, want %q", names(got), want)
	}
	for i, id := range []uuid.UUID{again.ID, report.ID, capture.ID} {
		if got[i].ID != id || got[i].TurnID != turn.ID || got[i].SessionID != session {
			t.Errorf("attachment %d of the turn = %+v, want %s of the turn", i, got[i], id)
		}
	}
	if read, err := st.Attachment(ctx, capture.ID); err != nil || read.TurnID != turn.ID || read.Name != "image (2).png" {
		t.Errorf("Attachment = %+v, %v; want it of the turn, with its name in the message", read, err)
	}
	if read, err := st.Attachment(ctx, kept.ID); err != nil || read.TurnID != uuid.Nil {
		t.Errorf("attachment that no message named = %+v, %v; want it of no turn", read, err)
	}
	if none, err := st.TurnAttachments(ctx, nil); err != nil || len(none) != 0 {
		t.Errorf("TurnAttachments of no turns = %v, %v", none, err)
	}
}

// A message is refused if one of its attachments is not of its session, is
// carried by another message or is named twice. Nothing changes then: the
// session takes the next message, with the attachments that were refused.
func (suite suite) testUnknownAttachments(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session, other := st.session(), st.session()
	mine, sent, foreign := st.attachment(session, "mine.txt"), st.attachment(session, "sent.txt"), st.attachment(other, "foreign.txt")
	first, _, err := st.CreateTurn(ctx, session, uuid.Nil, "one", []uuid.UUID{sent.ID}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	st.end(first.ID, true, store.Failure{})

	for what, attachments := range map[string][]uuid.UUID{
		"an unknown attachment":                      {mine.ID, uuid.New()},
		"an attachment of another session":           {mine.ID, foreign.ID},
		"an attachment that another message carries": {sent.ID, mine.ID},
		"an attachment that is named twice":          {mine.ID, mine.ID},
	} {
		if _, _, err := st.CreateTurn(ctx, session, uuid.Nil, "two", attachments, 0, 50); !errors.Is(err, store.ErrUnknownAttachment) {
			t.Errorf("CreateTurn with %s: %v, want ErrUnknownAttachment", what, err)
		}
	}
	if turns, err := st.Turns(ctx, session, 100, 100); err != nil || len(turns) != 1 {
		t.Errorf("session has %d turns after refused messages, %v; want its one turn", len(turns), err)
	}
	for _, kept := range []store.Attachment{mine, foreign} {
		if read, err := st.Attachment(ctx, kept.ID); err != nil || read.TurnID != uuid.Nil {
			t.Errorf("attachment of a refused message = %+v, %v; want it of no turn", read, err)
		}
	}
	second, _, err := st.CreateTurn(ctx, session, uuid.Nil, "two", []uuid.UUID{mine.ID}, 0, 50)
	if err != nil {
		t.Fatalf("CreateTurn after refused messages: %v", err)
	}
	if carried, err := st.TurnAttachments(ctx, []uuid.UUID{first.ID, second.ID}); err != nil ||
		!slices.Equal(names(carried[first.ID]), []string{"sent.txt"}) || !slices.Equal(names(carried[second.ID]), []string{"mine.txt"}) {
		t.Errorf("TurnAttachments = %v, %v; want one attachment of each turn", carried, err)
	}
	// A message that is refused for another reason carries nothing either.
	late := st.attachment(session, "late.txt")
	if _, _, err := st.CreateTurn(ctx, session, uuid.Nil, "three", []uuid.UUID{late.ID}, 0, 50); !errors.Is(err, store.ErrSessionBusy) {
		t.Fatalf("CreateTurn of a busy session: %v, want ErrSessionBusy", err)
	}
	if read, err := st.Attachment(ctx, late.ID); err != nil || read.TurnID != uuid.Nil {
		t.Errorf("attachment of a message to a busy session = %+v, %v; want it of no turn", read, err)
	}
}

// The attachments that no message carries are listed by their age, to be
// deleted. One that a message carries is not deleted.
func (suite suite) testUnsentAttachments(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	old, sent, newer := st.attachment(session, "old.txt"), st.attachment(session, "sent.txt"), st.attachment(session, "newer.txt")
	if _, _, err := st.CreateTurn(ctx, session, uuid.Nil, "look", []uuid.UUID{sent.ID}, 0, 50); err != nil {
		t.Fatal(err)
	}
	// The stores of the other tests have unsent attachments too.
	mine := func(before time.Time, limit int) []uuid.UUID {
		t.Helper()
		unsent, err := st.UnsentAttachments(ctx, before, limit)
		if err != nil {
			t.Fatal(err)
		}
		ids := []uuid.UUID{}
		for _, attachment := range unsent {
			if attachment.SessionID == session {
				ids = append(ids, attachment.ID)
			}
		}
		return ids
	}
	soon := time.Now().Add(time.Hour)
	if got := mine(soon, 1000); !slices.Equal(got, []uuid.UUID{old.ID, newer.ID}) {
		t.Errorf("UnsentAttachments = %v, want %v, the oldest first", got, []uuid.UUID{old.ID, newer.ID})
	}
	if got := mine(time.Now().Add(-time.Hour), 1000); len(got) != 0 {
		t.Errorf("UnsentAttachments before an hour ago = %v, want none", got)
	}
	if all, err := st.UnsentAttachments(ctx, soon, 1); err != nil || len(all) != 1 {
		t.Errorf("UnsentAttachments with a limit of 1 returned %d, %v", len(all), err)
	}

	if deleted, err := st.DeleteAttachment(ctx, old.ID); err != nil || !deleted {
		t.Errorf("DeleteAttachment of an unsent attachment = %t, %v; want true", deleted, err)
	}
	if _, err := st.Attachment(ctx, old.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted attachment: %v, want ErrNotFound", err)
	}
	if deleted, err := st.DeleteAttachment(ctx, sent.ID); err != nil || deleted {
		t.Errorf("DeleteAttachment of an attachment that a message carries = %t, %v; want false", deleted, err)
	}
	if read, err := st.Attachment(ctx, sent.ID); err != nil || read.TurnID == uuid.Nil {
		t.Errorf("attachment that a message carries = %+v, %v; want it kept", read, err)
	}
	if deleted, err := st.DeleteAttachment(ctx, uuid.New()); err != nil || deleted {
		t.Errorf("DeleteAttachment of an unknown ID = %t, %v; want false", deleted, err)
	}
	if got := mine(soon, 1000); !slices.Equal(got, []uuid.UUID{newer.ID}) {
		t.Errorf("UnsentAttachments after deleting one = %v, want %v", got, []uuid.UUID{newer.ID})
	}
}

// A session whose attachments the object storage may have is listed once it
// has ended, until the object storage has none of them.
func (suite suite) testLeftAttachments(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	expected, plain := st.session(), st.session()
	st.attachment(expected, "notes.txt")
	// The stores of the other tests have left attachments too.
	left := func(before time.Time) bool {
		t.Helper()
		sessions, err := st.LeftAttachments(ctx, before, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(sessions, plain) {
			t.Errorf("LeftAttachments lists a session that never had attachments")
		}
		return slices.Contains(sessions, expected)
	}
	soon := time.Now().Add(time.Hour)
	if left(soon) {
		t.Error("LeftAttachments lists a session that has not ended")
	}
	// The object storage keeps the attachments of a session that has not
	// ended.
	if err := st.ForgetAttachments(ctx, expected); err != nil {
		t.Fatal(err)
	}
	for _, session := range []uuid.UUID{expected, plain} {
		if _, err := st.EndSession(ctx, session, ""); err != nil {
			t.Fatal(err)
		}
	}
	if !left(soon) {
		t.Error("LeftAttachments does not list the session after it has ended")
	}
	if left(time.Now().Add(-time.Hour)) {
		t.Error("LeftAttachments lists a session that ended later than an hour ago")
	}
	if all, err := st.LeftAttachments(ctx, soon, 0); err != nil || len(all) != 0 {
		t.Errorf("LeftAttachments with a limit of 0 = %v, %v", all, err)
	}
	if err := st.ForgetAttachments(ctx, expected); err != nil {
		t.Fatal(err)
	}
	if left(soon) {
		t.Error("LeftAttachments lists the session after its attachments were forgotten")
	}
	if err := st.ForgetAttachments(ctx, uuid.New()); err != nil {
		t.Errorf("ForgetAttachments of an unknown session: %v", err)
	}
}

// recent reports whether at is a time of this test.
func recent(at *time.Time) bool {
	return at != nil && time.Since(*at) < time.Minute && time.Until(*at) < time.Minute
}
