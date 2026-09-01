package domainchecker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Listing is one aftermarket offer for a domain.
type Listing struct {
	Domain   string
	Price    string
	Currency string
	Ends     string
	Market   string
}

func (l Listing) String() string {
	parts := []string{"auction"}
	if l.Price != "" {
		parts = append(parts, strings.TrimSpace(l.Currency+" "+l.Price))
	}
	parts = append(parts, "on "+l.Market)
	if l.Ends != "" {
		parts = append(parts, "ends "+l.Ends)
	}
	return strings.Join(parts, " ")
}

// dynadotCommands are the no-parameter list endpoints worth pulling. Dynadot
// publishes the command names but not their response schemas, so the decoder
// below reads them structurally rather than against a fixed struct.
var dynadotCommands = []string{"get_open_auctions", "get_expired_closeout_domains", "get_listings"}

// FetchListings pulls every aftermarket listing Dynadot will hand us and keys
// them by domain. Returns nil with no error when no API key is configured.
func FetchListings(ctx context.Context, client *http.Client) (map[string]Listing, error) {
	key := os.Getenv("DYNADOT_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("set DYNADOT_API_KEY to use -auction (no other marketplace has an open API)")
	}
	out := map[string]Listing{}
	var firstErr error
	for _, cmd := range dynadotCommands {
		u := "https://api.dynadot.com/api3.json?key=" + url.QueryEscape(key) + "&command=" + cmd
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := client.Do(req)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		var doc any
		if err := json.Unmarshal(body, &doc); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", cmd, err)
			}
			continue
		}
		collectListings(doc, "dynadot", out)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// collectListings walks arbitrary JSON and harvests any object that carries a
// domain-shaped value, pulling neighbouring price/currency/end fields when they
// exist. Written structurally on purpose: Dynadot's response field names are
// undocumented and have changed before, and a wrong struct tag would silently
// yield zero listings.
// ponytail: structural walk over a fixed schema; swap in a typed struct if
// Dynadot ever publishes one.
func collectListings(v any, market string, out map[string]Listing) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			collectListings(e, market, out)
		}
	case map[string]any:
		if l, ok := listingFromObject(t, market); ok {
			if _, dup := out[l.Domain]; !dup {
				out[l.Domain] = l
			}
		}
		for _, e := range t {
			collectListings(e, market, out)
		}
	}
}

func listingFromObject(obj map[string]any, market string) (Listing, bool) {
	l := Listing{Market: market}
	for k, v := range obj {
		s, ok := v.(string)
		if !ok {
			if f, isNum := v.(float64); isNum {
				s = fmt.Sprintf("%g", f)
			} else {
				continue
			}
		}
		lk := strings.ToLower(k)
		switch {
		case l.Domain == "" && strings.Contains(lk, "domain") && validDomain(strings.ToLower(s)):
			l.Domain = strings.ToLower(s)
		case l.Domain == "" && lk == "name" && validDomain(strings.ToLower(s)):
			l.Domain = strings.ToLower(s)
		case l.Price == "" && (strings.Contains(lk, "price") || strings.Contains(lk, "bid")):
			l.Price = s
		case l.Currency == "" && strings.Contains(lk, "currency"):
			l.Currency = s
		case l.Ends == "" && (strings.Contains(lk, "end") || strings.Contains(lk, "close")):
			l.Ends = s
		}
	}
	return l, l.Domain != ""
}
