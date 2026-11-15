VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/tranbaohieu/lawliet/internal/version.Version=$(VERSION)
PYTHON  ?= python3

.PHONY: build run test race vet fmt fmt-check e2e bench docker clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/lawliet ./cmd/lawliet

run: build
	./bin/lawliet

test:
	go test ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Not gofmt'ed:"; gofmt -l .; exit 1)

# Requires python3 with redis and numpy (pip install -r examples/python/requirements.txt)
e2e:
	PYTHON=$(PYTHON) scripts/e2e/run_all.sh

bench: build
	$(PYTHON) scripts/bench/bench.py

docker:
	docker build --build-arg VERSION=$(VERSION) -t lawliet:$(VERSION) .

clean:
	rm -rf bin
