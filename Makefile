.PHONY: build build-all container publish release clean test lint tag

build:
	./build.sh binary

build-all:
	./build.sh binary-all

container:
	./build.sh container

publish:
	./build.sh publish

release:
	./build.sh release

clean:
	./build.sh clean

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...

# Create and push a version tag (e.g. make tag VERSION=v1.2.3)
tag:
	@[ "${VERSION}" ] || ( echo "VERSION is required: make tag VERSION=v1.2.3"; exit 1 )
	git tag -a ${VERSION} -m "Release ${VERSION}"
	git push origin ${VERSION}
	@echo "✓ Tagged ${VERSION} and pushed"
