package shield

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxy_EndToEnd(t *testing.T) {
	var upstreamRequests uint64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&upstreamRequests, 1)
		w.Header().Set("X-Custom-Echo", r.URL.Path)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	proxy, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		TargetURL:  upstream.URL,
		PoolSize:   10,
	})
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	go func() {
		_ = proxy.Start()
	}()
	defer func() {
		_ = proxy.Close()
	}()

	time.Sleep(50 * time.Millisecond)

	proxyURL := "http://" + proxy.Addr()

	// Make 5 requests through the proxy
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 5; i++ {
		resp, err := client.Get(proxyURL + "/api/v1/test")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		if resp.Header.Get("X-PortArmor-Pooled") != "1" {
			t.Errorf("expected X-PortArmor-Pooled header")
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != "ok" {
			t.Errorf("unexpected body: %s", string(body))
		}
	}

	if atomic.LoadUint64(&upstreamRequests) != 5 {
		t.Errorf("expected 5 upstream requests, got %d", upstreamRequests)
	}

	metrics := proxy.GetMetrics()
	if metrics.RequestsTotal != 5 {
		t.Errorf("expected 5 total requests, got %d", metrics.RequestsTotal)
	}
}

func TestProxy_Shutdown(t *testing.T) {
	proxy, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		TargetURL:  "http://127.0.0.1:9999",
	})
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	go func() {
		_ = proxy.Start()
	}()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := proxy.Shutdown(ctx); err != nil {
		t.Errorf("failed to shutdown cleanly: %v", err)
	}
}
