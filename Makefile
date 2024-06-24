build:
	go build

clean:
	rm -rf cronrunner
	rm -rf bin/cronrunner-*

install:
	go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest
	go install github.com/rakyll/gotest@latest
	go install golang.org/x/tools/cmd/deadcode@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.59.1

serve:
	go run main.go serve -c connections.yaml

build-macos:
	GOOS=darwin GOARCH=amd64 go build -o bin/cronrunner_darwin-amd64
	GOOS=darwin GOARCH=arm64 go build -o bin/cronrunner_darwin-arm64

build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/cronrunner_linux-amd64

build-windows:
	GOOS=windows GOARCH=amd64 go build -o bin/cronrunner_windows-amd64

build-all: clean build-macos build-linux build-windows

compress-linux:
	upx ./bin/cronrunner_linux*
