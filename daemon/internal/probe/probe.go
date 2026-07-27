// Package probe checks whether a local port speaks HTTP, and if so pulls the
// page title and favicon hint. This is what lets the dashboard show a real
// "Open" button for a dev server while leaving Postgres as a plain status row.
package probe

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result is the outcome of probing one port.
type Result struct {
	HTTP   bool   `json:"http"`
	Scheme string `json:"scheme,omitempty"`
	Status int    `json:"status,omitempty"`
	Title  string `json:"title,omitempty"`
	Server string `json:"server,omitempty"`
	// Favicon is the icon path advertised by the page, if any.
	Favicon string `json:"favicon,omitempty"`
	// Host is the loopback address that actually answered, so follow-up
	// requests such as the favicon proxy reach the same listener.
	Host string `json:"-"`

	checkedAt time.Time
}

// wireProtocol lists processes that speak their own binary protocol. Sending
// them an HTTP request would only produce noise in their logs, so we never do.
var wireProtocol = map[string]bool{
	"postgres":     true,
	"postmaster":   true,
	"mysqld":       true,
	"mariadbd":     true,
	"redis-server": true,
	"mongod":       true,
	"memcached":    true,
	"kafka":        true,
	"zookeeper":    true,
	"nats-server":  true,
	"beam.smp":     true,
	"sshd":         true,
}

// SkipProc reports whether a process should never be probed over HTTP.
func SkipProc(proc string) bool { return wireProtocol[proc] }

var (
	titleRe   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	iconRe    = regexp.MustCompile(`(?is)<link[^>]+rel=["']?[^"'>]*icon[^"'>]*["']?[^>]*>`)
	hrefRe    = regexp.MustCompile(`(?is)href=["']([^"']+)["']`)
	whitespce = regexp.MustCompile(`\s+`)
)

// Prober probes ports and caches the results, since a page title rarely changes
// and probing is the only genuinely slow part of a refresh cycle.
type Prober struct {
	client    *http.Client
	tlsClient *http.Client

	mu    sync.Mutex
	cache map[string]Result // keyed by pid:port so a restart re-probes

	// TTLs: a successful probe is trusted for a good while, a failure is
	// retried sooner because a dev server may still be booting.
	okTTL   time.Duration
	failTTL time.Duration
}

// New returns a Prober with timeouts tuned for loopback.
func New() *Prober {
	dialer := &net.Dialer{Timeout: 700 * time.Millisecond}
	base := func(insecure bool) *http.Client {
		return &http.Client{
			Timeout: 2 * time.Second,
			Transport: &http.Transport{
				DialContext:         dialer.DialContext,
				DisableKeepAlives:   true,
				TLSHandshakeTimeout: time.Second,
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecure},
			},
			// Don't chase redirects: a 302 already proves it speaks HTTP, and
			// following it can wander off to an external identity provider.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Prober{
		client:    base(false),
		tlsClient: base(true),
		cache:     make(map[string]Result),
		okTTL:     5 * time.Minute,
		failTTL:   20 * time.Second,
	}
}

// Probe returns the cached result if it is still fresh, otherwise probes now.
// hosts is the ordered list of loopback addresses the socket listens on; an
// IPv6-only listener (Vite's default) is unreachable on 127.0.0.1.
func (p *Prober) Probe(ctx context.Context, pid, port int, hosts []string) Result {
	k := strconv.Itoa(pid) + ":" + strconv.Itoa(port)

	p.mu.Lock()
	cached, ok := p.cache[k]
	p.mu.Unlock()
	if ok && p.fresh(cached) {
		return cached
	}

	res := p.probeOnce(ctx, port, hosts)
	res.checkedAt = time.Now()

	p.mu.Lock()
	p.cache[k] = res
	p.mu.Unlock()
	return res
}

func (p *Prober) fresh(r Result) bool {
	ttl := p.failTTL
	if r.HTTP {
		ttl = p.okTTL
	}
	return time.Since(r.checkedAt) < ttl
}

// Forget drops cache entries for PIDs that are gone.
func (p *Prober) Forget(alive map[int]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.cache {
		pidStr, _, ok := strings.Cut(k, ":")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(pidStr); err == nil && !alive[pid] {
			delete(p.cache, k)
		}
	}
}

func (p *Prober) probeOnce(ctx context.Context, port int, hosts []string) Result {
	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1"}
	}
	for _, host := range hosts {
		if r, ok := p.try(ctx, p.client, "http", host, port); ok {
			return r
		}
		// A TLS-only dev server (rarer, but Vite does it with --https) rejects
		// the plaintext request, so try again as HTTPS before giving up.
		if r, ok := p.try(ctx, p.tlsClient, "https", host, port); ok {
			return r
		}
	}
	return Result{}
}

func (p *Prober) try(ctx context.Context, client *http.Client, scheme, host string, port int) (Result, bool) {
	url := scheme + "://" + host + ":" + strconv.Itoa(port) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, false
	}
	req.Header.Set("User-Agent", "Marina/1.0 (+local dashboard probe)")
	req.Header.Set("Accept", "text/html,*/*")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, false
	}
	defer resp.Body.Close()

	res := Result{
		HTTP:   true,
		Scheme: scheme,
		Host:   host,
		Status: resp.StatusCode,
		Server: resp.Header.Get("Server"),
	}
	if res.Server == "" {
		res.Server = resp.Header.Get("X-Powered-By")
	}

	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "html") || ct == "" {
		// 256KB is plenty to reach </title> even in a bloated dev-mode document.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		res.Title = extractTitle(body)
		res.Favicon = extractIcon(body)
	}
	return res, true
}

func extractTitle(body []byte) string {
	m := titleRe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	title := whitespce.ReplaceAllString(strings.TrimSpace(string(m[1])), " ")
	if len(title) > 80 {
		title = title[:80] + "…"
	}
	return title
}

func extractIcon(body []byte) string {
	tag := iconRe.Find(body)
	if tag == nil {
		return ""
	}
	m := hrefRe.FindSubmatch(tag)
	if m == nil {
		return ""
	}
	href := strings.TrimSpace(string(m[1]))
	// Ignore inline data URIs and absolute externals; we only proxy local paths.
	if href == "" || strings.HasPrefix(href, "data:") || strings.HasPrefix(href, "http") {
		return ""
	}
	if !strings.HasPrefix(href, "/") {
		href = "/" + href
	}
	return href
}
