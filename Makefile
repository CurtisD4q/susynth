# Made from pmin/templates/program/Makefile, with susynth's own run and demo.
NAME := susynth
FILES :=
TARGETS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build test run demo install uninstall dist clean

build:
	CGO_ENABLED=0 go build -trimpath -o $(NAME) ./cmd/$(NAME)

test:
	go vet ./... && CGO_ENABLED=1 go test -race ./...

run: build
	./$(NAME) examples/first.syn

demo: build
	./$(NAME) render -cycles 8 -o demo.wav examples/first.syn

# Installs into ~/.local/bin; `make install FLAGS=--system` for /usr/local.
# Afterwards this folder can be deleted.
install: build
	./$(NAME) install $(FLAGS)

uninstall: build
	./$(NAME) uninstall $(FLAGS)

# One archive per target in dist/, with SHA256SUMS.
dist:
	NAME=$(NAME) FILES="$(FILES)" TARGETS="$(TARGETS)" sh scripts/dist.sh

clean:
	rm -rf $(NAME) dist
