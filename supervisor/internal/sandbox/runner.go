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
	// routeHeader is the header that tells the gateway which sandbox, and
	// which port of it, a request is for.
	routeHeader = "OpenSandbox-Ingress-To"
	// eventLimit is the longest event that is read.
	eventLimit = 4 << 20
	// told is how long a runner may take to answer what it is told.
	told = 30 * time.Second
)

// ErrBusy is the error of a runner that runs another turn.
var ErrBusy = errors.New("the runner runs another turn")

// Runner is the runner of a session, in its sandbox, as it is reached. What
// it says comes from the sandbox, in which the agent is root.
type Runner struct {
	address string
	route   string
	http    *http.Client
	quiet   time.Duration
}

// Turn is what a runner is given to run.
type Turn struct {
	// Text is the message that the user sent.
	Text string `json:"text"`
	// Instructions is what the agent is told before every turn.
	Instructions string `json:"instructions"`
	// Approval is "ask" for an agent that asks a person before it acts.
	Approval string `json:"approval"`
	// Environment is what the harness is started with, at the first turn of
	// the session: the model of the agent, for example.
	Environment map[string]string `json:"environment"`
}

// Event is an event of a turn, as the runner tells it.
type Event struct {
	// Seq is the number of the event in its turn, from one.
	Seq  uint64
	Name string
	// Data is JSON.
	Data json.RawMessage
}

// Start gives the runner the turn. A turn that it has already is no error,
// and one while it runs another is ErrBusy.
func (r *Runner) Start(ctx context.Context, turn uuid.UUID, start Turn) error {
	err := r.tell(ctx, http.MethodPut, "/turns/"+turn.String(), start)
	var refusal *Refusal
	if errors.As(err, &refusal) && refusal.Status == http.StatusConflict {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return err
}

// Cancel tells the runner to stop the turn. A turn that the runner does not
// have is ErrNotFound.
func (r *Runner) Cancel(ctx context.Context, turn uuid.UUID) error {
	return r.tell(ctx, http.MethodPost, "/turns/"+turn.String()+"/cancel", nil)
}

// Answer tells the runner which option a person chose for a request of the
// turn. A turn that the runner does not have is ErrNotFound.
func (r *Runner) Answer(ctx context.Context, turn uuid.UUID, request, option string) error {
	path := "/turns/" + turn.String() + "/approvals/" + url.PathEscape(request)
	return r.tell(ctx, http.MethodPut, path, map[string]string{"option_id": option})
}

// Events reads the events of the turn that follow the one with the number
// after, and gives each to each, in their order. It returns when the runner
// has no more to tell, which is when the turn has ended, when the reading
// was cut off, or when each returns an error: one who has not read the end
// of the turn reads on from the last event that it has. A turn that the
// runner does not have is ErrNotFound.
func (r *Runner) Events(ctx context.Context, turn uuid.UUID, after uint64, each func(Event) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// A runner that has nothing to tell says so, every few seconds: one
	// that says nothing at all is not there any more.
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
			// An empty line ends an event. Between two events there may be
			// lines that are none.
			whole := event
			event = Event{}
			if whole.Name == "" {
				continue
			}
			if whole.Seq != after+1 || !json.Valid(whole.Data) {
				return fmt.Errorf("the runner told %q as the event %d of the turn %s, after the event %d",
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

// errSilent is why the reading of a runner that says nothing is given up.
var errSilent = errors.New("the runner said nothing")

// cut is the error of a reading that ended with the error.
func (r *Runner) cut(ctx context.Context, err error) error {
	if context.Cause(ctx) == errSilent {
		return fmt.Errorf("%w for %s", errSilent, r.quiet)
	}
	return fmt.Errorf("the runner: %w", err)
}

// tell asks the runner for what takes it a moment.
func (r *Runner) tell(ctx context.Context, method, path string, body any) error {
	ctx, cancel := context.WithTimeout(ctx, told)
	defer cancel()
	request, err := r.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	answer, err := r.http.Do(request)
	if err != nil {
		return fmt.Errorf("the runner: %w", err)
	}
	defer answer.Body.Close()
	if answer.StatusCode < 200 || answer.StatusCode > 299 {
		return refused(answer, method+" "+path)
	}
	return nil
}

// request is a request to the runner, on its way through the gateway.
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
	request.Header.Set(routeHeader, r.route)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

// refused is the answer of a runner, or of the gateway on the way to it,
// that is no success.
func refused(answer *http.Response, asked string) error {
	said, _ := io.ReadAll(io.LimitReader(answer.Body, 4<<10))
	return &Refusal{Who: "the runner", Asked: asked, Status: answer.StatusCode, Said: strings.TrimSpace(string(said))}
}
