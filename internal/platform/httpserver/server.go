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
	Logger   *slog.Logger
	Public   http.Handler
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

	// OnListening is called once both listeners are bound, with their actual addresses (useful with port 0).
	OnListening func(public, operator net.Addr)
}

// Server runs the public and operator listeners and shuts both down gracefully.
type Server struct {
	opts Options
}

// New validates opts and returns a Server.
func New(opts Options) (*Server, error) {
	switch {
	case opts.Logger == nil:
		return nil, errors.New("httpserver: Logger is required")
	case opts.Public == nil || opts.Operator == nil:
		return nil, errors.New("httpserver: Public and Operator handlers are required")
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
	opLn, err := lc.Listen(ctx, "tcp", s.opts.OperatorAddr)
	if err != nil {
		_ = pubLn.Close()
		return fmt.Errorf("listen on operator address: %w", err)
	}

	pub, op := s.newHTTPServer(s.opts.Public), s.newHTTPServer(s.opts.Operator)
	errCh := make(chan error, 2)
	go func() { errCh <- serve("public", pub, pubLn) }()
	go func() { errCh <- serve("operator", op, opLn) }()

	if s.opts.OnListening != nil {
		s.opts.OnListening(pubLn.Addr(), opLn.Addr())
	}
	s.opts.Logger.InfoContext(ctx, "http listeners started",
		"public_addr", pubLn.Addr().String(), "operator_addr", opLn.Addr().String())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
	}
	return errors.Join(serveErr, s.shutdown(ctx, pub, op))
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
