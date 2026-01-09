build:
	go build -o demovoice .

# Build for Linux VPS (amd64)
build-vps:
	GOOS=linux GOARCH=amd64 go build -o demovoice-linux .

# Build for Linux ARM64 (if VPS uses ARM)
build-vps-arm:
	GOOS=linux GOARCH=arm64 go build -o demovoice-linux-arm64 .

clean:
	rm -f demovoice demovoice-linux demovoice-linux-arm64

.PHONY: build build-vps build-vps-arm clean