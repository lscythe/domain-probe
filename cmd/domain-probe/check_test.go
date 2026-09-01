package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExpand(t *testing.T) {
	got := expand([]string{"acme", "foo.io", "# comment", "", "ACME."}, []string{"com", "dev"})
	want := []string{"acme.com", "acme.dev", "foo.io"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expand = %v, want %v", got, want)
	}
}

func TestParseBootstrap(t *testing.T) {
	doc := `{"services":[[["com","net"],["https://rdap.verisign.com/com/v1"]],[["dev"],["https://www.registry.google/rdap/"]]]}`
	m, err := parseBootstrap(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if m["com"] != "https://rdap.verisign.com/com/v1/" || m["net"] != m["com"] {
		t.Fatalf("com/net = %q %q", m["com"], m["net"])
	}
	if m["dev"] != "https://www.registry.google/rdap/" {
		t.Fatalf("dev = %q", m["dev"])
	}
}

func TestParseRDAP(t *testing.T) {
	body := []byte(`{
	  "events":[{"eventAction":"registration","eventDate":"1997-09-15T04:00:00Z"},
	            {"eventAction":"expiration","eventDate":"2028-09-14T04:00:00Z"}],
	  "entities":[{"roles":["registrar"],
	    "vcardArray":["vcard",[["version",{},"text","4.0"],["fn",{},"text","MarkMonitor Inc."]]]}]
	}`)
	reg, exp, _ := parseRDAP(body)
	if reg != "MarkMonitor Inc." {
		t.Fatalf("registrar = %q", reg)
	}
	if exp != "2028-09-14" {
		t.Fatalf("expires = %q", exp)
	}
}

func TestClassifyWhois(t *testing.T) {
	cases := []struct {
		text string
		want Status
	}{
		{"No match for \"NOPE.COM\".\n>>> Last update of whois database", StatusAvailable},
		{"Domain Name: acme.com\nRegistrar: GoDaddy.com, LLC\n", StatusTaken},
		{"Status: free\n", StatusAvailable},
		{"some unrelated banner text\n", StatusUnknown},
	}
	for _, c := range cases {
		if got := classifyWhois(c.text); got != c.want {
			t.Errorf("classifyWhois(%q) = %s, want %s", c.text, got, c.want)
		}
	}
}

func TestWhoisField(t *testing.T) {
	text := "Domain Name: ACME.COM\n   Registrar: GoDaddy.com, LLC\nRegistry Expiry Date: 2027-03-01T05:00:00Z\n"
	if got := whoisField(text, "registrar:", "sponsoring registrar:"); got != "GoDaddy.com, LLC" {
		t.Fatalf("registrar = %q", got)
	}
	if got := shortDate(whoisField(text, "registry expiry date:")); got != "2027-03-01" {
		t.Fatalf("expiry = %q", got)
	}
	if got := whoisField(text, "nope:"); got != "" {
		t.Fatalf("missing key = %q", got)
	}
}

func TestTLDOf(t *testing.T) {
	if tldOf("a.b.co.uk") != "uk" || tldOf("nodot") != "" {
		t.Fatal("tldOf wrong")
	}
}

func TestReadLinesFromStripsComments(t *testing.T) {
	in := "# my ideas\nacme foo.io\nbar # trailing note\n\n"
	got, err := readLinesFrom(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "acme,foo.io,bar" {
		t.Fatalf("readLinesFrom = %v", got)
	}
}

func TestValidDomain(t *testing.T) {
	good := []string{"navigo.app", "a.co", "x-y.example.com", "1234.io"}
	bad := []string{"-tld.com", "nodot", "", "foo-.com", "foo..com", "sp ace.com", "UPPER.com"}
	for _, d := range good {
		if !validDomain(d) {
			t.Errorf("validDomain(%q) = false, want true", d)
		}
	}
	for _, d := range bad {
		if validDomain(d) {
			t.Errorf("validDomain(%q) = true, want false", d)
		}
	}
}

func TestCollectListings(t *testing.T) {
	var doc any
	raw := `{"ResponseCode":"0","AuctionList":{"Auction":[
	  {"DomainName":"navigo.com","CurrentPrice":"1250","Currency":"USD","EndTime":"2026-09-04 18:00"},
	  {"DomainName":"foo.io","HighBid":340,"AuctionEndDate":"2026-09-01"}]},
	  "CloseoutList":[{"name":"bar.net","price":"11.99"}],
	  "junk":{"note":"not a domain"}}`
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]Listing{}
	collectListings(doc, "dynadot", got)

	if len(got) != 3 {
		t.Fatalf("got %d listings, want 3: %v", len(got), got)
	}
	if l := got["navigo.com"]; l.Price != "1250" || l.Currency != "USD" || l.Ends != "2026-09-04 18:00" {
		t.Errorf("navigo.com = %+v", l)
	}
	if l := got["foo.io"]; l.Price != "340" {
		t.Errorf("foo.io price = %q", l.Price)
	}
	if l := got["bar.net"]; l.Price != "11.99" {
		t.Errorf("bar.net = %+v", l)
	}
	if s := got["navigo.com"].String(); s != "auction USD 1250 on dynadot ends 2026-09-04 18:00" {
		t.Errorf("String() = %q", s)
	}
}

func TestRefineLifecycle(t *testing.T) {
	c := NewChecker(time.Second)
	r := Result{Status: StatusTaken, Expires: "2026-06-01", Statuses: []string{"pendingdelete"}}
	c.refineLifecycle(&r)
	if r.Status != StatusDropping || !strings.Contains(r.Note, "drops ~2026-08-20") {
		t.Fatalf("dropping = %+v", r)
	}

	soon := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	r = Result{Status: StatusTaken, Expires: soon, Statuses: []string{"clienttransferprohibited"}}
	c.refineLifecycle(&r)
	if r.Status != StatusExpiring {
		t.Fatalf("expiring = %+v", r)
	}

	r = Result{Status: StatusTaken, Expires: "2030-01-01"}
	c.refineLifecycle(&r)
	if r.Status != StatusTaken {
		t.Fatalf("taken = %+v", r)
	}
}

func TestNormalizeEPP(t *testing.T) {
	cases := map[string]string{
		"pending delete": "pendingdelete",
		"pendingDelete https://icann.org/epp#pendingDelete": "pendingdelete",
		"redemption period":        "redemptionperiod",
		"clientTransferProhibited": "clienttransferprohibited",
	}
	for in, want := range cases {
		if got := normalizeEPP(in); got != want {
			t.Errorf("normalizeEPP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPriceForLongestSuffix(t *testing.T) {
	prices := map[string]TLDPrice{
		"mx":     {Registration: "35.57", Renewal: "41.23"},
		"com.mx": {Registration: "11.33", Renewal: "23.32"},
		"sh":     {Registration: "34.98", Renewal: "34.98"},
	}
	if p, _ := priceFor(prices, "shop.com.mx"); p.Registration != "11.33" {
		t.Errorf("com.mx = %+v, want the multi-label entry", p)
	}
	if p, _ := priceFor(prices, "shop.mx"); p.Registration != "35.57" {
		t.Errorf("mx = %+v", p)
	}
	if _, ok := priceFor(prices, "nope.zzz"); ok {
		t.Error("unknown TLD should miss")
	}
}

func TestBuyURL(t *testing.T) {
	if u := buyURL(Result{Domain: "navigo.sh", Status: StatusAvailable}); !strings.Contains(u, "porkbun.com") {
		t.Errorf("available buy = %q", u)
	}
	if u := buyURL(Result{Domain: "navigo.com", Status: StatusDropping}); !strings.Contains(u, "sedo.com") {
		t.Errorf("dropping buy = %q", u)
	}
	if u := buyURL(Result{Domain: "x.com", Status: StatusError}); u != "" {
		t.Errorf("error buy = %q, want empty", u)
	}
}

func TestPriceOfShowsRenewal(t *testing.T) {
	if got := priceOf(Result{Price: "1.11", Renewal: "62.00"}); got != "$1.11 → $62.00" {
		t.Errorf("teaser price = %q", got)
	}
	if got := priceOf(Result{Price: "34.98", Renewal: "34.98"}); got != "$34.98" {
		t.Errorf("flat price = %q", got)
	}
}

func TestResolveTLDs(t *testing.T) {
	got, err := resolveTLDs(context.Background(), NewChecker(time.Second), "popular")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 40 {
		t.Fatalf("popular = %d TLDs, want ~60", len(got))
	}
	if len(got) != len(dedupe(got)) {
		t.Error("popular list has duplicates")
	}
	if got, _ = resolveTLDs(context.Background(), nil, "com, .io ,dev"); strings.Join(got, ",") != "com,io,dev" {
		t.Errorf("csv = %v", got)
	}
}
