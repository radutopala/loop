.PHONY: build test lint coverage coverage-check docker-build run clean

build:
	go build -o bin/loop ./cmd/loop

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

coverage:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

coverage-check:
	go test -race -count=1 -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//' | \
		awk '{if ($$1 < 100.0) {print "Coverage is " $$1 "%, required 100%"; exit 1} else {print "Coverage: " $$1 "%"}}'

docker-build:
	docker build -t loop-agent -f container/Dockerfile .

run: build
	./bin/loop serve

clean:
	rm -rf bin/ coverage.out coverage.html
