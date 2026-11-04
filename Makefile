.PHONY: build run test docker

build:
	go build -o bin/s3hog .

run:
	go run .

test:
	go test ./...

docker:
	docker build -t s3hog:latest .
