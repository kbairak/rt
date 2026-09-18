.PHONY: build clean_logs

build:
	go build -o rt .

clean_logs:
	rm -f rt-*.log