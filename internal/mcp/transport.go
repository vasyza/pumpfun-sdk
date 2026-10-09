package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/vasyza/pumpfun-sdk/internal/logging"
)

const maxHTTPBodyBytes = 1 << 20

// ServeStdio uses standard input and output for protocol messages only.
func ServeStdio(ctx context.Context, server *protocol.Server) error {
	return server.Run(ctx, &protocol.StdioTransport{MaxLineLength: 1 << 20})
}

// HTTPHandler provides stateless Streamable HTTP at /mcp.
// It checks Host and Origin to prevent access through DNS rebinding.
func HTTPHandler(server *protocol.Server, logger zerolog.Logger) http.Handler {
	transport := protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, PropagateRequestCancellation: true,
		Logger: logging.Slog(logger), MaxRequestBodyBytes: maxHTTPBodyBytes,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "The request host is not permitted.", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme != "http" || u.Host != r.Host || u.User != nil {
				http.Error(w, "The request origin is not permitted.", http.StatusForbidden)
				return
			}
		}
		request, err := initializeHTTPRequest(r)
		if err != nil {
			http.Error(w, "The request body is too large or could not be read.", http.StatusBadRequest)
			return
		}
		transport.ServeHTTP(w, request)
	})
}

// initializeHTTPRequest permits the older handshake with a 2026 HTTP header.
// The SDK requires modern metadata for that header and rejects initialize with
// modern metadata. Change only the internal header for a bounded initialize
// request without modern metadata. The middleware selects the reply version.
func initializeHTTPRequest(r *http.Request) (*http.Request, error) {
	method := r.Header.Get("Mcp-Method")
	if r.Method != http.MethodPost || r.Header.Get("Mcp-Protocol-Version") != ProtocolVersion || method != "" && method != "initialize" {
		return r, nil
	}
	defer func() { _ = r.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBodyBytes+1))
	if err != nil || len(data) > maxHTTPBodyBytes {
		return nil, errors.New("the initialize request body could not be read within the limit")
	}
	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(data))
	var envelope struct {
		Method string `json:"method"`
		Params struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		} `json:"params"`
	}
	if json.Unmarshal(data, &envelope) == nil && envelope.Method == "initialize" {
		if _, modern := envelope.Params.Meta[protocol.MetaKeyProtocolVersion]; !modern {
			request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
		}
	}
	return request, nil
}

// ServeHTTP binds a loopback address and stops when the context is canceled.
func ServeHTTP(ctx context.Context, server *protocol.Server, listen string, logger zerolog.Logger) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return errors.New("the listen address must contain a host and a port")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("the HTTP server must use a loopback address")
	}
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", listen)
	if err != nil {
		return errors.New("the HTTP listen address could not be opened")
	}
	defer func() { _ = listener.Close() }()
	httpServer := &http.Server{
		Handler: HTTPHandler(server, logger), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: time.Minute,
		BaseContext: func(net.Listener) context.Context { return ctx },
		ErrorLog:    log.New(logging.Writer{Logger: logger}, "", 0),
	}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	logger.Info().Str("listen", listener.Addr().String()).Msg("The MCP HTTP server is ready.")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_ = httpServer.Close()
			return err
		}
		return nil
	}
}
