package store

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Bus is an event bus that fan-outs to subscribers in-process AND, when a
// Postgres pool is attached, NOTIFY's on the matching channel so other
// workers (or future replicas) can pick events up via LISTEN.
//
// Subscribers in this process get events synchronously; cross-process
// delivery rides Postgres LISTEN/NOTIFY (channel name = "platform_events"
// with the structured Event in the payload).
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan Event]struct{}
	pool        *pgxpool.Pool
	listenStop  chan struct{}
}

const pgListenChannel = "platform_events"

func NewBus() *Bus {
	return &Bus{subscribers: map[string]map[chan Event]struct{}{}}
}

// AttachPg starts a background goroutine that LISTENs on `platform_events`
// and republishes incoming notifications into the in-process bus.
func (b *Bus) AttachPg(pool *pgxpool.Pool) {
	b.pool = pool
	b.listenStop = make(chan struct{})
	go b.listen()
}

func (b *Bus) listen() {
	ctx := context.Background()
	for {
		select {
		case <-b.listenStop:
			return
		default:
		}
		conn, err := b.pool.Acquire(ctx)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		_, err = conn.Exec(ctx, "LISTEN "+pgListenChannel)
		if err != nil {
			conn.Release()
			time.Sleep(2 * time.Second)
			continue
		}
		for {
			select {
			case <-b.listenStop:
				conn.Release()
				return
			default:
			}
			notif, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				log.Printf("bus listen: %v", err)
				break
			}
			var ev Event
			if err := json.Unmarshal([]byte(notif.Payload), &ev); err != nil {
				continue
			}
			b.fanout(ev)
		}
		conn.Release()
		time.Sleep(500 * time.Millisecond)
	}
}

// Subscribe returns a buffered channel that receives events whose channel
// matches `pattern`. A pattern ending with `:*` matches a prefix.
func (b *Bus) Subscribe(pattern string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	if b.subscribers[pattern] == nil {
		b.subscribers[pattern] = map[chan Event]struct{}{}
	}
	b.subscribers[pattern][ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		delete(b.subscribers[pattern], ch)
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

func (b *Bus) Publish(ev Event) {
	if ev.Ts.IsZero() {
		ev.Ts = time.Now()
	}
	// Local fanout — synchronous, low latency.
	b.fanout(ev)
	// Cross-process — fire-and-forget NOTIFY.
	if b.pool != nil {
		payload, _ := json.Marshal(ev)
		if len(payload) < 7000 { // Postgres NOTIFY payload limit ~8000 bytes
			_, _ = b.pool.Exec(context.Background(), "SELECT pg_notify($1, $2)", pgListenChannel, string(payload))
		}
	}
}

func (b *Bus) fanout(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for pattern, subs := range b.subscribers {
		if !matches(pattern, ev.Channel) {
			continue
		}
		for ch := range subs {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

func matches(pattern, channel string) bool {
	if pattern == channel {
		return true
	}
	if strings.HasSuffix(pattern, ":*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(channel, prefix)
	}
	return false
}
