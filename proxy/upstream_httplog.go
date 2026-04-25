package proxy

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"proxy-llm/logger"
)

const defaultUpstreamLogMaxBytes = 256 * 1024

func (p *Proxy) shouldLogUpstreamHTTP() bool {
	return p.config != nil && p.config.Logging.UpstreamHTTPlog
}

func (p *Proxy) upstreamLogMax() int {
	if p.config == nil {
		return defaultUpstreamLogMaxBytes
	}
	n := p.config.Logging.UpstreamLogMaxBytes
	if n > 0 {
		return n
	}
	return defaultUpstreamLogMaxBytes
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... [truncated, total " + strconv.Itoa(len(s)) + " bytes]"
}

func redactHeaderValue(name, value string) string {
	ln := strings.ToLower(name)
	if ln == "authorization" {
		low := strings.ToLower(value)
		if strings.HasPrefix(low, "bearer ") {
			return "Bearer <redacted len=" + strconv.Itoa(len(value)) + ">"
		}
		return "<redacted len=" + strconv.Itoa(len(value)) + ">"
	}
	if ln == "x-api-key" {
		return "<redacted len=" + strconv.Itoa(len(value)) + ">"
	}
	return value
}

// formatHeaderLines prints headers with stable key order, one per line
func formatHeaderLines(h http.Header) string {
	if h == nil {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(&b, "  %s: %s\n", k, redactHeaderValue(k, v))
		}
	}
	return b.String()
}

// logHTTPClientToProxy logs the incoming request from the user agent to the proxy
func (p *Proxy) logHTTPClientToProxy(l *logger.Logger, intro string, r *http.Request, body []byte) {
	if !p.shouldLogUpstreamHTTP() {
		return
	}
	b := string(body)
	b = truncateForLog(b, p.upstreamLogMax())
	l.Info("%s [client->proxy] %s %s from %s\n  Query: %s\n  Headers:\n%s  Body (%d bytes):\n%s",
		intro, r.Method, r.URL.RequestURI(), r.RemoteAddr, r.URL.RawQuery, formatHeaderLines(r.Header), len(body), b,
	)
}

// logHTTPOutgoingUpstream logs the HTTP request the proxy is about to send to the model backend
func (p *Proxy) logHTTPOutgoingUpstream(l *logger.Logger, callID string, r *http.Request, body []byte) {
	if !p.shouldLogUpstreamHTTP() {
		return
	}
	if r == nil {
		return
	}
	b := string(body)
	b = truncateForLog(b, p.upstreamLogMax())
	l.Info("HTTP upstream request [call_id=%s] [proxy->LLM] %s %s\n  Headers:\n%s  Body (%d bytes):\n%s",
		callID, r.Method, r.URL.String(), formatHeaderLines(r.Header), len(body), b,
	)
}

// logHTTPUpstreamResponseFull logs status, headers and full body after a non-streaming read
func (p *Proxy) logHTTPUpstreamResponseFull(l *logger.Logger, callID string, resp *http.Response, body []byte) {
	if !p.shouldLogUpstreamHTTP() || resp == nil {
		return
	}
	b := string(body)
	b = truncateForLog(b, p.upstreamLogMax())
	l.Info("HTTP upstream response [call_id=%s] [LLM->proxy] %s (status %d, %d bytes body)\n  Headers:\n%s  Body:\n%s",
		callID, resp.Status, resp.StatusCode, len(body), formatHeaderLines(resp.Header), b,
	)
}

// logHTTPUpstreamResponseStreamMeta logs the response line and headers; stream body is not materialized
func (p *Proxy) logHTTPUpstreamResponseStreamMeta(l *logger.Logger, callID string, resp *http.Response) {
	if !p.shouldLogUpstreamHTTP() || resp == nil {
		return
	}
	l.Info("HTTP upstream response (streaming) [call_id=%s] [LLM->proxy] %s — body is SSE, read and forwarded chunkwise (not fully buffered here)\n  Headers:\n%s",
		callID, resp.Status, formatHeaderLines(resp.Header),
	)
}
