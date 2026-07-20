.PHONY: build test race vet vuln check tidy schema clean snapshot

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o bin/regbot ./cmd/regbot

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

vuln:
	govulncheck ./...

schema:
	python3 -m json.tool schema/regbot.v1.schema.json >/dev/null

tidy:
	go mod tidy

check: test vet schema
	test -z "$$(gofmt -l cmd internal pkg)"

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
