package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// OpsBridgeClient maintains a single persistent TCP connection to
// OpsBridgeCppMod's listener (port 9877 by default) inside the
// game-server-survival container, surviving server restarts via a
// reconnect loop with exponential backoff.
//
// Phase 10 C1: connection plumbing + status pill only. The synchronous
// Call() helper is implemented but no callers exist yet — C2 will wire
// the Ops tab Announce button through it.
//
// Wire protocol (matches OpsBridgeCppMod B0..B5):
//
//	auth     >> "<password>\n"
//	         << "{\"ok\":true}\n"   or   "{\"ok\":false,\"error\":\"...\"}\n"
//	dispatch >> "{\"op\":\"<op>\",\"id\":\"<uuid>\",\"args\":...}\n"
//	         << "{\"id\":\"<uuid>\",\"ok\":true,\"result\":...}\n"
//	         << "{\"id\":\"<uuid>\",\"ok\":false,\"error\":\"...\",\"code\":\"...\"}\n"
type OpsBridgeClient struct {
	addr     string
	password string

	// connected is observed by handleStatus on the HTTP thread. atomic
	// avoids holding mu just to answer the status pill.
	connected atomic.Bool

	mu      sync.Mutex
	conn    net.Conn
	writer  *bufio.Writer
	nextID  uint64
	pending map[string]chan opsReply

	// dispatched only by the Run goroutine; readers don't touch them.
}

// opsReply is what the read loop hands to a Call() waiter.
type opsReply struct {
	ok     bool
	result json.RawMessage // body of "result" on success
	errMsg string          // body of "error" on failure
	code   string          // body of "code" on failure (may be empty)
}

// ErrOpsBridgeDisconnected is returned to in-flight Call() waiters when
// the underlying socket dies before a reply arrives.
var ErrOpsBridgeDisconnected = errors.New("opsbridge: disconnected")

// NewOpsBridgeClient creates an unstarted client. Call Run(ctx) in a
// goroutine to begin connecting.
func NewOpsBridgeClient(addr, password string) *OpsBridgeClient {
	return &OpsBridgeClient{
		addr:     addr,
		password: password,
		pending:  make(map[string]chan opsReply),
	}
}

// Connected reports whether the client currently holds an authenticated
// socket. Snapshot-only — the value can flip the moment after a caller
// reads it.
func (c *OpsBridgeClient) Connected() bool { return c.connected.Load() }

// Addr returns the configured target address (for diagnostics).
func (c *OpsBridgeClient) Addr() string { return c.addr }

// Run blocks until ctx is cancelled, maintaining the connection with
// exponential backoff between attempts (1s → 30s cap). Each successful
// session runs until the read loop exits.
func (c *OpsBridgeClient) Run(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.session(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("opsbridge: session ended: %v (retry in %s)", err, backoff)
		}
		// Wait with backoff, unless ctx fires first.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		// On a clean session we want the next retry to be fast — only
		// extended outages should pay the full backoff.
		if c.connected.Load() {
			backoff = time.Second
		}
	}
}

// session establishes one authenticated socket and runs its read loop
// until the connection drops. Always restores connected=false and
// drains pending waiters before returning.
func (c *OpsBridgeClient) session(ctx context.Context) error {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	if _, err := io.WriteString(conn, c.password+"\n"); err != nil {
		return fmt.Errorf("auth write: %w", err)
	}
	reader := bufio.NewReader(conn)
	authLine, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("auth read: %w", err)
	}
	var authResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(authLine), &authResp); err != nil {
		return fmt.Errorf("auth parse %q: %w", authLine, err)
	}
	if !authResp.OK {
		return fmt.Errorf("auth refused: %s", authResp.Error)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clear deadline: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.writer = bufio.NewWriter(conn)
	c.mu.Unlock()
	c.connected.Store(true)
	log.Printf("opsbridge: connected to %s", c.addr)

	// Closing the conn from this goroutine on ctx cancel is what lets
	// the read loop unblock from its blocking ReadString.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()
	readErr := c.readLoop(reader)
	close(stop)

	c.mu.Lock()
	c.conn = nil
	c.writer = nil
	// Fail any in-flight Calls — they'll surface ErrOpsBridgeDisconnected
	// to their callers rather than block until ctx cancel.
	for id, ch := range c.pending {
		// Non-blocking send: Call() always allocates a buffered chan.
		select {
		case ch <- opsReply{ok: false, errMsg: ErrOpsBridgeDisconnected.Error()}:
		default:
		}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.connected.Store(false)
	log.Printf("opsbridge: disconnected from %s", c.addr)

	return readErr
}

// readLoop reads JSON envelopes line-by-line until EOF / network error.
func (c *OpsBridgeClient) readLoop(r *bufio.Reader) error {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // clean close — treat as a normal session end
			}
			return fmt.Errorf("read: %w", err)
		}
		var env struct {
			ID     string          `json:"id"`
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
			Code   string          `json:"code"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			log.Printf("opsbridge: malformed reply %q: %v", line, err)
			continue
		}
		if env.ID == "" {
			// Unsolicited or auth echo — ignore.
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[env.ID]
		if ok {
			delete(c.pending, env.ID)
		}
		c.mu.Unlock()
		if !ok {
			// Reply for a Call whose context was cancelled — drop.
			continue
		}
		ch <- opsReply{
			ok:     env.OK,
			result: env.Result,
			errMsg: env.Error,
			code:   env.Code,
		}
	}
}

// Call dispatches one RPC and blocks until either the matching reply
// arrives, ctx fires, or the underlying socket drops. The returned
// json.RawMessage is the body of the "result" field on success.
func (c *OpsBridgeClient) Call(ctx context.Context, op string, args any) (json.RawMessage, error) {
	if !c.connected.Load() {
		return nil, ErrOpsBridgeDisconnected
	}
	id := c.allocID()
	envelope := struct {
		Op   string `json:"op"`
		ID   string `json:"id"`
		Args any    `json:"args,omitempty"`
	}{Op: op, ID: id, Args: args}
	buf, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	ch := make(chan opsReply, 1)
	c.mu.Lock()
	if c.writer == nil {
		c.mu.Unlock()
		return nil, ErrOpsBridgeDisconnected
	}
	c.pending[id] = ch
	if _, err := c.writer.Write(buf); err == nil {
		_, err = c.writer.WriteString("\n")
	}
	if err == nil {
		err = c.writer.Flush()
	}
	if err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write: %w", err)
	}
	c.mu.Unlock()

	select {
	case reply := <-ch:
		if !reply.ok {
			if reply.errMsg == ErrOpsBridgeDisconnected.Error() {
				return nil, ErrOpsBridgeDisconnected
			}
			if reply.code != "" {
				return nil, fmt.Errorf("opsbridge %s: %s [%s]", op, reply.errMsg, reply.code)
			}
			return nil, fmt.Errorf("opsbridge %s: %s", op, reply.errMsg)
		}
		return reply.result, nil
	case <-ctx.Done():
		// Drop the slot so a late reply doesn't leak.
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *OpsBridgeClient) allocID() string {
	// Monotonic counter inside a single process is enough — IDs are
	// per-connection scoped on the cppmod side too. UUID would burn
	// entropy for no benefit.
	n := atomic.AddUint64(&c.nextID, 1)
	return "da-" + strconv.FormatUint(n, 10)
}
