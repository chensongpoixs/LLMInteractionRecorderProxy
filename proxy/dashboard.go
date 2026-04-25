package proxy

import (
	"embed"
	"net/http"
)

//go:embed web/usage.html
var dashboardFS embed.FS

func (p *Proxy) handleUsageDashboard(w http.ResponseWriter, r *http.Request) {
	data, err := dashboardFS.ReadFile("web/usage.html")
	if err != nil {
		http.Error(w, "dashboard not available", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

