.PHONY: build run test test-coverage clean

build:
	go build -o app ./cmd/app/

run:
	go run ./cmd/app/

test:
	go test ./...

test-coverage:
	go test -cover ./...

clean:
	rm -f app cache.db
