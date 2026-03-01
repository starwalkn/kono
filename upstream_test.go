package kono

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"

	"go.uber.org/zap"
)

func mustParseCIDR(cidr string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}

	return ipnet
}

func TestResolveHeaders_UntrustedProxyTLS(t *testing.T) {
	up := &httpUpstream{
		log:            zap.NewNop(),
		trustedProxies: []*net.IPNet{mustParseCIDR("10.0.0.0/8")},
	}

	orig, _ := http.NewRequest(http.MethodGet, "https://example.com:8443/test", nil)
	orig.RemoteAddr = "1.2.3.4:12345"
	orig.TLS = &tls.ConnectionState{}

	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	if got := target.Header.Get("X-Forwarded-For"); got != "1.2.3.4" {
		t.Errorf("X-Forwarded-For = %q; want %q", got, "1.2.3.4")
	}
	if got := target.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Errorf("X-Forwarded-Proto = %q; want %q", got, "https")
	}
	if got := target.Header.Get("X-Forwarded-Host"); got != "example.com:8443" {
		t.Errorf("X-Forwarded-Host = %q; want %q", got, "example.com:8443")
	}
	if got := target.Header.Get("X-Forwarded-Port"); got != "8443" {
		t.Errorf("X-Forwarded-Port = %q; want %q", got, "8443")
	}
	if got := target.Header.Get("Forwarded"); got != "for=1.2.3.4; proto=https; host=example.com:8443" {
		t.Errorf("Forwarded = %q; want %q", got, "for=1.2.3.4; proto=https; host=example.com:8443")
	}
}

func TestResolveHeaders_TrustedProxyWithXFF(t *testing.T) {
	up := &httpUpstream{
		log:            zap.NewNop(),
		trustedProxies: []*net.IPNet{mustParseCIDR("10.0.0.0/8")},
	}

	orig, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	orig.RemoteAddr = "10.0.1.5:12345"
	orig.Header.Set("X-Forwarded-For", "5.6.7.8")
	orig.Header.Set("X-Forwarded-Proto", "http")
	orig.Header.Set("X-Forwarded-Host", "example.com")
	orig.Header.Set("X-Forwarded-Port", "80")

	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	if got := target.Header.Get("X-Forwarded-For"); got != "5.6.7.8, 10.0.1.5" {
		t.Errorf("X-Forwarded-For = %q; want %q", got, "5.6.7.8, 10.0.1.5")
	}
	if got := target.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q; want %q", got, "http")
	}
	if got := target.Header.Get("X-Forwarded-Host"); got != "example.com" {
		t.Errorf("X-Forwarded-Host = %q; want %q", got, "example.com")
	}
	if got := target.Header.Get("X-Forwarded-Port"); got != "80" {
		t.Errorf("X-Forwarded-Port = %q; want %q", got, "80")
	}
	if got := target.Header.Get("Forwarded"); got != "for=10.0.1.5; proto=http; host=example.com" {
		t.Errorf("Forwarded = %q; want %q", got, "for=10.0.1.5; proto=http; host=example.com")
	}
}

func TestResolveHeaders_TrustedProxyInvalidProto(t *testing.T) {
	up := &httpUpstream{
		log:            zap.NewNop(),
		trustedProxies: []*net.IPNet{mustParseCIDR("10.0.0.0/8")},
	}

	orig, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	orig.RemoteAddr = "10.0.1.6:12345"
	orig.Header.Set("X-Forwarded-Proto", "ftp")

	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	if got := target.Header.Get("X-Forwarded-For"); got != "10.0.1.6" {
		t.Errorf("X-Forwarded-For = %q; want %q", got, "10.0.1.6")
	}
	if got := target.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q; want %q", got, "http")
	}
	if got := target.Header.Get("X-Forwarded-Host"); got != "example.com" {
		t.Errorf("X-Forwarded-Host = %q; want %q", got, "example.com")
	}
	if got := target.Header.Get("X-Forwarded-Port"); got != "80" {
		t.Errorf("X-Forwarded-Port = %q; want %q", got, "80")
	}
	if got := target.Header.Get("Forwarded"); got != "for=10.0.1.6; proto=http; host=example.com" {
		t.Errorf("Forwarded = %q; want %q", got, "for=10.0.1.6; proto=http; host=example.com")
	}
}
