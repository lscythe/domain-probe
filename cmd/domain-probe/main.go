package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	tlds := flag.String("tld", "com,net,org,io,dev", "TLDs for bare names, popular, or all")
	plain := flag.Bool("plain", false, "tab-separated output")
	file := flag.String("f", "", "input file")
	flag.Parse()
	tokens := flag.Args()
	if *file != "" {
		lines, e := readLines(*file)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		tokens = append(tokens, lines...)
	}
	if len(tokens) == 0 {
		usage()
		return
	}
	c := NewChecker(10 * time.Second)
	ctx := context.Background()
	ts, e := resolveTLDs(ctx, c, *tlds)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	rs := run(c, expand(tokens, ts), 8, 10*time.Second, false)
	if *plain || !isTTY(os.Stdout) {
		printPlain(rs)
	} else {
		printTable(rs)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "domain-probe - check domain availability via RDAP, falling back to WHOIS")
}
func run(c *Checker, domains []string, workers int, timeout time.Duration, auction bool) []Result {
	out := make([]Result, len(domains))
	for i, d := range domains {
		out[i] = c.Check(context.Background(), d)
	}
	return out
}
func printPlain(rs []Result) {
	for _, r := range rs {
		fmt.Printf("%s\t%s\t%s\n", r.Domain, r.Status, r.Note)
	}
}
func printTable(rs []Result)                       { printPlain(rs) }
func detailOf(r Result) string                     { return strings.TrimSpace(r.Note) }
func renderTable(rs []Result, selected int) string { return "" }
func summary(rs []Result) string                   { return fmt.Sprintf("%d domains", len(rs)) }
func priceOf(r Result) string {
	if r.Price == "" {
		return ""
	}
	if r.Renewal != "" && r.Renewal != r.Price {
		return "$" + r.Price + " → $" + r.Renewal
	}
	return "$" + r.Price
}
func expand(tokens, tlds []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, token := range tokens {
		for _, x := range strings.Fields(token) {
			if strings.HasPrefix(x, "#") {
				break
			}
			if strings.Contains(x, ".") {
				x = strings.TrimSuffix(x, ".")
				if !strings.Contains(x, ".") {
					continue
				}
				if !seen[x] {
					out = append(out, x)
					seen[x] = true
				}
			} else {
				for _, t := range tlds {
					d := x + "." + t
					if !seen[d] {
						out = append(out, d)
						seen[d] = true
					}
				}
			}
		}
	}
	return out
}
func splitCSV(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		x = strings.TrimPrefix(strings.TrimSpace(x), ".")
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
func readLines(path string) ([]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return readLinesFrom(f)
}
func readLinesFrom(r interface{ Read([]byte) (int, error) }) ([]string, error) {
	b := make([]byte, 1<<20)
	n, e := r.Read(b)
	if e != nil && n == 0 {
		return nil, e
	}
	var out []string
	for _, l := range strings.Split(string(b[:n]), "\n") {
		l = strings.TrimSpace(strings.SplitN(l, "#", 2)[0])
		out = append(out, strings.Fields(l)...)
	}
	return out, nil
}
