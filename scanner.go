package main

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// ScanResult holds the results of scanning a single resolver.
type ScanResult struct {
	Resolver        Resolver
	Status          string
	LatencyMs       int
	NSSupport       bool
	TXTSupport      bool
	RandomSubdomain bool
	TunnelRealism   bool
	EDNSSupport     bool
	EDNSMax         int
	NXDomainCorrect bool
	Score           int
	Details         string
}

func randLabel(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func buildScanQuery(name string, qtype uint16, ednsPayload uint16) ([]byte, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(name), qtype)
	msg.RecursionDesired = true
	if ednsPayload > 0 {
		msg.SetEdns0(ednsPayload, false)
	}
	return msg.Pack()
}

func getRcode(resp []byte) int {
	if len(resp) >= 4 {
		return int(resp[3] & 0x0F)
	}
	return -1
}

func getAncount(resp []byte) int {
	if len(resp) >= 8 {
		return int(resp[6])<<8 | int(resp[7])
	}
	return 0
}

func scanResolver(r Resolver, testDomain string) ScanResult {
	result := ScanResult{
		Resolver: r,
		Status:   "WORKING",
	}

	parts := strings.SplitN(testDomain, ".", 2)
	parentDomain := testDomain
	if len(parts) == 2 {
		parentDomain = parts[1]
	}

	var details []string
	score := 0

	// Test 0: Basic connectivity
	qname := fmt.Sprintf("%s.%s", randLabel(8), parentDomain)
	query, _ := buildScanQuery(qname, dns.TypeA, 0)
	t0 := time.Now()
	resp, err := sendQueryUDP(query, r.Addr, upstreamTimeout)
	result.LatencyMs = int(time.Since(t0).Milliseconds())
	if err != nil {
		result.Status = "ERROR"
		return result
	}
	if len(resp) < 12 {
		result.Status = "ERROR"
		return result
	}

	// Test 1: NS delegation + glue
	func() {
		defer func() { _ = recover() }()
		query, _ := buildScanQuery(parentDomain, dns.TypeNS, 0)
		resp, err := sendQueryUDP(query, r.Addr, upstreamTimeout)
		if err != nil || len(resp) < 12 || getRcode(resp) != 0 {
			details = append(details, "NS✗")
			return
		}
		query2, _ := buildScanQuery(fmt.Sprintf("ns.%s", parentDomain), dns.TypeA, 0)
		resp2, err := sendQueryUDP(query2, r.Addr, upstreamTimeout)
		if err == nil && resp2 != nil && getRcode(resp2) == 0 {
			result.NSSupport = true
			score++
			details = append(details, "NS✓")
		} else {
			details = append(details, "NS✗")
		}
	}()

	// Test 2: TXT record support
	func() {
		defer func() { _ = recover() }()
		qname := fmt.Sprintf("%s.%s", randLabel(8), parentDomain)
		query, _ := buildScanQuery(qname, dns.TypeTXT, 0)
		resp, err := sendQueryUDP(query, r.Addr, upstreamTimeout)
		if err == nil && len(resp) >= 12 {
			result.TXTSupport = true
			score++
			details = append(details, "TXT✓")
		} else {
			details = append(details, "TXT✗")
		}
	}()

	// Test 3: Random nested subdomain
	func() {
		defer func() { _ = recover() }()
		passed := false
		for i := 0; i < 2; i++ {
			qname := fmt.Sprintf("%s.%s.%s", randLabel(8), randLabel(8), testDomain)
			query, _ := buildScanQuery(qname, dns.TypeA, 0)
			resp, err := sendQueryUDP(query, r.Addr, upstreamTimeout)
			if err == nil && len(resp) >= 12 {
				passed = true
				break
			}
		}
		result.RandomSubdomain = passed
		if passed {
			score++
			details = append(details, "RND✓")
		} else {
			details = append(details, "RND✗")
		}
	}()

	// Test 4: Tunnel realism (DPI evasion)
	func() {
		defer func() { _ = recover() }()
		payload := make([]byte, 100)
		for i := range payload {
			payload[i] = byte(rand.Intn(256))
		}
		b32 := strings.ToLower(strings.TrimRight(
			base32StdEncode(payload), "="))
		dotified := dotifyBase32(b32, 57)
		qname := fmt.Sprintf("%s.%s", dotified, testDomain)
		query, _ := buildScanQuery(qname, dns.TypeTXT, 0)
		resp, err := sendQueryUDP(query, r.Addr, upstreamTimeout)
		if err == nil && len(resp) >= 12 {
			result.TunnelRealism = true
			score++
			details = append(details, "DPI✓")
		} else {
			details = append(details, "DPI✗")
		}
	}()

	// Test 5: EDNS0 payload size
	func() {
		defer func() { _ = recover() }()
		maxEdns := 0
		for _, size := range []uint16{512, 900, 1232} {
			qname := fmt.Sprintf("%s.%s", randLabel(8), testDomain)
			query, _ := buildScanQuery(qname, dns.TypeTXT, size)
			resp, err := sendQueryUDP(query, r.Addr, upstreamTimeout)
			if err == nil && len(resp) >= 12 && getRcode(resp) != 1 {
				maxEdns = int(size)
			} else {
				break
			}
		}
		result.EDNSMax = maxEdns
		if maxEdns > 0 {
			result.EDNSSupport = true
			score++
			details = append(details, fmt.Sprintf("EDNS✓(%d)", maxEdns))
		} else {
			details = append(details, "EDNS✗")
		}
	}()

	// Test 6: NXDOMAIN correctness
	func() {
		defer func() { _ = recover() }()
		nxCorrect := 0
		for i := 0; i < 3; i++ {
			qname := fmt.Sprintf("nxd-%s.invalid", randLabel(8))
			query, _ := buildScanQuery(qname, dns.TypeA, 0)
			resp, err := sendQueryUDP(query, r.Addr, upstreamTimeout)
			if err != nil {
				continue
			}
			rcode := getRcode(resp)
			if rcode == 3 { // NXDOMAIN
				nxCorrect++
			} else if rcode == 0 && getAncount(resp) == 0 {
				nxCorrect++
			}
		}
		if nxCorrect >= 2 {
			result.NXDomainCorrect = true
			score++
			details = append(details, "NXD✓")
		} else {
			details = append(details, "NXD✗")
		}
	}()

	result.Score = score
	result.Details = strings.Join(details, " ")
	return result
}

// scanResolversQuiet scans resolvers without terminal output.
func scanResolversQuiet(resolvers []Resolver, testDomain string, workers int) []ScanResult {
	var (
		mu      sync.Mutex
		results []ScanResult
		wg      sync.WaitGroup
	)

	if workers <= 0 {
		workers = 32
	}
	sem := make(chan struct{}, workers)
	for _, r := range resolvers {
		wg.Add(1)
		sem <- struct{}{}
		go func(r Resolver) {
			defer func() {
				<-sem
				wg.Done()
			}()
			result := scanResolver(r, testDomain)
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(r)
	}
	wg.Wait()

	return results
}

