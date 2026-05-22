package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// proxyHarness wires the CONNECT proxy to a fake "upstream" TCP
// listener so tests do not need real network. The upstream simply
// echoes "TUNNELED <host>" once the proxy connects to it.
type proxyHarness struct {
	proxy    *proxy
	proxyLn  net.Listener
	upstream net.Listener
	target   string // host:port the proxy should treat as the allowed upstream
}

func newHarness(t *testing.T, allowHost string) *proxyHarness {
	t.Helper()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	go func() {
		for {
			c, err := upstream.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.WriteString(c, "TUNNELED\n")
				io.Copy(io.Discard, c)
			}(c)
		}
	}()

	upHost, upPort, _ := net.SplitHostPort(upstream.Addr().String())
	_ = upHost

	p := newProxy([]string{allowHost})
	p.logf = func(string, ...any) {}
	// Rewrite dial: any request for allowHost:<anything> is redirected
	// to the harness upstream port.
	p.dial = func(network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if host != allowHost {
			return nil, fmt.Errorf("unexpected dial to %s", addr)
		}
		return net.Dial(network, net.JoinHostPort("127.0.0.1", upPort))
	}

	ln, err := p.listen(0)
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	t.Cleanup(func() {
		ln.Close()
		upstream.Close()
	})

	return &proxyHarness{
		proxy:    p,
		proxyLn:  ln,
		upstream: upstream,
		target:   allowHost + ":443",
	}
}

func TestProxyAllowsListedHost(t *testing.T) {
	h := newHarness(t, "api.example.test")

	c, err := net.Dial("tcp", h.proxyLn.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", h.target, h.target)

	br := bufio.NewReader(c)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.HasPrefix(status, "HTTP/1.1 200") {
		t.Fatalf("expected 200, got %q", status)
	}
	// consume blank line(s) until tunnel body
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	body, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read tunnel body: %v", err)
	}
	if !strings.Contains(body, "TUNNELED") {
		t.Fatalf("expected TUNNELED, got %q", body)
	}
}

func TestProxyDeniesUnlistedHost(t *testing.T) {
	h := newHarness(t, "api.example.test")
	c, err := net.Dial("tcp", h.proxyLn.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT evil.test:443 HTTP/1.1\r\nHost: evil.test:443\r\n\r\n")
	br := bufio.NewReader(c)
	status, _ := br.ReadString('\n')
	if !strings.HasPrefix(status, "HTTP/1.1 403") {
		t.Fatalf("expected 403, got %q", status)
	}
}

func TestProxyRejectsNonConnect(t *testing.T) {
	h := newHarness(t, "api.example.test")
	c, err := net.Dial("tcp", h.proxyLn.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	fmt.Fprintf(c, "GET http://api.example.test/ HTTP/1.1\r\nHost: api.example.test\r\n\r\n")
	br := bufio.NewReader(c)
	status, _ := br.ReadString('\n')
	if !strings.HasPrefix(status, "HTTP/1.1 405") {
		t.Fatalf("expected 405, got %q", status)
	}
}

// TestProxyViaHTTPSClient drives the proxy with the standard library
// http.Client + Transport.Proxy, the same path the Anthropic SDK
// takes when HTTPS_PROXY is set. The upstream serves real TLS so the
// tunnel must carry encrypted bytes end to end.
func TestProxyViaHTTPSClient(t *testing.T) {
	tlsCert, err := generateCert()
	if err != nil {
		t.Fatalf("gen cert: %v", err)
	}
	upstream := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "ok")
		}),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{tlsCert}},
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", upstream.TLSConfig)
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	defer ln.Close()
	go upstream.Serve(ln)

	_, upPort, _ := net.SplitHostPort(ln.Addr().String())

	p := newProxy([]string{"api.example.test"})
	p.logf = func(string, ...any) {}
	p.dial = func(network, addr string) (net.Conn, error) {
		return net.Dial(network, net.JoinHostPort("127.0.0.1", upPort))
	}
	pln, err := p.listen(0)
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	defer pln.Close()

	proxyURL, _ := url.Parse("http://" + pln.Addr().String())
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Get("https://api.example.test/")
	if err != nil {
		t.Fatalf("https through proxy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
