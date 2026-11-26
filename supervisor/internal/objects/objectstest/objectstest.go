// Package objectstest serves, from memory, the requests of the S3 API that
// package objects makes, for tests.
package objectstest

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"aviary/supervisor/internal/objects"
)

// Server is an S3 API of buckets in memory, addressed in the path.
type Server struct {
	*httptest.Server

	mu sync.Mutex
	// objects are by bucket and key, as "bucket/key".
	objects map[string][]byte
	full    bool
}

// New starts a server that the test stops.
func New(t testing.TB) *Server {
	s := &Server{objects: map[string][]byte{}}
	s.Server = httptest.NewServer(s)
	t.Cleanup(s.Close)
	return s
}

// Store returns a store of the server at location, such as s3://bucket/.
func (s *Server) Store(t testing.TB, location string) *objects.Store {
	t.Helper()
	store, err := objects.New(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, location, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(s.URL)
		o.UsePathStyle = true
		o.RetryMaxAttempts = 1
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// Keys returns the keys of the bucket that begin with prefix, in order.
func (s *Server) Keys(bucket, prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for name := range s.objects {
		if key, found := strings.CutPrefix(name, bucket+"/"); found && strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// Object returns the object of the key in the bucket, if there is one.
func (s *Server) Object(bucket, key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, found := s.objects[bucket+"/"+key]
	return object, found
}

// Fill makes the server answer every write as a store without room does.
func (s *Server) Fill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.full = true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodPut && key != "":
		if s.full {
			_, _ = io.Copy(io.Discard, r.Body)
			fail(w, http.StatusInsufficientStorage, "XMinioStorageFull")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || int64(len(body)) != r.ContentLength {
			fail(w, http.StatusBadRequest, "IncompleteBody")
			return
		}
		s.objects[bucket+"/"+key] = body
		w.Header().Set("ETag", `"0"`)
	case r.Method == http.MethodGet && key != "":
		object, found := s.objects[bucket+"/"+key]
		if !found {
			fail(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(object)))
		_, _ = w.Write(object)
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		type contents struct{ Key string }
		listed := struct {
			XMLName     xml.Name `xml:"ListBucketResult"`
			Name        string
			KeyCount    int
			IsTruncated bool
			Contents    []contents
		}{Name: bucket}
		prefix := r.URL.Query().Get("prefix")
		for name := range s.objects {
			if key, found := strings.CutPrefix(name, bucket+"/"); found && strings.HasPrefix(key, prefix) {
				listed.Contents = append(listed.Contents, contents{key})
			}
		}
		slices.SortFunc(listed.Contents, func(a, b contents) int { return strings.Compare(a.Key, b.Key) })
		listed.KeyCount = len(listed.Contents)
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(listed)
	case r.Method == http.MethodDelete && key != "":
		delete(s.objects, bucket+"/"+key)
		w.WriteHeader(http.StatusNoContent)
	default:
		fail(w, http.StatusNotImplemented, "NotImplemented")
	}
}

// fail answers with the error of an S3 API.
func fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(struct {
		XMLName xml.Name `xml:"Error"`
		Code    string
	}{Code: code})
}
