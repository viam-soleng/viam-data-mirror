BINARY = bin/viam-data-mirror

.PHONY: build clean lint module

build: $(BINARY)

$(BINARY): $(shell find . -type f -name '*.go') go.mod go.sum
	go build -o $(BINARY) .

lint:
	go vet ./...

clean:
	rm -rf bin module.tar.gz

# Package the module for upload to the Viam registry.
module: build
	tar -czf module.tar.gz bin/viam-data-mirror meta.json
