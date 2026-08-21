build:
	@go build -o bin/fx-app ./cmd
run: build
	@./bin/fx-app
test:
	@go test -v ./...
