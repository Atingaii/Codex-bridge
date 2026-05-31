package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

// acpClient is a generic bidirectional stdio JSON-RPC 2.0 channel for an Agent
// Client Protocol (ACP) adapter process. It mirrors the structure of
// appServerClient in appserver_runner.go but adds support for agent->client
// reverse requests (for example session/request_permission and fs/* methods)
// which the runner must answer.
//
// The client keeps the adapter process resident so a chat session maps to one
// long-lived ACP conversation (target A). Lifecycle is owned by ACPRunner.
type acpClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan acpResponse
	requests chan acpMessage
	notifs   chan acpMessage
	closed   bool
	wait     sync.Once
	waitDone chan struct{}
}

type acpMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type acpResponse struct {
	result json.RawMessage
	err    error
}

const (
	acpProcessCloseTimeout = 30 * time.Second
)

// startACPClient spawns the adapter process and begins reading its stdout. The
// caller owns context cancellation and must call close() when done. cwd, when
// non-empty, is used as the process working directory; env, when non-nil,
// replaces the process environment.
func startACPClient(ctx context.Context, command string, args []string, cwd string, env []string) (*acpClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	configureManagedCommand(cmd)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if env != nil {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &acpClient{
		cmd:      cmd,
		stdin:    stdin,
		pending:  make(map[int64]chan acpResponse),
		requests: make(chan acpMessage, 64),
		notifs:   make(chan acpMessage, 128),
		waitDone: make(chan struct{}),
	}
	go c.read(stdout)
	go io.Copy(io.Discard, stderr)
	return c, nil
}

// request sends a JSON-RPC request and waits for its matching response.
func (c *acpClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errACPExited
	}
	c.nextID++
	id := c.nextID
	ch := make(chan acpResponse, 1)
	c.pending[id] = ch
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, err := json.Marshal(req)
	if err == nil {
		_, err = c.stdin.Write(append(b, '\n'))
	}
	if err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	c.mu.Unlock()

	select {
	case res := <-ch:
		return res.result, res.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *acpClient) notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errACPExited
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// respond answers an agent->client request by echoing its id.
func (c *acpClient) respond(id json.RawMessage, result any) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": jsonRawOrNull(id), "result": result}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errACPExited
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// respondError answers an agent->client request with a JSON-RPC error.
func (c *acpClient) respondError(id json.RawMessage, code int, message string) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      jsonRawOrNull(id),
		"error":   map[string]any{"code": code, "message": message},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errACPExited
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *acpClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *acpClient) read(stdout io.Reader) {
	defer func() {
		c.mu.Lock()
		pending := c.pending
		c.pending = make(map[int64]chan acpResponse)
		c.closed = true
		c.mu.Unlock()
		for _, ch := range pending {
			ch <- acpResponse{err: errACPExited}
		}
		close(c.requests)
		close(c.notifs)
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var msg acpMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch {
		case msg.Method == "" && len(msg.ID) > 0:
			// Response to one of our requests.
			if id, ok := rawIDInt(msg.ID); ok {
				c.mu.Lock()
				ch := c.pending[id]
				delete(c.pending, id)
				c.mu.Unlock()
				if ch != nil {
					var err error
					if msg.Error != nil {
						err = errors.New(msg.Error.Message)
					}
					ch <- acpResponse{result: msg.Result, err: err}
				}
			}
		case msg.Method != "" && len(msg.ID) > 0:
			// Agent->client request expecting a response.
			c.requests <- msg
		case msg.Method != "":
			// Agent->client notification.
			c.notifs <- msg
		}
	}
}

func (c *acpClient) close() {
	c.closeWithTimeout(acpProcessCloseTimeout)
}

func (c *acpClient) closeWithTimeout(timeout time.Duration) {
	c.mu.Lock()
	terminate := !c.closed
	if terminate {
		c.closed = true
		_ = c.stdin.Close()
	}
	c.mu.Unlock()
	if c.cmd == nil {
		return
	}
	done := c.waitProcess()
	if timeout <= 0 {
		<-done
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
		if c.cmd.Process != nil {
			_ = terminateProcessGroup(c.cmd.Process.Pid)
		}
		<-done
	}
}

func (c *acpClient) waitProcess() <-chan struct{} {
	if c.waitDone == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	c.wait.Do(func() {
		go func() {
			_ = c.cmd.Wait()
			close(c.waitDone)
		}()
	})
	return c.waitDone
}

var errACPExited = errors.New("acp adapter exited")

func jsonRawOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func rawIDInt(id json.RawMessage) (int64, bool) {
	if len(id) == 0 {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(id, &n); err == nil {
		return n, true
	}
	// Some peers echo numeric ids as strings; not used for our own requests but
	// handled defensively.
	var s string
	if err := json.Unmarshal(id, &s); err == nil {
		var parsed int64
		if _, err := scanInt(s, &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func scanInt(s string, out *int64) (int, error) {
	var n int64
	read := 0
	neg := false
	for i, r := range s {
		if i == 0 && r == '-' {
			neg = true
			read++
			continue
		}
		if r < '0' || r > '9' {
			return read, errors.New("not an integer")
		}
		n = n*10 + int64(r-'0')
		read++
	}
	if read == 0 || (neg && read == 1) {
		return read, errors.New("not an integer")
	}
	if neg {
		n = -n
	}
	*out = n
	return read, nil
}
