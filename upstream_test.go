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

func Test_httpUpstream_resolveHeaders(t *testing.T) {
	tests := []struct {
		name            string
		trustedProxies  []*net.IPNet
		remoteAddr      string
		url             string
		tls             bool
		originalHeaders map[string]string
		expectedXFF     string
		expectedProto   string
		expectedHost    string
		expectedPort    string
	}{
		{
			name: "untrusted proxy with TLS",
			trustedProxies: []*net.IPNet{
				mustParseCIDR("10.0.0.0/8"),
			},
			remoteAddr:    "1.2.3.4:12345",
			url:           "https://example.com:8443/test",
			tls:           true,
			expectedXFF:   "1.2.3.4",
			expectedProto: "https",
			expectedHost:  "example.com:8443",
			expectedPort:  "8443",
		},
		{
			name: "trusted proxy with existing XFF",
			trustedProxies: []*net.IPNet{
				mustParseCIDR("10.0.0.0/8"),
			},
			remoteAddr: "10.0.1.5:12345",
			url:        "http://example.com/test",
			tls:        false,
			originalHeaders: map[string]string{
				"X-Forwarded-For":   "5.6.7.8",
				"X-Forwarded-Proto": "http",
				"X-Forwarded-Host":  "example.com",
				"X-Forwarded-Port":  "80",
			},
			expectedXFF:   "5.6.7.8, 10.0.1.5",
			expectedProto: "http",
			expectedHost:  "example.com",
			expectedPort:  "80",
		},
		{
			name: "trusted proxy with invalid proto header",
			trustedProxies: []*net.IPNet{
				mustParseCIDR("10.0.0.0/8"),
			},
			remoteAddr: "10.0.1.6:12345",
			url:        "http://example.com/test",
			tls:        false,
			originalHeaders: map[string]string{
				"X-Forwarded-Proto": "ftp",
			},
			expectedXFF:   "10.0.1.6",
			expectedProto: "http",
			expectedHost:  "example.com",
			expectedPort:  "80",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up := &httpUpstream{
				log:            zap.NewNop(),
				trustedProxies: tt.trustedProxies,
			}

			orig, _ := http.NewRequest(http.MethodGet, tt.url, nil)
			orig.RemoteAddr = tt.remoteAddr

			if tt.tls {
				orig.TLS = &tls.ConnectionState{}
			}

			for k, v := range tt.originalHeaders {
				orig.Header.Set(k, v)
			}

			target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

			if err := up.resolveHeaders(target, orig); err != nil {
				t.Fatalf("resolveHeaders error: %v", err)
			}

			if xff := target.Header.Get("X-Forwarded-For"); xff != tt.expectedXFF {
				t.Errorf("X-Forwarded-For = %q; want %q", xff, tt.expectedXFF)
			}

			if proto := target.Header.Get("X-Forwarded-Proto"); proto != tt.expectedProto {
				t.Errorf("X-Forwarded-Proto = %q; want %q", proto, tt.expectedProto)
			}

			if host := target.Header.Get("X-Forwarded-Host"); host != tt.expectedHost {
				t.Errorf("X-Forwarded-Host = %q; want %q", host, tt.expectedHost)
			}

			if port := target.Header.Get("X-Forwarded-Port"); port != tt.expectedPort {
				t.Errorf("X-Forwarded-Port = %q; want %q", port, tt.expectedPort)
			}
		})
	}
}
