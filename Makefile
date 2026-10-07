BINARY := pst2eml
CMD := ./cmd/pst2eml
OUT := dist

.PHONY: build-linux build-windows build-darwin build-all clean test

build-linux:
	mkdir -p $(OUT)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(OUT)/$(BINARY)-linux-amd64 $(CMD)

build-windows:
	mkdir -p $(OUT)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o $(OUT)/$(BINARY)-windows-amd64.exe $(CMD)

build-darwin:
	mkdir -p $(OUT)
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o $(OUT)/$(BINARY)-darwin-arm64 $(CMD)

build-all: build-linux build-windows build-darwin

test:
	go test ./...

clean:
	rm -rf $(OUT)
