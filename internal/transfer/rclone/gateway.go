package rclone

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

const gatewayIdleTimeout = 60 * time.Second

type storageGateway struct {
	upstream     *url.URL
	accountPath  string
	infoPath     string
	upstreamKey  string
	localKey     string
	localHost    string
	identity     string
	scope        *gatewayScope
	transport    *http.Transport
	admitRequest func(context.Context) error
	server       *http.Server
	cancel       context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
	bulkSlots    chan struct{}
}

type gatewayFactory func(context.Context, Remote, *gatewayScope, int) (*storageGateway, Remote, error)

func startGateways(ctx context.Context, remotes []Remote, destination string, rules destinationRules, capacity int) ([]Remote, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	// SIGINT needs a usable route for the child's best-effort cleanup.
	// Revoke on exit or at the existing kill deadline, whichever is first.
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		select {
		case <-ctx.Done():
			timer := time.NewTimer(childCancelGrace)
			defer timer.Stop()
			select {
			case <-timer.C:
				cancel()
			case <-lifetime.Done():
			}
		case <-lifetime.Done():
		}
	}()
	local, closeAll, err := startGatewaysWith(lifetime, remotes, destination, rules, capacity, newStorageGateway)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return local, func() { cancel(); closeAll() }, nil
}

func startGatewaysWith(ctx context.Context, remotes []Remote, destination string, rules destinationRules, capacity int, create gatewayFactory) ([]Remote, func(), error) {
	var gateways []*storageGateway
	closeAll := func() {
		for _, gateway := range gateways {
			gateway.Close()
		}
	}
	var local []Remote
	seen := map[string]bool{}
	for _, remote := range remotes {
		if (remote.Name != "aeronautes_src" && remote.Name != "aeronautes_dst") || seen[remote.Name] {
			closeAll()
			return nil, nil, fmt.Errorf("gateway needs distinct fixed remote names")
		}
		seen[remote.Name] = true
		scope, err := destinationScope(remote.Name, destination, rules)
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		gateway, childRemote, err := create(ctx, remote, scope, capacity)
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		gateways = append(gateways, gateway)
		if gateway.identity != gateways[0].identity {
			closeAll()
			return nil, nil, fmt.Errorf("TLS trust differs between remotes; align the bookmarks' trust settings")
		}
		local = append(local, childRemote)
	}
	return local, closeAll, nil
}

func newStorageGateway(parent context.Context, remote Remote, scope *gatewayScope, capacity int) (*storageGateway, Remote, error) {
	if capacity < 1 || capacity > 1024 {
		return nil, Remote{}, fmt.Errorf("gateway connection capacity out of range")
	}
	if err := parent.Err(); err != nil {
		return nil, Remote{}, err
	}
	if remote.AdmitRequest != nil {
		if err := remote.AdmitRequest(parent); err != nil {
			return nil, Remote{}, err
		}
	}
	if remote.Token == "" || strings.ContainsAny(remote.Token, "\r\n\x00") {
		return nil, Remote{}, fmt.Errorf("gateway needs a valid upstream token")
	}
	policy := swiftcontrol.TLSConfig{Mode: swiftcontrol.TLSModeSystem}
	identity := "system"
	if remote.CACertFile != "" {
		pem, err := os.ReadFile(remote.CACertFile)
		if err != nil {
			return nil, Remote{}, fmt.Errorf("gateway CA bundle: %w", err)
		}
		policy = swiftcontrol.TLSConfig{Mode: swiftcontrol.TLSModeCustomCA, CACertPEM: pem}
		identity = fmt.Sprintf("ca:%x", sha256.Sum256(pem))
	}
	if remote.InsecureTLS {
		policy.Mode = swiftcontrol.TLSModeInsecure
		identity = "insecure"
		if remote.CACertFile != "" {
			identity = fmt.Sprintf("insecure+ca:%x", sha256.Sum256(policy.CACertPEM))
		}
	}
	if err := swiftcontrol.ValidateEndpointURL(remote.StorageURL, policy.Mode); err != nil {
		return nil, Remote{}, fmt.Errorf("gateway storage endpoint: %w", err)
	}
	upstream, err := url.Parse(remote.StorageURL)
	if err != nil {
		return nil, Remote{}, err
	}
	accountPath := strings.TrimSuffix(upstream.Path, "/")
	if accountPath != "" && !gatewayPathSafe(strings.TrimPrefix(accountPath, "/")) {
		return nil, Remote{}, fmt.Errorf("gateway storage endpoint has an unsafe account path")
	}
	tlsConfig, err := policy.ClientConfig()
	if err != nil {
		return nil, Remote{}, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return gatewayUpstreamConn{Conn: conn}, nil
		},
		TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 10 * time.Second,
		// One request per connection keeps the per-I/O idle deadlines
		// independent; the pool still permits all configured transfers.
		TLSNextProto:           map[string]func(string, *tls.Conn) http.RoundTripper{},
		DisableCompression:     true,
		ResponseHeaderTimeout:  30 * time.Second,
		ExpectContinueTimeout:  time.Second,
		IdleConnTimeout:        gatewayIdleTimeout,
		MaxIdleConns:           capacity,
		MaxIdleConnsPerHost:    capacity,
		MaxConnsPerHost:        capacity,
		MaxResponseHeaderBytes: 32 << 10,
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		transport.CloseIdleConnections()
		return nil, Remote{}, err
	}
	ctx, cancel := context.WithCancel(parent)
	gateway := &storageGateway{
		upstream: upstream, accountPath: accountPath, infoPath: path.Join(accountPath, "..", "..", "info"),
		upstreamKey: remote.Token, localKey: rand.Text(), localHost: listener.Addr().String(),
		identity: identity, scope: scope, transport: transport, cancel: cancel, admitRequest: remote.AdmitRequest,
		done: make(chan struct{}), bulkSlots: make(chan struct{}, 2),
	}
	gateway.server = &http.Server{
		Handler: gateway, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: gatewayIdleTimeout,
		MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	bounded := &gatewayListener{Listener: listener, ctx: ctx, slots: make(chan struct{}, capacity)}
	go func() {
		defer close(gateway.done)
		_ = gateway.server.Serve(bounded)
	}()
	go func() {
		select {
		case <-ctx.Done():
			gateway.Close()
		case <-gateway.done:
		}
	}()
	localURL := &url.URL{Scheme: "http", Host: gateway.localHost, Path: accountPath}
	return gateway, Remote{Name: remote.Name, StorageURL: localURL.String(), Token: gateway.localKey}, nil
}

func (g *storageGateway) Close() {
	g.closeOnce.Do(func() {
		g.cancel()
		_ = g.server.Close()
		g.transport.CloseIdleConnections()
		<-g.done
	})
}

func (g *storageGateway) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	refuse := func() { http.Error(w, "child storage request refused", http.StatusForbidden) }
	if req.URL.IsAbs() || req.URL.Host != "" || req.Host != g.localHost {
		refuse()
		return
	}
	// A child must not nominate a validated policy or manifest header
	// for removal by the hop-header filter before it reaches Swift.
	for _, value := range req.Header.Values("Connection") {
		for _, option := range strings.Split(value, ",") {
			option = strings.TrimSpace(option)
			if !strings.EqualFold(option, "close") && !strings.EqualFold(option, "keep-alive") {
				refuse()
				return
			}
		}
	}
	info := req.Method == http.MethodGet && req.URL.Path == g.infoPath && req.URL.RawQuery == "" && !req.URL.ForceQuery
	if !info {
		keys := req.Header.Values("X-Auth-Token")
		if len(keys) != 1 || subtle.ConstantTimeCompare([]byte(keys[0]), []byte(g.localKey)) != 1 {
			refuse()
			return
		}
		if req.URL.Path != g.accountPath && !strings.HasPrefix(req.URL.Path, g.accountPath+"/") {
			refuse()
			return
		}
	}
	controller := http.NewResponseController(w)
	body := req.Body
	if body != nil && body != http.NoBody {
		body = &gatewayBody{ReadCloser: body, controller: controller}
	}
	if !info && isGatewayBulk(req, g.accountPath) {
		if g.scope == nil || g.scope.keepsObjects {
			refuse()
			return
		}
		select {
		case g.bulkSlots <- struct{}{}:
			defer func() { <-g.bulkSlots }()
		case <-req.Context().Done():
			return
		}
		data, err := io.ReadAll(io.LimitReader(body, maxGatewayBulkBytes+1))
		if err != nil || !g.scope.validBulkBody(req, data) {
			refuse()
			return
		}
		body = io.NopCloser(bytes.NewReader(data))
	} else if !info {
		relative := strings.TrimPrefix(strings.TrimPrefix(req.URL.Path, g.accountPath), "/")
		if relative != "" && !gatewayReadSafe(relative) {
			refuse()
			return
		}
		switch req.Method {
		case http.MethodGet, http.MethodHead:
		case http.MethodPut, http.MethodDelete:
			if !gatewayPathSafe(relative) || !g.scope.allowsWrite(req, relative) || (req.Method == http.MethodDelete && g.scope.keepsObjects) {
				refuse()
				return
			}
			if !strings.Contains(relative, "/") && !g.sidecarPolicyMatches(req) {
				refuse()
				return
			}
		case http.MethodPost:
			if g.scope == nil || !g.scope.setsTimes || !gatewayPathSafe(relative) || !g.scope.allowsWrite(req, relative) {
				refuse()
				return
			}
		default:
			refuse()
			return
		}
	}
	upstream := req.Clone(req.Context())
	upstream.RequestURI = ""
	upstream.URL.Scheme, upstream.URL.Host = g.upstream.Scheme, g.upstream.Host
	upstream.Host, upstream.Body = g.upstream.Host, body
	upstream.Trailer = nil
	stripGatewayHeaders(upstream.Header)
	if !info {
		upstream.Header.Set("X-Auth-Token", g.upstreamKey)
	}
	resp, err := g.roundTrip(upstream)
	if err != nil {
		// Transport errors may contain request details. Keep them out of
		// child logs, which feed persisted job diagnostics.
		g.logUpstreamFailure(req, err)
		http.Error(w, "child storage upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		refuse()
		return
	}
	stripGatewayHeaders(resp.Header)
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	if err := controller.SetWriteDeadline(time.Now().Add(gatewayIdleTimeout)); err != nil {
		panic(http.ErrAbortHandler)
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.CopyBuffer(gatewayWriter{Writer: w, controller: controller}, resp.Body, make([]byte, 32<<10)); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// logUpstreamFailure records in the application log why the gateway answered
// the engine 502, which is otherwise all a job's diagnostics can say. Only the
// method, the upstream host and the network error are written: never the URL,
// whose path names the account and the object, and never a header. A request
// the engine or the job abandoned is not an upstream failure and is skipped.
func (g *storageGateway) logUpstreamFailure(req *http.Request, err error) {
	if req.Context().Err() != nil {
		return
	}
	applog.Warnf("storage gateway: %s to %s failed: %v", req.Method, g.upstream.Host, transportCause(err))
}

// transportCause drops a URL error's wrapper, whose text carries the URL.
func transportCause(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// repeatableRead reports whether a request may be sent again unchanged: a
// read with no body.
func repeatableRead(req *http.Request) bool {
	return (req.Method == http.MethodGet || req.Method == http.MethodHead) &&
		(req.Body == nil || req.Body == http.NoBody)
}

func (g *storageGateway) roundTrip(req *http.Request) (*http.Response, error) {
	if g.admitRequest != nil {
		if err := g.admitRequest(req.Context()); err != nil {
			if req.Body != nil {
				req.Body.Close()
			}
			return nil, err
		}
	}
	resp, err := g.transport.RoundTrip(req)
	// A read that fails in transit is sent once more, on another connection,
	// because the engine would not: rclone's Swift backend does not retry the
	// 502 the gateway answers with, so one dropped connection or TLS alert
	// from the store failed a whole copy (0.5.0 test run). A write is never
	// repeated here; the engine's own retries own that decision.
	if err != nil && repeatableRead(req) && req.Context().Err() == nil {
		applog.Warnf("storage gateway: %s to %s failed, trying once more: %v", req.Method, g.upstream.Host, transportCause(err))
		resp, err = g.transport.RoundTrip(req.Clone(req.Context()))
		return resp, err
	}
	// The store's own 502 is the same blip to the engine, which retries a
	// 503 itself but never a 502, so a read it answers with 502 is sent once
	// more too. A 503 is left to the engine, so an overloaded store is not
	// asked twice as often.
	if err == nil && resp.StatusCode == http.StatusBadGateway && repeatableRead(req) && req.Context().Err() == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		applog.Warnf("storage gateway: %s to %s answered 502, trying once more", req.Method, g.upstream.Host)
		resp, err = g.transport.RoundTrip(req.Clone(req.Context()))
	}
	return resp, err
}

func (g *storageGateway) sidecarPolicyMatches(req *http.Request) bool {
	// The child cannot select a tier or supply another container's
	// evidence. This fresh request is built wholly from the bound scope.
	target := *g.upstream
	target.Path = g.accountPath + "/" + g.scope.container
	target.RawPath = strings.TrimSuffix(g.upstream.EscapedPath(), "/") + "/" + url.PathEscape(g.scope.container)
	target.RawQuery, target.ForceQuery = "", false
	head, err := http.NewRequestWithContext(req.Context(), http.MethodHead, target.String(), nil)
	if err != nil {
		return false
	}
	head.Header.Set("X-Auth-Token", g.upstreamKey)
	resp, err := g.roundTrip(head)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	actual := resp.Header.Values("X-Storage-Policy")
	requested := req.Header.Values("X-Storage-Policy")
	if len(actual) == 0 {
		return len(requested) == 0
	}
	return len(actual) == 1 && actual[0] != "" && len(requested) == 1 && actual[0] == requested[0]
}

func stripGatewayHeaders(headers http.Header) {
	for _, value := range headers.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for name := range headers {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-auth-") || lower == "x-storage-token" || lower == "x-subject-token" || lower == "x-service-token" {
			headers.Del(name)
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Proxy-Authenticate", "Authorization", "WWW-Authenticate", "Authentication-Info", "Set-Cookie", "Location"} {
		headers.Del(name)
	}
}

type gatewayBody struct {
	io.ReadCloser
	controller *http.ResponseController
}

func (b *gatewayBody) Read(p []byte) (int, error) {
	if err := b.controller.SetReadDeadline(time.Now().Add(gatewayIdleTimeout)); err != nil {
		return 0, err
	}
	return b.ReadCloser.Read(p)
}

type gatewayWriter struct {
	io.Writer
	controller *http.ResponseController
}

func (w gatewayWriter) Write(p []byte) (int, error) {
	if err := w.controller.SetWriteDeadline(time.Now().Add(gatewayIdleTimeout)); err != nil {
		return 0, err
	}
	return w.Writer.Write(p)
}

type gatewayUpstreamConn struct{ net.Conn }

func (c gatewayUpstreamConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(gatewayIdleTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c gatewayUpstreamConn) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(gatewayIdleTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

type gatewayListener struct {
	net.Listener
	ctx   context.Context
	slots chan struct{}
}

func (l *gatewayListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.ctx.Done():
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &gatewayLimitedConn{Conn: conn, release: func() { <-l.slots }}, nil
}

type gatewayLimitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *gatewayLimitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
