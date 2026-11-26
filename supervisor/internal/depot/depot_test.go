package depot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// serve serves the objects of the directory as a depot until the test ends,
// and returns a client of it.
func serve(t *testing.T, dir string, served func(method, outcome string, took time.Duration, moved int64)) *Client {
	t.Helper()
	objects, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler(objects, served))
	t.Cleanup(server.Close)
	return At(server.URL)
}

// read returns the object of the key as the depot serves it.
func read(t *testing.T, from *Client, key string) (string, error) {
	t.Helper()
	object, size, err := from.Get(context.Background(), key)
	if err != nil {
		return "", err
	}
	defer object.Close()
	content, err := io.ReadAll(object)
	if err == nil && int64(len(content)) != size {
		t.Errorf("Get(%s) returned the size %d for an object of %d bytes", key, size, len(content))
	}
	return string(content), err
}

// write writes the objects, each with its key as its content.
func write(t *testing.T, to *Client, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if size, err := to.Put(context.Background(), key, strings.NewReader(key)); err != nil || size != int64(len(key)) {
			t.Fatalf("Put(%s) = %d, %v", key, size, err)
		}
	}
}

// files returns the paths of the files and directories under dir.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			// A depot deleted it meanwhile.
			return nil
		}
		if path != dir {
			paths = append(paths, strings.TrimPrefix(path, dir+"/"))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// zeros reads as zeros without end.
type zeros struct{}

func (zeros) Read(into []byte) (int, error) {
	clear(into)
	return len(into), nil
}

// failing fails every read.
type failing struct{ error }

func (f failing) Read([]byte) (int, error) { return 0, f.error }

// cut returns an object whose sender fails after a part of it that is large
// enough to reach the depot.
func cut() io.Reader {
	return io.MultiReader(io.LimitReader(zeros{}, 1<<20), failing{errors.New("the sender is gone")})
}

// The depots of a directory serve the same objects: what one wrote, another
// reads and deletes.
func TestDepotsOfADirectoryServeTheSameObjects(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	one, other := serve(t, dir, nil), serve(t, dir, nil)
	size, err := one.Put(context.Background(), "attachments/a-session/a-file", strings.NewReader("the file of a message"))
	if err != nil || size != 21 {
		t.Fatalf("Put = %d, %v; want the 21 bytes of the object", size, err)
	}
	if content, err := read(t, other, "attachments/a-session/a-file"); err != nil || content != "the file of a message" {
		t.Errorf("the other depot read %q, %v", content, err)
	}
	if err := other.Delete(context.Background(), "attachments/a-session/a-file"); err != nil {
		t.Fatal(err)
	}
	if _, err := read(t, one, "attachments/a-session/a-file"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get of a deleted object = %v, want ErrNotFound", err)
	}
}

// A key is written once: another object is refused, even one that is large
// and still being sent, and the first stays as it was.
func TestAKeyIsWrittenOnce(t *testing.T) {
	t.Parallel()
	depot := serve(t, t.TempDir(), nil)
	write(t, depot, "attachments/a-session/a-file")
	for what, body := range map[string]io.Reader{
		"a small object": strings.NewReader("another"),
		"a large object": io.LimitReader(zeros{}, 64<<20),
	} {
		if _, err := depot.Put(context.Background(), "attachments/a-session/a-file", body); !errors.Is(err, ErrExists) {
			t.Errorf("Put of %s under a key that has one = %v, want ErrExists", what, err)
		}
	}
	if content, err := read(t, depot, "attachments/a-session/a-file"); err != nil || content != "attachments/a-session/a-file" {
		t.Errorf("the object is now %q, %v", content, err)
	}
}

// An object that its sender did not send whole is not kept, and its key can
// be written again.
func TestAnObjectThatWasNotSentWholeIsNotKept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	depot := serve(t, dir, nil)
	if _, err := depot.Put(context.Background(), "attachments/a-session/a-file", cut()); err == nil || !strings.Contains(err.Error(), "the sender is gone") {
		t.Fatalf("Put of an object whose sender fails = %v", err)
	}
	// The depot learns that the sender is gone after the sender does.
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Equal(files(t, dir), []string{"attachments", "attachments/a-session"}) {
		if time.Now().After(deadline) {
			t.Fatalf("the directory still has %q", files(t, dir))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := read(t, depot, "attachments/a-session/a-file"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
	write(t, depot, "attachments/a-session/a-file")
}

// Deleting a key deletes its object and the objects under it, with their
// directories, and no other.
func TestDeleteRemovesTheObjectsUnderAKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	depot := serve(t, dir, nil)
	write(t, depot,
		"attachments/session-1/a", "attachments/session-1/b", "attachments/session-1/of/a-turn",
		"attachments/session-10/a", "attachments/session-2/a", "other/session-1/a",
	)
	if err := depot.Delete(context.Background(), "attachments/session-1"); err != nil {
		t.Fatal(err)
	}
	left := []string{
		"attachments", "attachments/session-10", "attachments/session-10/a", "attachments/session-2", "attachments/session-2/a",
		"other", "other/session-1", "other/session-1/a",
	}
	if now := files(t, dir); !slices.Equal(now, left) {
		t.Errorf("the directory has %q, want %q", now, left)
	}
	// A single object, and keys without objects.
	for _, key := range []string{"attachments/session-2/a", "attachments/session-2/a", "attachments/session-1", "skills"} {
		if err := depot.Delete(context.Background(), key); err != nil {
			t.Errorf("Delete(%s) = %v", key, err)
		}
	}
	if _, err := read(t, depot, "attachments/session-2/a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get of a deleted object = %v, want ErrNotFound", err)
	}
	if content, err := read(t, depot, "attachments/session-10/a"); err != nil || content != "attachments/session-10/a" {
		t.Errorf("an object under another key is now %q, %v", content, err)
	}
}

// A key that only has objects under it has no object of its own.
func TestAKeyAboveObjectsHasNone(t *testing.T) {
	t.Parallel()
	depot := serve(t, t.TempDir(), nil)
	write(t, depot, "attachments/a-session/a-file")
	for _, key := range []string{"attachments/a-session", "attachments/a-session/a-file/below", "attachments/another"} {
		if _, err := read(t, depot, key); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%s) = %v, want ErrNotFound", key, err)
		}
	}
}

// A key is a path of plain names. The client sends no other, and a depot
// that gets one refuses it, so that no request reaches a file outside the
// objects.
func TestKeysArePathsOfPlainNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := Open(filepath.Join(dir, "objects")); err == nil {
		t.Fatal("Open of a directory that does not exist succeeded")
	}
	if err := os.Mkdir(filepath.Join(dir, "objects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside"), []byte("not an object"), 0o600); err != nil {
		t.Fatal(err)
	}
	objects, err := Open(filepath.Join(dir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler(objects, nil))
	defer server.Close()
	depot := At(server.URL)

	invalid := []string{
		"", "/", "/a", "a/", "a//b", ".", "..", "../outside", "a/../../outside", "a/./b", "a b", `a\b`, "a?b", "a%2Fb", "é",
		strings.Repeat("a", maxName+1), strings.Repeat("a/", maxKey/2) + "a",
	}
	for _, key := range invalid {
		if _, err := objects.Put(key, strings.NewReader("an object")); !errors.Is(err, ErrKey) {
			t.Errorf("Directory.Put(%q) = %v, want ErrKey", key, err)
		}
		if _, _, err := objects.Get(key); !errors.Is(err, ErrKey) {
			t.Errorf("Directory.Get(%q) = %v, want ErrKey", key, err)
		}
		if err := objects.Delete(key); !errors.Is(err, ErrKey) {
			t.Errorf("Directory.Delete(%q) = %v, want ErrKey", key, err)
		}
		if _, err := depot.Put(context.Background(), key, strings.NewReader("an object")); !errors.Is(err, ErrKey) {
			t.Errorf("Client.Put(%q) = %v, want ErrKey", key, err)
		}
		if _, _, err := depot.Get(context.Background(), key); !errors.Is(err, ErrKey) {
			t.Errorf("Client.Get(%q) = %v, want ErrKey", key, err)
		}
		if err := depot.Delete(context.Background(), key); !errors.Is(err, ErrKey) {
			t.Errorf("Client.Delete(%q) = %v, want ErrKey", key, err)
		}
	}
	// Requests that the client would not send.
	for _, path := range []string{"/objects/a%20b", "/objects/a/b%5Cc", "/objects/a/%C3%A9"} {
		for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
			request, err := http.NewRequest(method, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			answer, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			answer.Body.Close()
			if answer.StatusCode != http.StatusBadRequest {
				t.Errorf("%s %s = %d, want 400", method, path, answer.StatusCode)
			}
		}
	}
	if now := files(t, dir); !slices.Equal(now, []string{"objects", "outside"}) {
		t.Errorf("the directory has %q, want nothing new", now)
	}
	for _, key := range []string{"a", "a.b/c_d/e-f", strings.Repeat("b", maxName), strings.Repeat("c/", maxKey/2-1) + "cc"} {
		if _, err := depot.Put(context.Background(), key, strings.NewReader("an object")); err != nil {
			t.Errorf("Put(%q) = %v", key, err)
		}
	}
}

// A depot says of each request how it ended and how many bytes of the object
// it wrote or read.
func TestHandlerReportsEachRequest(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var reported []string
	depot := serve(t, t.TempDir(), func(method, outcome string, took time.Duration, moved int64) {
		mu.Lock()
		defer mu.Unlock()
		if took <= 0 {
			t.Errorf("%s took %s", method, took)
		}
		reported = append(reported, fmt.Sprintf("%s %s %d", method, outcome, moved))
	})
	ctx := context.Background()
	write(t, depot, "attachments/a-session/a-file")
	depot.Put(ctx, "attachments/a-session/a-file", strings.NewReader("another"))
	read(t, depot, "attachments/a-session/a-file")
	read(t, depot, "attachments/a-session/none")
	depot.Delete(ctx, "attachments/a-session")
	depot.Put(ctx, "attachments/a-session/cut", cut())
	expected := []string{
		"PUT done 28", "PUT refused 0", "GET done 28", "GET refused 0", "DELETE done 0", "PUT refused 0",
	}
	// The depot learns that the sender of the last object is gone after the
	// sender does.
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		now := slices.Clone(reported)
		mu.Unlock()
		if slices.Equal(now, expected) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the depot reported %q, want %q", now, expected)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A volume without room for an object is told apart from other failures, by
// the depot and by its client.
func TestNoRoomIsToldApart(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err    error
		status int
	}{
		{&fs.PathError{Op: "write", Path: "a", Err: syscall.ENOSPC}, http.StatusInsufficientStorage},
		{&fs.PathError{Op: "close", Path: "a", Err: syscall.EDQUOT}, http.StatusInsufficientStorage},
		{&fs.PathError{Op: "write", Path: "a", Err: syscall.EIO}, http.StatusInternalServerError},
	} {
		if status := statusOf(full(test.err)); status != test.status {
			t.Errorf("a depot answers %d to %v, want %d", status, test.err, test.status)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusInternalServerError
		if r.URL.Path == "/objects/full" {
			status = http.StatusInsufficientStorage
		}
		http.Error(w, "the volume said more", status)
	}))
	defer server.Close()
	depot := At(server.URL)
	if _, err := depot.Put(context.Background(), "full", strings.NewReader("an object")); !errors.Is(err, ErrFull) {
		t.Errorf("Put to a depot that answers 507 = %v, want ErrFull", err)
	}
	_, err := depot.Put(context.Background(), "broken", strings.NewReader("an object"))
	if err == nil || errors.Is(err, ErrFull) || errors.Is(err, ErrExists) || errors.Is(err, ErrNotFound) {
		t.Errorf("Put to a depot that answers 500 = %v, want an error of its own", err)
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "the volume said more") {
		t.Errorf("Put to a depot that answers 500 = %v, want the status and what the depot said", err)
	}
}

// Reading an object that the depot does not send whole fails.
func TestAnObjectThatEndsEarlyFailsItsReader(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "21")
		w.Write([]byte("the file of"))
	}))
	defer server.Close()
	object, size, err := At(server.URL).Get(context.Background(), "attachments/a-session/a-file")
	if err != nil || size != 21 {
		t.Fatalf("Get = %d, %v", size, err)
	}
	defer object.Close()
	if content, err := io.ReadAll(object); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("reading an object that ends early = %q, %v; want io.ErrUnexpectedEOF", content, err)
	}
}

// A depot does not start on a directory that it cannot write, rather than
// fail its first object.
func TestOpenNeedsADirectoryThatItCanWrite(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("root can write every directory")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil {
		t.Fatal("Open of a directory that cannot be written succeeded")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("Open = %v, want an error that names the directory", err)
	}
}

// The volume of a directory has a size, and no more free than that.
func TestVolume(t *testing.T) {
	t.Parallel()
	objects, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	capacity, available, err := objects.Volume()
	if err != nil || capacity == 0 || available > capacity {
		t.Errorf("Volume = %d, %d, %v", capacity, available, err)
	}
}
