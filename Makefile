REGISTRY ?= ghcr.io/potibm
IMAGE    ?= s3hog
TAG      ?= latest

.PHONY: build run test docker docker-build docker-push

build:
	go build -o bin/s3hog .

run:
	go run .

test:
	go test ./...

docker: docker-build

docker-build:
	docker build -t $(REGISTRY)/$(IMAGE):$(TAG) .

docker-push: docker-build
	docker push $(REGISTRY)/$(IMAGE):$(TAG)

# convenience: push latest
docker-release:
	$(MAKE) docker-build TAG=latest
	$(MAKE) docker-push TAG=latest
