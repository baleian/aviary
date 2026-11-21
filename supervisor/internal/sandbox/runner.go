package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// eventLimit is the maximum length of a line of an event stream.
	eventLimit = 4 << 20
	// told is the timeout of requests to the runner, except event streams.
	told = 30 * time.Second
)

// ErrBusy is returned by Start when the runner is running another turn.
var ErrBusy = errors.New("runner is busy with another turn")

// Runner is a client of the runner in a sandbox. Its responses come from the
// sandbox, which the agent controls, so they are not trusted.
type Runner struct {
	address string
	http    *http.Client
	// quiet is how long an event stream may stay silent before it is
	// abandoned.
	quiet time.Duration
}

// stream is the HTTP client of all Runners. It has no timeout, since an
// event stream lasts as long as its turn.
var stream = &http.Client{}

// RunnerAt returns a client of the runner at address.
func RunnerAt(address string) *Runner {
	return &Runner{address: address, http: stream, quiet: time.Minute}
}

// Address returns the runner's base URL.
func (r *Runner) Address() string {
	return r.address
}

// Turn is a turn as the runner receives it.
type Turn struct {
	Text string `json:"text"`
	// Instructions are given to the agent before every turn.
	Instructions string `json:"instructions"`
	// Approval is "ask" if the harness asks a person before it acts.
	Approval string `json:"approval"`
	// Environment holds environment variables of the harness, such as the
	// agent's model. A harness has both from its start, so the runner starts
	// a new one for a turn in which they differ.
	Environment map[string]string `json:"environment"`
}

// Event is an event of a turn as reported by the runner.
type Event struct {
	// Seq numbers the events of a turn from 1.
	Seq  uint64
	Name string
	// Data is valid JSON.
	Data json.RawMessage
}

// Start gives the turn to the runner. It is idempotent, and returns ErrBusy
// if the runner is running another turn.
func (r *Runner) Start(ctx context.Context, turn uuid.UUID, start Turn) error {
	err := r.tell(ctx, http.MethodPut, "/turns/"+turn.String(), start)
	var refusal *Refusal
	if errors.As(err, &refusal) && refusal.Status == http.StatusConflict {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return err
}

// Cancel asks the runner to stop the turn. It returns ErrNotFound if the
// runner does not have the turn.
func (r *Runner) Cancel(ctx context.Context, turn uuid.UUID) error {
	return r.tell(ctx, http.MethodPost, "/turns/"+turn.String()+"/cancel", nil)
}

// Answer passes the option that a person chose for an approval request of
// the turn on to the runner. It returns ErrNotFound if the runner does not
// have the turn.
func (r *Runner) Answer(ctx context.Context, turn uuid.UUID, request, option string) error {
	path := "/turns/" + turn.String() + "/approvals/" + url.PathEscape(request)
	return r.tell(ctx, http.MethodPut, path, map[string]string{"option_id": option})
}

// Events calls each for the events of the turn after the one numbered after,
// in order, until the stream ends or breaks or each returns an error. The
// runner ends the stream once the turn has ended. To resume, call Events
// again with the number of the last event received. It returns ErrNotFound
// if the runner does not have the turn.
func (r *Runner) Events(ctx context.Context, turn uuid.UUID, after uint64, each func(Event) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// The runner sends keepalives on an idle stream, so silence means that
	// it is gone.
	silence := time.AfterFunc(r.quiet, func() { cancel(errSilent) })
	defer silence.Stop()

	path := "/turns/" + turn.String() + "/events"
	request, err := r.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "text/event-stream")
	if after > 0 {
		request.Header.Set("Last-Event-ID", strconv.FormatUint(after, 10))
	}
	answer, err := r.http.Do(request)
	if err != nil {
		return r.cut(ctx, err)
	}
	defer answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		return refused(answer, http.MethodGet+" "+path)
	}

	lines := bufio.NewScanner(answer.Body)
	lines.Buffer(nil, eventLimit)
	var event Event
	for lines.Scan() {
		silence.Reset(r.quiet)
		line := lines.Bytes()
		if len(line) == 0 {
			// A blank line ends an event. Blocks without an event name, such
			// as comments, are skipped.
			whole := event
			event = Event{}
			if whole.Name == "" {
				continue
			}
			if whole.Seq != after+1 || !json.Valid(whole.Data) {
				return fmt.Errorf("invalid event %q with ID %d for turn %s after event %d",
					whole.Name, whole.Seq, turn, after)
			}
			if err := each(whole); err != nil {
				return err
			}
			after = whole.Seq
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "id":
			event.Seq, _ = strconv.ParseUint(string(value), 10, 64)
		case "event":
			event.Name = string(value)
		case "data":
			if event.Data != nil {
				event.Data = append(event.Data, '\n')
			}
			event.Data = append(event.Data, value...)
		}
	}
	if err := lines.Err(); err != nil {
		return r.cut(ctx, err)
	}
	return nil
}

// errSilent is the cause of abandoning an event stream that stayed silent
// for longer than quiet.
var errSilent = errors.New("runner sent nothing")

// cut returns the error that ended an event stream, reporting silence as
// errSilent.
func (r *Runner) cut(ctx context.Context, err error) error {
	if context.Cause(ctx) == errSilent {
		return fmt.Errorf("%w for %s", errSilent, r.quiet)
	}
	return fmt.Errorf("runner: %w", err)
}

// tell sends a request that the runner answers at once, and returns an error
// unless the response is a success.
func (r *Runner) tell(ctx context.Context, method, path string, body any) error {
	ctx, cancel := context.WithTimeout(ctx, told)
	defer cancel()
	request, err := r.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	answer, err := r.http.Do(request)
	if err != nil {
		return fmt.Errorf("runner: %w", err)
	}
	defer answer.Body.Close()
	if answer.StatusCode < 200 || answer.StatusCode > 299 {
		return refused(answer, method+" "+path)
	}
	return nil
}

func (r *Runner) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var sent io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		sent = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, r.address+path, sent)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

// refused returns the Refusal for an error response.
func refused(answer *http.Response, asked string) error {
	said, _ := io.ReadAll(io.LimitReader(answer.Body, 4<<10))
	return &Refusal{Who: "runner", Asked: asked, Status: answer.StatusCode, Said: strings.TrimSpace(string(said))}
}
