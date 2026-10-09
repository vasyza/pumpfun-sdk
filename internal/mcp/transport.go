package mcp

import (
	"context"
	"errors"
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

// ServeStdio uses standard input and output for protocol messages only.
func ServeStdio(ctx context.Context, server *protocol.Server) error {
	return server.Run(ctx, &protocol.StdioTransport{MaxLineLength: 1 << 20})
}

// HTTPHandler provides stateless Streamable HTTP at /protocol.
// It checks Host and Origin to prevent access through DNS rebinding.
func HTTPHandler(server *protocol.Server, logger zerolog.Logger) http.Handler {
	transport := protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, PropagateRequestCancellation: true,
		Logger: logging.Slog(logger), MaxRequestBodyBytes: 1 << 20,
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
		transport.ServeHTTP(w, r)
	})
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
