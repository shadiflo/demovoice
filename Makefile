build:
	go build -o demovoice .

clean:
	rm -f demovoice

.PHONY: build clean