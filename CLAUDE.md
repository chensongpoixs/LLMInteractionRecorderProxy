# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**proxy-llm** - A Go-based LLM API proxy service that forwards requests to large language model providers (OpenAI, Anthropic, etc.) and logs all request/response data to local files in dataset format.

## Architecture

```
Client ──► proxy-llm (Go HTTP server) ──► LLM Provider API
                    │
                    ▼
              Local Storage (JSONL files)
```

### Key Packages

| Package | Path | Responsibility |
|---------|------|----------------|
| `main` | `cmd/server/main.go` | Entry point, CLI flags, graceful shutdown |
| `config` | `config/config.go` | Configuration loading from YAML + env vars |
| `proxy` | `proxy/proxy.go` | Core HTTP proxy logic, request forwarding, streaming |
| `storage` | `storage/storage.go` | JSONL file logging for request/response pairs |
| `metrics` | `proxy/metrics.go` | Request metrics collection and HTTP exposure |

## Development Commands

```bash
# Build
GOPROXY=https://goproxy.cn,direct go build -o proxy-llm ./cmd/server

# Run
./proxy-llm --config config.yaml

# Run with verbose output
./proxy-llm --config config.yaml --version

# Test (when tests are added)
go test ./...

# Format check
go fmt ./...
go vet ./...
```

## Configuration

Edit `config.yaml` to set up your LLM providers. Key settings:

- `models[]`: Array of LLM providers (name, base_url, api_key, model)
- `server.port`: HTTP listen port (default: 8080)
- `storage.directory`: Where to save request/response logs (default: ./data)
- `proxy.enable_stream`: Enable SSE streaming responses
- `proxy.enable_auth`: Enable API key authentication

Environment variable overrides:
- `PROXY_PORT` - Server port
- `PROXY_STORAGE_DIR` - Storage directory

## Data Storage Format

Request/response pairs are saved as **JSONL** (one JSON object per line):

```
data/
├── session_gpt-4/
│   ├── session_gpt-4_20240101_120000.jsonl
│   └── ...
├── session_claude/
│   └── ...
└── streams/
    └── session_gpt-4_20240101.jsonl  (streaming chunks)
```

Each JSONL line contains: id, timestamp, session_id, endpoint, method, model, provider, request_body, status_code, response_body, stream (bool), duration, error (optional), tokens_used (optional).

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/chat/completions` | POST | Chat completions (proxied) |
| `/v1/completions` | POST | Text completions (proxied) |
| `/v1/embeddings` | POST | Embeddings (proxied) |
| `/health` | GET | Health check |
| `/metrics` | GET | Prometheus-style metrics |

## Key Implementation Details

1. **Proxy Logic**: Reads full request body, creates new HTTP request to target API, forwards response back to client
2. **Streaming**: Uses `http.ResponseWriter.Flusher` interface to forward SSE chunks in real-time
3. **Concurrency**: `sync.Mutex` protects file writes in storage layer; each model has its own HTTP client
4. **Graceful Shutdown**: Listens for SIGINT/SIGTERM, calls `http.Server.Shutdown()` with timeout
5. **Config**: YAML file with environment variable overrides (e.g., `${OPENAI_API_KEY}`)
