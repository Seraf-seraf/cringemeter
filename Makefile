GO ?= go

.PHONY: test vet

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
