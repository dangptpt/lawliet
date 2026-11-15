.PHONY: build run test race vet fmt clean

build:
	go build -o bin/lawliet ./cmd/lawliet

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

clean:
	rm -rf bin
