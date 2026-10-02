package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Options configures a Server. Addresses and timeouts come from config.HTTP.
type Options struct {
	Logger *slog.Logger
	Public http.Handler
	// Operator is the handler of the second, operator-only listener. Nil runs a single listener (the worker).
	Operator http.Handler
	// Health is told to report not ready as soon as shutdown starts. May be nil.
	Health *Health

	PublicAddr        string
	OperatorAddr      string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration

	// OnListening is called once the listeners are bound, with their actual addresses (useful with port 0). The
	// operator address is nil when there is no operator listener.
	OnListening func(public, operator net.Addr)
}

// Server runs the public listener and, when configured, the operator listener, and shuts them down gracefully.
type Server struct {
	opts Options
}

// New validates opts and returns a Server.
func New(opts Options) (*Server, error) {
	switch {
	case opts.Logger == nil:
		return nil, errors.New("httpserver: Logger is required")
	case opts.Public == nil:
		return nil, errors.New("httpserver: the Public handler is required")
	case opts.ShutdownTimeout <= 0:
		return nil, errors.New("httpserver: ShutdownTimeout must be positive")
	}
	return &Server{opts: opts}, nil
}

func (s *Server) newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		ReadTimeout:       s.opts.ReadTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
		// net/http internal errors go through the redacting logger instead of the standard log package.
		ErrorLog: slog.NewLogLogger(s.opts.Logger.Handler(), slog.LevelError),
	}
}

// Run binds both listeners, serves until ctx is cancelled or a listener fails, then shuts down: readiness flips
// to not ready, new connections are refused, in-flight requests get ShutdownTimeout to finish, and anything left
// is closed. It returns nil after a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	pubLn, err := lc.Listen(ctx, "tcp", s.opts.PublicAddr)
	if err != nil {
		return fmt.Errorf("listen on public address: %w", err)
	}
	var opLn net.Listener
	if s.opts.Operator != nil {
		if opLn, err = lc.Listen(ctx, "tcp", s.opts.OperatorAddr); err != nil {
			_ = pubLn.Close()
			return fmt.Errorf("listen on operator address: %w", err)
		}
	}

	servers := []*http.Server{s.newHTTPServer(s.opts.Public)}
	errCh := make(chan error, 2)
	go func() { errCh <- serve("public", servers[0], pubLn) }()
	var opAddr net.Addr
	if opLn != nil {
		op := s.newHTTPServer(s.opts.Operator)
		servers = append(servers, op)
		opAddr = opLn.Addr()
		go func() { errCh <- serve("operator", op, opLn) }()
	}

	if s.opts.OnListening != nil {
		s.opts.OnListening(pubLn.Addr(), opAddr)
	}
	s.logStarted(ctx, pubLn.Addr(), opAddr)

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
	}
	return errors.Join(serveErr, s.shutdown(ctx, servers...))
}

func (s *Server) logStarted(ctx context.Context, public, operator net.Addr) {
	if operator == nil {
		s.opts.Logger.InfoContext(ctx, "http listener started", "addr", public.String())
		return
	}
	s.opts.Logger.InfoContext(ctx, "http listeners started", "public_addr", public.String(), "operator_addr", operator.String())
}

func serve(name string, srv *http.Server, ln net.Listener) error {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s listener: %w", name, err)
	}
	return nil
}

func (s *Server) shutdown(ctx context.Context, servers ...*http.Server) error {
	if s.opts.Health != nil {
		s.opts.Health.SetDraining()
	}
	// The request context is already cancelled during a signal-driven shutdown; shutdown must not inherit that.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.ShutdownTimeout)
	defer cancel()
	s.opts.Logger.InfoContext(ctx, "http shutdown started", "timeout", s.opts.ShutdownTimeout.String())

	errs := make(chan error, len(servers))
	for _, srv := range servers {
		go func() { errs <- shutdownOne(shutdownCtx, srv) }()
	}
	var joined error
	for range servers {
		joined = errors.Join(joined, <-errs)
	}
	if joined == nil {
		s.opts.Logger.InfoContext(ctx, "http shutdown complete")
	}
	return joined
}

// shutdownOne drains a server and force-closes it when the grace period runs out.
func shutdownOne(ctx context.Context, srv *http.Server) error {
	err := srv.Shutdown(ctx)
	if err == nil {
		return nil
	}
	_ = srv.Close()
	return fmt.Errorf("graceful shutdown did not finish, connections closed: %w", err)
}
