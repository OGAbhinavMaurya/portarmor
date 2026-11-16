package shield

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config configures the outbound connection pool shield proxy.
type Config struct {
	ListenAddr      string
	TargetURL       string
	PoolSize        int
	IdleConnTimeout time.Duration
}

// Metrics tracks requests and connection pooling efficiency.
type Metrics struct {
	RequestsTotal       uint64 `json:"requests_total"`
	ActiveRequests      int64  `json:"active_requests"`
	PooledSocketsActive int    `json:"pooled_sockets_active"`
}

// Proxy is a transparent loopback HTTP reverse proxy that absorbs unpooled
// outbound client traffic and multiplexes it over a persistent connection pool.
type Proxy struct {
	cfg       Config
	target    *url.URL
	server    *http.Server
	transport *http.Transport
	listener  net.Listener
	mu        sync.Mutex
	running   bool
	metrics   Metrics
}

// New creates a new connection pool shield proxy.
func New(cfg Config) (*Proxy, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:8080"
	}
	if cfg.TargetURL == "" {
		return nil, errors.New("target URL must not be empty")
	}
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 100
	}
	if cfg.IdleConnTimeout <= 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}

	rawTarget := cfg.TargetURL
	if !strings.HasPrefix(rawTarget, "http://") && !strings.HasPrefix(rawTarget, "https://") {
		rawTarget = "http://" + rawTarget
	}
	parsedTarget, err := url.Parse(rawTarget)
	if err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   false,
		MaxIdleConns:        cfg.PoolSize * 2,
		MaxIdleConnsPerHost: cfg.PoolSize,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		DisableKeepAlives:   false,
	}

	p := &Proxy{
		cfg:       cfg,
		target:    parsedTarget,
		transport: transport,
	}

	p.server = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: p,
	}

	return p, nil
}

// ServeHTTP proxies incoming requests over persistent pooled connections.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddUint64(&p.metrics.RequestsTotal, 1)
	atomic.AddInt64(&p.metrics.ActiveRequests, 1)
	defer atomic.AddInt64(&p.metrics.ActiveRequests, -1)

	outURL := *p.target
	outURL.Path = singleJoiningSlash(p.target.Path, r.URL.Path)
	outURL.RawQuery = r.URL.RawQuery

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
	if err != nil {
		http.Error(w, "Failed to create upstream request", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for k, vv := range r.Header {
		for _, v := range vv {
			outReq.Header.Add(k, v)
		}
	}
	outReq.Host = p.target.Host

	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("Upstream request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-PortArmor-Pooled", "1")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// Start begins listening on the configured address.
func (p *Proxy) Start() error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return errors.New("proxy already running")
	}

	ln, err := net.Listen("tcp", p.cfg.ListenAddr)
	if err != nil {
		p.mu.Unlock()
		return fmt.Errorf("listen failed: %w", err)
	}
	p.listener = ln
	p.running = true
	p.mu.Unlock()

	return p.server.Serve(ln)
}

// Addr returns the bound address of the listener.
func (p *Proxy) Addr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		return p.listener.Addr().String()
	}
	return p.cfg.ListenAddr
}

// Close stops the proxy and releases idle connections.
func (p *Proxy) Close() error {
	p.mu.Lock()
	p.running = false
	p.mu.Unlock()

	p.transport.CloseIdleConnections()
	return p.server.Close()
}

// Shutdown gracefully stops the proxy.
func (p *Proxy) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.running = false
	p.mu.Unlock()

	p.transport.CloseIdleConnections()
	return p.server.Shutdown(ctx)
}

// GetMetrics returns operational metrics.
func (p *Proxy) GetMetrics() Metrics {
	return Metrics{
		RequestsTotal:  atomic.LoadUint64(&p.metrics.RequestsTotal),
		ActiveRequests: atomic.LoadInt64(&p.metrics.ActiveRequests),
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
