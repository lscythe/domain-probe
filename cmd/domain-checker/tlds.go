package main

import (
	"context"
	"fmt"
	"strings"
)

// popularTLDs is the set people actually launch products on: the legacy gTLDs,
// the tech-startup favourites, and the ccTLDs commonly used as vanity endings.
var popularTLDs = []string{
	"com", "net", "org", "co", "io", "ai", "dev", "app", "sh", "gg",
	"me", "xyz", "so", "to", "cc", "tv", "fm", "am", "is", "it",
	"la", "li", "ly", "ms", "mx", "nu", "pm", "re", "run", "st",
	"tech", "site", "online", "store", "shop", "cloud", "space", "world", "life", "live",
	"studio", "design", "agency", "digital", "media", "network", "systems", "tools", "works", "zone",
	"page", "site", "web", "wiki", "blog", "news", "email", "chat", "team", "group",
}

// resolveTLDs turns a -tld value into a concrete list. "all" means every
// RDAP-capable TLD (needs the IANA bootstrap), "popular" the curated set,
// anything else is read as a comma-separated list.
func resolveTLDs(ctx context.Context, c *Checker, spec string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "all":
		tlds, err := c.AllTLDs(ctx)
		if err != nil {
			return nil, fmt.Errorf("load TLD list: %w", err)
		}
		return tlds, nil
	case "popular":
		return dedupe(popularTLDs), nil
	default:
		list := splitCSV(spec)
		if len(list) == 0 {
			return nil, fmt.Errorf("no TLDs in %q", spec)
		}
		return list, nil
	}
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
