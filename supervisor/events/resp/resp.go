// Package resp keeps the events of the turns in a server that has streams,
// scripts and channels and is spoken to in RESP. More than one product
// serves it.
//
// The address of such a log is a URL with the scheme valkey or redis, or
// valkeys or rediss for a connection with TLS:
//
//	valkey://user:password@host:6379
package resp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/valkey-io/valkey-go"

	"aviary/supervisor/events"
)

// start is the position before the first entry of a stream.
const start = "0-0"

// The most events that one read returns: that many when a reader waits for
// the next, and more when it reads what is there.
const (
	waitBatch = 100
	readBatch = 1000
)

// receive records the runner's message with the number ARGV[1] and adds its
// event, of type ARGV[2] and with the data ARGV[3], to the turn's stream. It
// leaves out a message that is recorded. Without it, a message that a runner
// sends again could be added twice: the Supervisor that the runner lost may
// still be recording it.
var receive = valkey.NewLuaScript(`
local last = tonumber(redis.call('GET', KEYS[2]) or '0')
local seq = tonumber(ARGV[1])
if seq <= last then return 0 end
if seq > last + 1 then return -1 end
if ARGV[2] ~= '' then redis.call('XADD', KEYS[1], '*', 'type', ARGV[2], 'data', ARGV[3]) end
redis.call('SET', KEYS[2], ARGV[1])
return 1
`)

// follow gives the turn to the Supervisor ARGV[1] for ARGV[2] milliseconds,
// unless another has it.
var follow = valkey.NewLuaScript(`
local follower = redis.call('GET', KEYS[1])
if follower and follower ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1
`)

// leave gives the turn up, if the Supervisor ARGV[1] has it.
var leave = valkey.NewLuaScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then redis.call('DEL', KEYS[1]) end
return 1
`)

// Plugin connects the Supervisor to the logs of this kind.
type Plugin struct{}

func (Plugin) Schemes() []string { return []string{"valkey", "valkeys", "redis", "rediss"} }

func (Plugin) Open(address string) (events.Log, error) {
	options, err := valkey.ParseURL(address)
	if err != nil {
		// A URL that cannot be parsed is quoted in the error, password included.
		var quoting *url.Error
		if errors.As(err, &quoting) {
			err = quoting.Err
		}
		return nil, fmt.Errorf("parse the URL: %w", err)
	}
	// The Supervisor does not cache on the client side, and not every
	// managed server supports it.
	options.DisableCache = true
	// The client would repeat a read for as long as the server is
	// unreachable. A read that fails at once lets the caller report it.
	options.DisableRetry = true
	client, err := valkey.NewClient(options)
	if err != nil {
		return nil, err
	}
	return &log{client: client}, nil
}

type log struct {
	client valkey.Client
}

func (l *log) Close() {
	l.client.Close()
}

func (l *log) Append(ctx context.Context, turnID uuid.UUID, eventType, data string) error {
	return l.client.Do(ctx, l.client.B().Xadd().Key(key(turnID)).Id("*").
		FieldValue().FieldValue("type", eventType).FieldValue("data", data).Build()).Error()
}

func (l *log) Receive(ctx context.Context, turnID uuid.UUID, seq uint64, eventType, data string) error {
	recorded, err := receive.Exec(ctx, l.client, []string{key(turnID), receivedKey(turnID)},
		[]string{strconv.FormatUint(seq, 10), eventType, data}).AsInt64()
	if err != nil {
		return err
	}
	if recorded < 0 {
		return events.ErrGap
	}
	return nil
}

func (l *log) Received(ctx context.Context, turnID uuid.UUID) (uint64, error) {
	seq, err := l.client.Do(ctx, l.client.B().Get().Key(receivedKey(turnID)).Build()).AsUint64()
	if valkey.IsValkeyNil(err) {
		return 0, nil
	}
	return seq, err
}

func (l *log) Read(ctx context.Context, turnID uuid.UUID, after string, wait time.Duration) ([]events.Event, error) {
	if after == events.Start {
		after = start
	}
	var entries []valkey.XRangeEntry
	if wait > 0 {
		streams, err := l.client.Do(ctx, l.client.B().Xread().Count(waitBatch).Block(wait.Milliseconds()).
			Streams().Key(key(turnID)).Id(after).Build()).AsXRead()
		if err != nil && !valkey.IsValkeyNil(err) {
			return nil, err
		}
		entries = streams[key(turnID)]
	} else {
		// A read that blocks for no time would block for good.
		var err error
		entries, err = l.client.Do(ctx, l.client.B().Xrange().Key(key(turnID)).Start("("+after).End("+").
			Count(readBatch).Build()).AsXRange()
		if err != nil {
			return nil, err
		}
	}
	batch := make([]events.Event, len(entries))
	for i, entry := range entries {
		batch[i] = events.Event{ID: entry.ID, Type: entry.FieldValues["type"], Data: entry.FieldValues["data"]}
	}
	return batch, nil
}

// IsID reports whether id has the form of the ID of a stream entry.
func (*log) IsID(id string) bool {
	millis, sequence, found := strings.Cut(id, "-")
	if !found {
		return false
	}
	_, millisErr := strconv.ParseUint(millis, 10, 64)
	_, sequenceErr := strconv.ParseUint(sequence, 10, 64)
	return millisErr == nil && sequenceErr == nil
}

func (l *log) Expire(ctx context.Context, turnID uuid.UUID, after time.Duration) error {
	for _, key := range []string{key(turnID), receivedKey(turnID)} {
		if err := l.client.Do(ctx, l.client.B().Pexpire().Key(key).Milliseconds(after.Milliseconds()).Build()).Error(); err != nil {
			return err
		}
	}
	return nil
}

func (l *log) Follow(ctx context.Context, turnID, follower uuid.UUID, lease time.Duration) (bool, error) {
	// A key cannot be kept for less than a millisecond.
	milliseconds := max(lease.Milliseconds(), 1)
	given, err := follow.Exec(ctx, l.client, []string{followerKey(turnID)},
		[]string{follower.String(), strconv.FormatInt(milliseconds, 10)}).AsInt64()
	return given == 1, err
}

func (l *log) Leave(ctx context.Context, turnID, follower uuid.UUID) error {
	return leave.Exec(ctx, l.client, []string{followerKey(turnID)}, []string{follower.String()}).Error()
}

func (l *log) Announce(ctx context.Context, channel string, turnID uuid.UUID) error {
	return l.client.Do(ctx, l.client.B().Publish().Channel(channel).Message(turnID.String()).Build()).Error()
}

func (l *log) Watch(ctx context.Context, channel string, announced func(turnID uuid.UUID)) {
	for ctx.Err() == nil {
		err := l.client.Receive(ctx, l.client.B().Subscribe().Channel(channel).Build(),
			func(message valkey.PubSubMessage) {
				if turnID, err := uuid.Parse(message.Message); err == nil {
					announced(turnID)
				}
			})
		if ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "watch for what the Supervisors announce", "channel", channel, "error", err)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
	}
}

// key names the turn's stream.
func key(turnID uuid.UUID) string {
	return "turn:" + turnID.String()
}

// receivedKey names where the number of the last recorded message of the
// turn's runner is kept. Without the braces, a cluster could keep it apart
// from the turn's stream, and no script could use both.
func receivedKey(turnID uuid.UUID) string {
	return "{turn:" + turnID.String() + "}:received"
}

// followerKey names where the Supervisor that follows the turn is kept, for
// as long as its lease.
func followerKey(turnID uuid.UUID) string {
	return "{turn:" + turnID.String() + "}:follower"
}
