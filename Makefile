TEST_CMD := go test -count=1 -cover -race -v -timeout 60s ./...

test: deps
	$(TEST_CMD) -short

deps:
	go mod download

lint: lint-deps
	golangci-lint run

lint-deps:
	@which gometalinter > /dev/null || \
		(curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b ./bin/dependencies v1.43.0)