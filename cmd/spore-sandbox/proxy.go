package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// proxy is a CONNECT-only HTTPS proxy that allows tunnels to a fixed
// allowlist of hostnames. All other requests are refused with HTTP
// 403. The proxy listens on loopback inside the sandbox netns; the
// sandboxed agent reaches it via HTTPS_PROXY.
type proxy struct {
	allow map[string]struct{} // exact-match hostnames
	dial  func(network, addr string) (net.Conn, error)
	logf  func(format string, args ...any)
}

func newProxy(allowHosts []string) *proxy {
	allow := make(map[string]struct{}, len(allowHosts))
	for _, h := range allowHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			allow[h] = struct{}{}
		}
	}
	return &proxy{
		allow: allow,
		dial:  (&net.Dialer{Timeout: 10 * time.Second}).Dial,
		logf:  log.New(log.Writer(), "sandbox-proxy: ", log.LstdFlags).Printf,
	}
}

// listen starts the proxy on 127.0.0.1:0 (or the given port if
// nonzero) and returns the bound listener.
func (p *proxy) listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	go p.serve(ln)
	return ln, nil
}

// listenUnix starts the proxy on a unix socket at path. The caller
// owns cleanup of the socket file.
func (p *proxy) listenUnix(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	go p.serve(ln)
	return ln, nil
}

func (p *proxy) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if !errIsClosed(err) {
				p.logf("accept: %v", err)
			}
			return
		}
		go p.handle(c)
	}
}

func errIsClosed(err error) bool {
	return strings.Contains(err.Error(), "use of closed network connection")
}

func (p *proxy) handle(client net.Conn) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(15 * time.Second))

	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		// Silent on empty connections: the inside-shim probes the
		// socket at startup to confirm readiness, which surfaces as
		// io.EOF here.
		if err != io.EOF {
			p.logf("read request: %v", err)
		}
		return
	}
	if req.Method != http.MethodConnect {
		p.logf("reject non-CONNECT %s %s", req.Method, req.URL)
		writeStatus(client, http.StatusMethodNotAllowed, "Only CONNECT is supported")
		return
	}

	host, port, err := net.SplitHostPort(req.Host)
	if err != nil {
		writeStatus(client, http.StatusBadRequest, "Malformed CONNECT target")
		return
	}
	host = strings.ToLower(host)
	if _, ok := p.allow[host]; !ok {
		p.logf("deny CONNECT %s:%s (not in allowlist; add to [sandbox].allow_hosts in spore.toml or pass -allow %s)", host, port, host)
		writeStatus(client, http.StatusForbidden, "Host not in sandbox allowlist")
		return
	}

	upstream, err := p.dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		p.logf("dial %s:%s: %v", host, port, err)
		writeStatus(client, http.StatusBadGateway, "Upstream dial failed")
		return
	}
	defer upstream.Close()

	if _, err := io.WriteString(client, "HTTP/1.1 200 OK\r\n\r\n"); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})

	// br may have buffered bytes the client sent immediately after
	// the CONNECT line; flush those upstream before splicing.
	if n := br.Buffered(); n > 0 {
		buf, _ := br.Peek(n)
		if _, err := upstream.Write(buf); err != nil {
			return
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(upstream, client); halfClose(upstream) }()
	go func() { defer wg.Done(); io.Copy(client, upstream); halfClose(client) }()
	wg.Wait()
}

func halfClose(c net.Conn) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

// forwardTCPToUnix serves on a TCP listener and, for each accepted
// connection, dials the given unix socket path and splices bytes
// between the two. It is the inside-sandbox shim that exposes the
// host's CONNECT proxy on a loopback TCP port the way HTTPS_PROXY
// expects.
func forwardTCPToUnix(ln net.Listener, sock string, logf func(string, ...any)) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if !errIsClosed(err) {
				logf("forward accept: %v", err)
			}
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			u, err := net.Dial("unix", sock)
			if err != nil {
				logf("forward dial %s: %v", sock, err)
				return
			}
			defer u.Close()
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); io.Copy(u, c); halfClose(u) }()
			go func() { defer wg.Done(); io.Copy(c, u); halfClose(c) }()
			wg.Wait()
		}(c)
	}
}

func writeStatus(w io.Writer, code int, msg string) {
	fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(msg), msg)
}
