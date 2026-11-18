.PHONY: build test run demo dist
build:
	CGO_ENABLED=0 go build -o susynth ./cmd/susynth
test:
	go vet ./... && CGO_ENABLED=1 go test -race ./...
run: build
	./susynth examples/first.syn
demo: build
	./susynth render -cycles 8 -o demo.wav examples/first.syn
dist:
	mkdir -p dist
	for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
	  GOOS=$${t%/*} GOARCH=$${t#*/} CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
	  -o dist/susynth-$${t%/*}-$${t#*/}$$([ $${t%/*} = windows ] && echo .exe) ./cmd/susynth || exit 1; done
