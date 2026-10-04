# Development shortcuts.
.PHONY: dev test scan

# Live reload: rebuild + restart on every .go change (needs `air`, see README).
dev:
	air

test:
	go vet ./... && go test -race ./...

# The same scans CI runs (.github/workflows/security.yml).
scan:
	go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
	go run github.com/securego/gosec/v2/cmd/gosec@v2.29.0 -quiet -exclude=G104 ./...
	go run github.com/zricethezav/gitleaks/v8@v8.30.1 detect --source . --no-banner --redact
