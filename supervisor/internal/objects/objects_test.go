package objects_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"aviary/supervisor/internal/objects"
	"aviary/supervisor/internal/objects/objectstest"
)

// put writes content as the object of key.
func put(t *testing.T, store *objects.Store, key, content string) {
	t.Helper()
	// Not a strings.Reader, which the client could read twice: the body of
	// an upload is read once.
	body := io.MultiReader(strings.NewReader(content))
	if err := store.Put(context.Background(), key, body, int64(len(content))); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

// An object is kept under the prefix of the location, and read back whole.
func TestObjectsAreKeptUnderTheLocation(t *testing.T) {
	t.Parallel()
	server := objectstest.New(t)
	store := server.Store(t, "s3://aviary/in/stallation")
	put(t, store, "attachments/s1/a1", "the file")
	if keys := server.Keys("aviary", ""); !slices.Equal(keys, []string{"in/stallation/attachments/s1/a1"}) {
		t.Errorf("keys = %v, want the key under the prefix of the location", keys)
	}
	object, size, err := store.Get(context.Background(), "attachments/s1/a1")
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	read, err := io.ReadAll(object)
	if err != nil || string(read) != "the file" || size != int64(len("the file")) {
		t.Errorf("get = %q of %d bytes, %v; want the file", read, size, err)
	}
	if _, _, err := store.Get(context.Background(), "attachments/s1/none"); !errors.Is(err, objects.ErrNotFound) {
		t.Errorf("get of a key without an object = %v, want ErrNotFound", err)
	}
	empty := server.Store(t, "s3://aviary/")
	put(t, empty, "top", "")
	if _, found := server.Object("aviary", "top"); !found {
		t.Errorf("an empty object at the root of the bucket is not kept: %v", server.Keys("aviary", ""))
	}
}

// Deleting a key deletes the keys under it too, and nothing else.
func TestDeleteTakesTheKeysUnderIt(t *testing.T) {
	t.Parallel()
	server := objectstest.New(t)
	store := server.Store(t, "s3://aviary/")
	for _, key := range []string{"attachments/s1/a1", "attachments/s1/a2", "attachments/s10/a3", "attachments/s1"} {
		put(t, store, key, key)
	}
	if err := store.Delete(context.Background(), "attachments/s1"); err != nil {
		t.Fatal(err)
	}
	if keys := server.Keys("aviary", ""); !slices.Equal(keys, []string{"attachments/s10/a3"}) {
		t.Errorf("keys after deleting attachments/s1 = %v, want only those of another session", keys)
	}
	if err := store.Delete(context.Background(), "attachments/none"); err != nil {
		t.Errorf("delete of a key without objects = %v, want no error", err)
	}
}

// A store without room says so.
func TestAFullStoreSaysSo(t *testing.T) {
	t.Parallel()
	server := objectstest.New(t)
	store := server.Store(t, "s3://aviary/")
	server.Fill()
	if err := store.Put(context.Background(), "a", strings.NewReader("a"), 1); !errors.Is(err, objects.ErrFull) {
		t.Errorf("put to a full store = %v, want ErrFull", err)
	}
}

func TestLocations(t *testing.T) {
	t.Parallel()
	for _, location := range []string{"", "aviary", "gs://aviary/", "s3://", "s3:///prefix/", "s3://key:secret@aviary/", "s3://aviary/?a=b"} {
		if _, err := objects.New(aws.Config{Region: "us-east-1"}, location); err == nil {
			t.Errorf("location %q is taken, want it refused", location)
		}
	}
}
