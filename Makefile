.PHONY: build test clean

build:
	go build -o rt .

test:
	go test -count=1 -timeout 30s ./...

clean:
	rm -f rt