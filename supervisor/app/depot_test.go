package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"aviary/supervisor/internal/depot"
)

// servedDepot is a running depot, with a client of it.
type servedDepot struct {
	*depot.Client
	// metrics is the URL of its metrics.
	metrics string
	// stop cancels the depot's context, as SIGTERM does, and waits for it to
	// return.
	stop func()
}

// runDepot serves the objects of the directory as a depot until the test
// ends.
func runDepot(t *testing.T, dir string) *servedDepot {
	t.Helper()
	objects, err := depot.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	listen := func() net.Listener {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return listener
	}
	requests, scrapes := listen(), listen()
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- serveDepotOn(ctx, requests, scrapes, objects) }()
	served := &servedDepot{
		Client:  depot.At("http://" + requests.Addr().String()),
		metrics: "http://" + scrapes.Addr().String() + "/metrics",
	}
	served.stop = sync.OnceFunc(func() {
		stop()
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("serveDepotOn: %v", err)
			}
		case <-time.After(patience):
			t.Error("the depot did not stop")
		}
	})
	t.Cleanup(served.stop)
	return served
}

// metric returns the value of the depot's series.
func (d *servedDepot) metric(t *testing.T, series string) float64 {
	t.Helper()
	status, lines := scrape(t, d.metrics)
	if status != http.StatusOK {
		t.Fatalf("the metrics of the depot answered %d", status)
	}
	for _, line := range lines {
		if value, found := strings.CutPrefix(line, series+" "); found {
			number, err := strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatalf("invalid metrics line %q", line)
			}
			return number
		}
	}
	t.Fatalf("the metrics of the depot lack %s", series)
	return 0
}

// "depot" needs the directory of the objects, and neither a database nor a
// plugin.
func TestRunServesADepotWithoutADatabase(t *testing.T) {
	t.Setenv(databaseURL, "")
	t.Setenv(depotDirectory, "")
	build := &build{}
	err := Run(context.Background(), []string{"depot"}, build.plugins())
	if err == nil || !strings.Contains(err.Error(), depotDirectory) {
		t.Errorf("depot without a directory: %v, want an error naming the setting", err)
	}
	missing := filepath.Join(t.TempDir(), "objects")
	t.Setenv(depotDirectory, missing)
	err = Run(context.Background(), []string{"depot"}, build.plugins())
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("depot with a directory that does not exist: %v, want an error naming it", err)
	}
	if len(build.asked) != 0 {
		t.Errorf("plugin calls = %q, want none", build.asked)
	}
}

// A depot starts with every series at zero, counts the requests that it
// serves and the bytes of their objects, and reports the volume of its
// directory.
func TestDepotMetrics(t *testing.T) {
	t.Parallel()
	d := runDepot(t, t.TempDir())
	for _, series := range []string{
		`aviary_depot_request_duration_seconds_count{operation="write",outcome="done"}`,
		`aviary_depot_request_duration_seconds_count{operation="write",outcome="failed"}`,
		`aviary_depot_request_duration_seconds_count{operation="read",outcome="refused"}`,
		`aviary_depot_request_duration_seconds_count{operation="delete",outcome="done"}`,
		`aviary_depot_object_bytes_total{operation="write"}`,
		`aviary_depot_object_bytes_total{operation="read"}`,
	} {
		if value := d.metric(t, series); value != 0 {
			t.Errorf("%s = %v at the start, want 0", series, value)
		}
	}

	ctx := context.Background()
	if _, err := d.Put(ctx, "attachments/a-session/a-file", strings.NewReader("a file of 21 bytes...")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		object, _, err := d.Get(ctx, "attachments/a-session/a-file")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, object)
		object.Close()
	}
	if _, _, err := d.Get(ctx, "attachments/a-session/none"); !errors.Is(err, depot.ErrNotFound) {
		t.Fatalf("Get of a key without an object = %v, want ErrNotFound", err)
	}
	if err := d.Delete(ctx, "attachments/a-session"); err != nil {
		t.Fatal(err)
	}
	for series, expected := range map[string]float64{
		`aviary_depot_request_duration_seconds_count{operation="write",outcome="done"}`:   1,
		`aviary_depot_request_duration_seconds_count{operation="read",outcome="done"}`:    2,
		`aviary_depot_request_duration_seconds_count{operation="read",outcome="refused"}`: 1,
		`aviary_depot_request_duration_seconds_count{operation="delete",outcome="done"}`:  1,
		`aviary_depot_request_duration_seconds_count{operation="write",outcome="failed"}`: 0,
		`aviary_depot_object_bytes_total{operation="write"}`:                              21,
		`aviary_depot_object_bytes_total{operation="read"}`:                               42,
	} {
		if value := d.metric(t, series); value != expected {
			t.Errorf("%s = %v, want %v", series, value, expected)
		}
	}
	capacity, available := d.metric(t, "aviary_depot_volume_capacity_bytes"), d.metric(t, "aviary_depot_volume_available_bytes")
	if capacity <= 0 || available > capacity {
		t.Errorf("the volume has %v of %v bytes free", available, capacity)
	}
}

// A stopping depot takes no new requests once the handover is over, and lets
// a transfer that is under way finish.
func TestStoppingDepotFinishesItsTransfers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := runDepot(t, dir)
	half := bytes.Repeat([]byte("a"), 1<<20)
	body, sender := io.Pipe()
	type result struct {
		size int64
		err  error
	}
	put := make(chan result, 1)
	go func() {
		size, err := d.Put(context.Background(), "attachments/a-session/a-file", body)
		put <- result{size, err}
	}()
	if _, err := sender.Write(half); err != nil {
		t.Fatal(err)
	}
	eventually(t, patience, "the depot has not begun to write the object", func() bool {
		info, err := os.Stat(filepath.Join(dir, "attachments/a-session/a-file"))
		return err == nil && info.Size() > 0
	})

	stopped := make(chan struct{})
	go func() {
		d.stop()
		close(stopped)
	}()
	eventually(t, patience, "the stopping depot still takes requests", func() bool {
		_, _, err := d.Get(context.Background(), "attachments/a-session/none")
		return err != nil && !errors.Is(err, depot.ErrNotFound)
	})
	select {
	case <-stopped:
		t.Fatal("the depot stopped before its transfer finished")
	case written := <-put:
		t.Fatalf("Put returned %d, %v before its object was sent", written.size, written.err)
	default:
	}
	if _, err := sender.Write(half); err != nil {
		t.Fatal(err)
	}
	sender.Close()
	if written := <-put; written.err != nil || written.size != int64(2*len(half)) {
		t.Errorf("Put = %d, %v; want the %d bytes of the object", written.size, written.err, 2*len(half))
	}
	select {
	case <-stopped:
	case <-time.After(patience):
		t.Fatal("the depot did not stop after its transfer")
	}
	content, err := os.ReadFile(filepath.Join(dir, "attachments/a-session/a-file"))
	if err != nil || !slices.Equal(content, append(slices.Clone(half), half...)) {
		t.Errorf("the object has %d bytes, %v; want the %d that were sent", len(content), err, 2*len(half))
	}
}
