package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusAvailable Status = "AVAILABLE"
	StatusTaken     Status = "TAKEN"
	StatusDropping  Status = "DROPPING"
	StatusExpiring  Status = "EXPIRING"
	StatusUnknown   Status = "UNKNOWN"
	StatusError     Status = "ERROR"
)

type Result struct {
	Domain    string
	Status    Status
	Registrar string
	Expires   string
	Source    string   // rdap | whois
	Note      string   // error text, EPP status codes, or why unknown
	Statuses  []string // normalized EPP status codes
	Auction   string   // marketplace listing, when -auction is on
	Price     string   // first-year registration price, when known
	Renewal   string   // renewal price, when known
	Buy       string   // where to acquire it
}

// Checker resolves domain availability via RDAP, falling back to port-43 WHOIS
// for TLDs that have no RDAP service or whose RDAP server is unhealthy.
type Checker struct {
	HTTP *http.Client
	// Bulk is for the one-shot list fetches (pricing, marketplace). They are
	// large and slow enough that the per-lookup timeout would kill them.
	Bulk    *http.Client
	Timeout time.Duration
	// ExpiringIn flags a still-registered domain as EXPIRING when its
	// expiry date falls within this window.
	ExpiringIn time.Duration

	bootstrapOnce sync.Once
	bootstrap     map[string]string // tld -> RDAP base URL
	bootstrapErr  error

	mu          sync.Mutex
	whoisServer map[string]string // tld -> whois host
	deadHost    map[string]bool   // whois hosts that refused; do not redial
}

func NewChecker(timeout time.Duration) *Checker {
	return &Checker{
		HTTP:        &http.Client{Timeout: timeout},
		Bulk:        &http.Client{Timeout: 60 * time.Second},
		Timeout:     timeout,
		ExpiringIn:  30 * 24 * time.Hour,
		whoisServer: map[string]string{},
		deadHost:    map[string]bool{},
	}
}

func (c *Checker) Check(ctx context.Context, domain string) Result {
	if !validDomain(domain) {
		return Result{Domain: domain, Status: StatusError, Note: "not a valid domain name"}
	}
	tld := tldOf(domain)
	base, err := c.rdapBase(ctx, tld)
	if err == nil && base != "" {
		if r, ok := c.rdap(ctx, base, domain); ok {
			return r
		}
	}
	return c.whois(ctx, domain, tld)
}

// --- RDAP ---

const bootstrapURL = "https://data.iana.org/rdap/dns.json"

func (c *Checker) rdapBase(ctx context.Context, tld string) (string, error) {
	c.bootstrapOnce.Do(func() { c.bootstrap, c.bootstrapErr = c.loadBootstrap(ctx) })
	if c.bootstrapErr != nil {
		return "", c.bootstrapErr
	}
	return c.bootstrap[tld], nil
}

func (c *Checker) loadBootstrap(ctx context.Context) (map[string]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, bootstrapURL, nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseBootstrap(resp.Body)
}

// parseBootstrap turns IANA's RDAP bootstrap document into a tld -> base URL map.
// Each service entry is [[tld, ...], [url, ...]].
func parseBootstrap(r io.Reader) (map[string]string, error) {
	var doc struct {
		Services [][][]string `json:"services"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	m := make(map[string]string, 1500)
	for _, svc := range doc.Services {
		if len(svc) < 2 || len(svc[1]) == 0 {
			continue
		}
		url := strings.TrimSuffix(svc[1][0], "/") + "/"
		for _, tld := range svc[0] {
			m[strings.ToLower(tld)] = url
		}
	}
	return m, nil
}

// rdap reports a result and whether it was conclusive. A non-conclusive answer
// (rate limit, server error, transport failure) means the caller should fall back.
func (c *Checker) rdap(ctx context.Context, base, domain string) (Result, bool) {
	return c.rdapOnce(ctx, base, domain, false)
}

func (c *Checker) rdapOnce(ctx context.Context, base, domain string, retried bool) (Result, bool) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"domain/"+domain, nil)
	req.Header.Set("Accept", "application/rdap+json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, false
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Result{Domain: domain, Status: StatusAvailable, Source: "rdap"}, true
	case resp.StatusCode == http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return Result{}, false
		}
		reg, exp, statuses := parseRDAP(body)
		r := Result{Domain: domain, Status: StatusTaken, Registrar: reg, Expires: exp, Source: "rdap", Statuses: statuses}
		c.refineLifecycle(&r)
		return r, true
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		// A sweep across every TLD trips registry rate limits. One backoff
		// keeps those answers on RDAP instead of stampeding WHOIS.
		if !retried {
			io.Copy(io.Discard, resp.Body)
			if sleepCtx(ctx, retryAfter(resp)) {
				return c.rdapOnce(ctx, base, domain, true)
			}
		}
		return Result{}, false
	default:
		return Result{}, false
	}
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 && secs <= 10 {
			return time.Duration(secs) * time.Second
		}
	}
	return 1500 * time.Millisecond
}

// sleepCtx waits, reporting false if the context died first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func parseRDAP(body []byte) (registrar, expires string, statuses []string) {
	var doc struct {
		Status []string `json:"status"`
		Events []struct {
			Action string `json:"eventAction"`
			Date   string `json:"eventDate"`
		} `json:"events"`
		Entities []struct {
			Roles      []string        `json:"roles"`
			VCardArray json.RawMessage `json:"vcardArray"`
		} `json:"entities"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return "", "", nil
	}
	for _, s := range doc.Status {
		statuses = append(statuses, normalizeEPP(s))
	}
	for _, e := range doc.Events {
		if e.Action == "expiration" {
			expires = shortDate(e.Date)
			break
		}
	}
	for _, e := range doc.Entities {
		if hasRole(e.Roles, "registrar") {
			registrar = vcardFN(e.VCardArray)
			break
		}
	}
	return registrar, expires, statuses
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// vcardFN digs the formatted name out of jCard: ["vcard", [ ["fn",{},"text","Name"], ... ]].
func vcardFN(raw json.RawMessage) string {
	var outer []json.RawMessage
	if json.Unmarshal(raw, &outer) != nil || len(outer) < 2 {
		return ""
	}
	var props [][]json.RawMessage
	if json.Unmarshal(outer[1], &props) != nil {
		return ""
	}
	for _, p := range props {
		if len(p) < 4 {
			continue
		}
		var key, val string
		if json.Unmarshal(p[0], &key) != nil || key != "fn" {
			continue
		}
		if json.Unmarshal(p[3], &val) == nil {
			return val
		}
	}
	return ""
}

func shortDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// --- WHOIS ---

var whoisAvailable = []string{
	"no match", "not found", "no data found", "no entries found",
	"no object found", "domain not found", "status: free",
	"status: available", "is available for registration", "no such domain",
}

var whoisTaken = []string{
	"registrar:", "creation date", "created:", "registry domain id",
	"domain name:", "registered on", "expiry date", "expiration date",
	"paid-till", "sponsoring registrar",
}

func (c *Checker) whois(ctx context.Context, domain, tld string) Result {
	server, err := c.whoisHost(ctx, tld)
	if err != nil {
		return Result{Domain: domain, Status: StatusError, Source: "whois", Note: err.Error()}
	}
	if server == "" {
		return Result{Domain: domain, Status: StatusUnknown, Source: "whois", Note: "no whois server for ." + tld}
	}
	c.mu.Lock()
	dead := c.deadHost[server]
	c.mu.Unlock()
	if dead {
		return Result{Domain: domain, Status: StatusUnknown, Source: "whois", Note: server + " is refusing connections"}
	}
	text, err := whoisQuery(ctx, server, domain, c.Timeout)
	if err != nil {
		// A refusing or unreachable WHOIS host will refuse every other TLD it
		// serves too. Remember it rather than dialing it once per TLD.
		if isConnRefused(err) {
			c.mu.Lock()
			c.deadHost[server] = true
			c.mu.Unlock()
		}
		return Result{Domain: domain, Status: StatusError, Source: "whois", Note: err.Error()}
	}
	status := classifyWhois(text)
	r := Result{Domain: domain, Status: status, Source: "whois"}
	if status == StatusTaken {
		r.Registrar = whoisField(text, "registrar:", "sponsoring registrar:")
		r.Expires = shortDate(whoisField(text, "registry expiry date:", "expiry date:", "expiration date:", "paid-till:"))
		r.Statuses = whoisStatuses(text)
		c.refineLifecycle(&r)
	}
	if status == StatusUnknown {
		r.Note = "whois text not recognized"
	}
	return r
}

func (c *Checker) whoisHost(ctx context.Context, tld string) (string, error) {
	c.mu.Lock()
	host, ok := c.whoisServer[tld]
	c.mu.Unlock()
	if ok {
		return host, nil
	}
	text, err := whoisQuery(ctx, "whois.iana.org", tld, c.Timeout)
	if err != nil {
		return "", err
	}
	host = whoisField(text, "whois:")
	c.mu.Lock()
	c.whoisServer[tld] = host
	c.mu.Unlock()
	return host, nil
}

func whoisQuery(ctx context.Context, server, query string, timeout time.Duration) (string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(server, "43"))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := fmt.Fprintf(conn, "%s\r\n", query); err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(conn, 1<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// classifyWhois reads free-form WHOIS text. Availability markers win over
// "taken" markers because some registries print both a not-found line and a
// boilerplate footer mentioning registrar fields.
func classifyWhois(text string) Status {
	low := strings.ToLower(text)
	for _, m := range whoisAvailable {
		if strings.Contains(low, m) {
			return StatusAvailable
		}
	}
	for _, m := range whoisTaken {
		if strings.Contains(low, m) {
			return StatusTaken
		}
	}
	return StatusUnknown
}

// whoisField returns the value of the first matching "key: value" line.
func whoisField(text string, keys ...string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		low := strings.ToLower(line)
		for _, k := range keys {
			if !strings.HasPrefix(low, k) {
				continue
			}
			if v := strings.TrimSpace(line[len(k):]); v != "" {
				return v
			}
		}
	}
	return ""
}

// validDomain checks LDH syntax: at least two labels, each 1-63 chars of
// letters, digits or hyphens, never hyphen-edged. Catches typos and stray
// flags before they cost a network round trip.
func validDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			ch := l[i]
			switch {
			case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '-':
			default:
				return false
			}
		}
	}
	return true
}

// AllTLDs returns every TLD that operates an RDAP service, sorted. These are
// the TLDs that can answer authoritatively, which is why they are the default
// universe for a full sweep.
func (c *Checker) AllTLDs(ctx context.Context) ([]string, error) {
	c.bootstrapOnce.Do(func() { c.bootstrap, c.bootstrapErr = c.loadBootstrap(ctx) })
	if c.bootstrapErr != nil {
		return nil, c.bootstrapErr
	}
	out := make([]string, 0, len(c.bootstrap))
	for tld := range c.bootstrap {
		out = append(out, tld)
	}
	sort.Strings(out)
	return out, nil
}

func isConnRefused(err error) bool {
	s := err.Error()
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "i/o timeout")
}

// --- lifecycle ---

// EPP codes that mean the registration already lapsed: the domain is on its way
// out and is what auction and drop-catch services list.
var droppingCodes = map[string]bool{
	"redemptionperiod": true,
	"pendingdelete":    true,
	"pendingrestore":   true,
}

// normalizeEPP folds RDAP's spaced lowercase form ("pending delete") and WHOIS's
// camelCase-with-trailing-URL form ("pendingDelete https://icann.org/epp#...")
// into one comparable token.
func normalizeEPP(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		if strings.Contains(s, "://") {
			s = s[:i]
		}
	}
	s = strings.ToLower(s)
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "-", " ")), "")
}

func whoisStatuses(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		low := strings.ToLower(line)
		for _, k := range []string{"domain status:", "status:"} {
			if strings.HasPrefix(low, k) {
				if v := normalizeEPP(strings.TrimSpace(line[len(k):])); v != "" {
					out = append(out, v)
				}
				break
			}
		}
	}
	return out
}

// refineLifecycle upgrades a TAKEN result to DROPPING or EXPIRING based on EPP
// status codes and the expiry date.
func (c *Checker) refineLifecycle(r *Result) {
	if r.Status != StatusTaken {
		return
	}
	for _, s := range r.Statuses {
		if droppingCodes[s] {
			r.Status = StatusDropping
			r.Note = strings.Join(r.Statuses, ", ")
			if d := estimateDrop(r.Expires); d != "" {
				r.Note += "; drops ~" + d
			}
			return
		}
	}
	if exp, err := time.Parse("2006-01-02", r.Expires); err == nil {
		if left := time.Until(exp); left > 0 && left < c.ExpiringIn {
			r.Status = StatusExpiring
			r.Note = fmt.Sprintf("expires in %dd", int(left.Hours()/24))
		}
	}
}

// estimateDrop projects the gTLD deletion date: expiry + 45d auto-renew grace
// + 30d redemption + 5d pendingDelete. ccTLDs run their own schedules, so this
// is a hint, not a promise.
// ponytail: fixed-offset heuristic; read the real drop date from the registry
// zone or a drop-catch feed if you start bidding on it.
func estimateDrop(expires string) string {
	exp, err := time.Parse("2006-01-02", expires)
	if err != nil {
		return ""
	}
	return exp.AddDate(0, 0, 80).Format("2006-01-02")
}

func tldOf(domain string) string {
	i := strings.LastIndex(domain, ".")
	if i < 0 {
		return ""
	}
	return strings.ToLower(domain[i+1:])
}
