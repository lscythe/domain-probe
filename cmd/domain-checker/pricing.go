package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	porkbunPricingURL = "https://api.porkbun.com/api/json/v3/pricing/get"
	// Porkbun takes over ten seconds to serve its full price list and the
	// prices move rarely, so a short-lived local copy pays for itself.
	pricingCacheTTL = 24 * time.Hour
)

func pricingCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "domain-checker", "porkbun-pricing.json")
}

// TLDPrice is the first-year registration price and the renewal that follows it.
// Registration is frequently a loss-leader; renewal is what you actually live with.
type TLDPrice struct {
	Registration string
	Renewal      string
}

// FetchPricing pulls Porkbun's public price list, which needs no API key.
// Keys can be multi-label ("com.mx"), so lookups match the longest suffix.
func FetchPricing(ctx context.Context, client *http.Client) (map[string]TLDPrice, error) {
	cache := pricingCachePath()
	if body, ok := readFreshCache(cache); ok {
		if prices, err := decodePricing(body); err == nil {
			return prices, nil
		}
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, porkbunPricingURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}

	prices, err := decodePricing(body)
	if err != nil {
		return nil, err
	}
	writeCache(cache, body)
	return prices, nil
}

// readFreshCache returns the cached body when it exists and is inside the TTL.
func readFreshCache(path string) ([]byte, bool) {
	if path == "" {
		return nil, false
	}
	fi, err := os.Stat(path)
	if err != nil || time.Since(fi.ModTime()) > pricingCacheTTL {
		return nil, false
	}
	body, err := os.ReadFile(path)
	return body, err == nil
}

// writeCache stores the price list, ignoring failures: a missing cache only
// costs time on the next run.
func writeCache(path string, body []byte) {
	if path == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	_ = os.WriteFile(path, body, 0o644)
}

func decodePricing(body []byte) (map[string]TLDPrice, error) {
	var doc struct {
		Status  string `json:"status"`
		Pricing map[string]struct {
			Registration string `json:"registration"`
			Renewal      string `json:"renewal"`
		} `json:"pricing"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc.Status != "SUCCESS" {
		return nil, fmt.Errorf("porkbun pricing: status %q", doc.Status)
	}
	out := make(map[string]TLDPrice, len(doc.Pricing))
	for tld, p := range doc.Pricing {
		out[strings.ToLower(tld)] = TLDPrice{Registration: p.Registration, Renewal: p.Renewal}
	}
	return out, nil
}

// priceFor matches the longest suffix present in the table, so "shop.com.mx"
// finds "com.mx" rather than falling back to "mx".
func priceFor(prices map[string]TLDPrice, domain string) (TLDPrice, bool) {
	labels := strings.Split(strings.ToLower(domain), ".")
	for i := 1; i < len(labels); i++ {
		if p, ok := prices[strings.Join(labels[i:], ".")]; ok {
			return p, true
		}
	}
	return TLDPrice{}, false
}

// buyURL points at somewhere the domain can actually be acquired: a registrar
// checkout when it is free, an aftermarket search when someone already owns it.
func buyURL(r Result) string {
	q := url.QueryEscape(r.Domain)
	switch r.Status {
	case StatusAvailable:
		return "https://porkbun.com/checkout/search?q=" + q
	case StatusTaken, StatusExpiring, StatusDropping:
		return "https://sedo.com/search/?keyword=" + q
	}
	return ""
}

// openURL hands a link to the desktop's default browser.
func openURL(link string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	return exec.Command(cmd, append(args, link)...).Start()
}
