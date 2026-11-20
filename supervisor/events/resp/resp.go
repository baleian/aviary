// Package resp implements an events.Log on a server that speaks RESP and
// supports streams, Lua scripts and pub/sub. More than one product serves
// this protocol.
//
// The URL of such a log has the scheme valkey or redis, or valkeys or rediss
// for TLS:
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

// The maximum number of events that one read returns: waitBatch when it
// waits for new events, and readBatch when it reads existing ones.
const (
	waitBatch = 100
	readBatch = 1000
)

// receive records message ARGV[1] of the turn's runner and, if ARGV[2] is not
// empty, appends the event of type ARGV[2] with data ARGV[3] to the stream,
// atomically. It ignores a message that is already recorded, since a runner
// may resend a message that the Supervisor it lost is still recording. It
// returns -1 for a message that does not follow the last recorded one.
var receive = valkey.NewLuaScript(`
local last = tonumber(redis.call('GET', KEYS[2]) or '0')
local seq = tonumber(ARGV[1])
if seq <= last then return 0 end
if seq > last + 1 then return -1 end
if ARGV[2] ~= '' then redis.call('XADD', KEYS[1], '*', 'type', ARGV[2], 'data', ARGV[3]) end
redis.call('SET', KEYS[2], ARGV[1])
return 1
`)

// follow grants the lease on the turn to ARGV[1] for ARGV[2] milliseconds,
// unless another follower holds it.
var follow = valkey.NewLuaScript(`
local follower = redis.call('GET', KEYS[1])
if follower and follower ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1
`)

// leave releases the lease if ARGV[1] holds it.
var leave = valkey.NewLuaScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then redis.call('DEL', KEYS[1]) end
return 1
`)

// Plugin is the events.Plugin for logs of this kind.
type Plugin struct{}

func (Plugin) Schemes() []string { return []string{"valkey", "valkeys", "redis", "rediss"} }

func (Plugin) Open(address string) (events.Log, error) {
	options, err := valkey.ParseURL(address)
	if err != nil {
		// url.Error quotes the URL, password included.
		var quoting *url.Error
		if errors.As(err, &quoting) {
			err = quoting.Err
		}
		return nil, fmt.Errorf("parse the URL: %w", err)
	}
	// Client-side caching is not used, and not every managed server
	// supports it.
	options.DisableCache = true
	// Fail at once instead of retrying while the server is unreachable, so
	// that the caller can report the error.
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
		// XREAD with BLOCK 0 would block indefinitely.
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

// IsID reports whether id has the form of a stream entry ID.
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
	// PX requires a positive number of milliseconds.
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

// key returns the key of the turn's stream.
func key(turnID uuid.UUID) string {
	return "turn:" + turnID.String()
}

// receivedKey returns the key of the sequence number of the last recorded
// message. Its hash tag puts it in the cluster slot of the turn's stream, so
// that one script can use both.
func receivedKey(turnID uuid.UUID) string {
	return "{turn:" + turnID.String() + "}:received"
}

// followerKey returns the key that holds the turn's follower for the
// duration of its lease.
func followerKey(turnID uuid.UUID) string {
	return "{turn:" + turnID.String() + "}:follower"
}
