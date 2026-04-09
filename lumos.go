package kono

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	lumosSocketPath     = "/tmp/lumos.sock"
	lumosActionContinue = "continue"
	lumosActionAbort    = "abort"
)

type lumos struct {
	cfg lumosConfig
}

type lumosConfig struct {
	socketPath          string
	socketReadDeadline  time.Duration
	socketWriteDeadline time.Duration
	maxMsg              int
}

// SendGet sends request data from the client to Lumos over a unix socket
// and returns a modified request in the response with the action field.
//
// Wire protocol:
//
//	Request:  [4 bytes big-endian uint32: JSON length][N bytes: JSON]
//	Response: [4 bytes big-endian uint32: JSON length][N bytes: JSON]
//
// Action is a special field from Lumos that indicates the gateway action
// for the current request. Possible values: "continue" or "abort".
func (l *lumos) SendGet(ctx context.Context, req *http.Request, scriptPath string) (*LumosJSONResponse, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", l.cfg.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to dial lumos socket: %w", err)
	}
	defer conn.Close()

	if err = conn.SetReadDeadline(time.Now().Add(l.cfg.socketReadDeadline)); err != nil {
		return nil, fmt.Errorf("failed to set lumos socket read deadline: %w", err)
	}

	if err = conn.SetWriteDeadline(time.Now().Add(l.cfg.socketWriteDeadline)); err != nil {
		return nil, fmt.Errorf("failed to set lumos socket write deadline: %w", err)
	}

	// Marshal request payload
	lumosReq := LumosJSONData{
		RequestID:  requestIDFromContext(req.Context()),
		Method:     req.Method,
		Path:       req.URL.Path,
		Query:      req.URL.RawQuery,
		Headers:    req.Header.Clone(),
		Body:       nil,
		ClientIP:   clientIPFromContext(req.Context()),
		ScriptPath: scriptPath,
	}

	if rctx := chi.RouteContext(req.Context()); rctx != nil {
		for i, key := range rctx.URLParams.Keys {
			lumosReq.Params[key] = rctx.URLParams.Values[i]
		}
	}

	msg, err := json.Marshal(&lumosReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal lumos request: %w", err)
	}

	if len(msg) > l.cfg.maxMsg {
		return nil, fmt.Errorf("request message exceeds max size: %d bytes (limit %d)", len(msg), l.cfg.maxMsg)
	}

	// Write length-prefixed request: [uint32 big-endian length][JSON bytes].
	// The 4-byte prefix tells Lumos exactly how many bytes to read,
	// avoiding the need for delimiters or fixed-size buffers
	var sizeBuf [4]byte
	binary.BigEndian.PutUint32(sizeBuf[:], uint32(len(msg))) //nolint:gosec // no integer overflow

	if _, err = conn.Write(sizeBuf[:]); err != nil {
		return nil, fmt.Errorf("failed to write lumos request size: %w", err)
	}

	if _, err = conn.Write(msg); err != nil {
		return nil, fmt.Errorf("failed to write lumos request: %w", err)
	}

	// Read response size prefix.
	// io.ReadFull guarantees all 4 bytes are read — unlike conn.Read,
	// which may return fewer bytes than requested in a single call
	if _, err = io.ReadFull(conn, sizeBuf[:]); err != nil {
		return nil, fmt.Errorf("failed to read lumos response size: %w", err)
	}

	respSize := binary.BigEndian.Uint32(sizeBuf[:])

	// Guard against a malformed or malicious response that would cause
	// an oversized allocation
	if int(respSize) > l.cfg.maxMsg {
		return nil, fmt.Errorf("lumos response size %d exceeds max allowed %d", respSize, l.cfg.maxMsg)
	}

	// Read exactly respSize bytes — no more, no less
	rawResp := make([]byte, respSize)
	if _, err = io.ReadFull(conn, rawResp); err != nil {
		return nil, fmt.Errorf("failed to read lumos response: %w", err)
	}

	var lumosResp LumosJSONResponse
	if err = json.Unmarshal(rawResp, &lumosResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal lumos response: %w", err)
	}

	return &lumosResp, nil
}

type Script struct {
	Source string
	Path   string
}

type LumosJSONData struct {
	RequestID  string            `json:"request_id"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query"`
	Headers    http.Header       `json:"headers"`
	Params     map[string]string `json:"params"`
	Body       []byte            `json:"body"`
	ClientIP   string            `json:"client_ip"`
	ScriptPath string            `json:"script_path"`
}

type LumosJSONResponse struct {
	Action     string `json:"action"`
	HTTPStatus int    `json:"http_status"`
	Error      string `json:"error"`

	LumosJSONData
}
