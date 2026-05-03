set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

default:
    @just --list

check: fmt-check lint test vuln nix-check

fmt:
    gofmt -w $(find . -name '*.go' -not -path './.git/*' -not -path './.worktrees/*' | sort)
    nixpkgs-fmt $(find . -name '*.nix' -not -path './.git/*' -not -path './.worktrees/*' | sort)

fmt-check:
    @unformatted="$(gofmt -l $(find . -name '*.go' -not -path './.git/*' -not -path './.worktrees/*' | sort))"; \
    if [ -n "$unformatted" ]; then \
      printf 'gofmt needed:\n%s\n' "$unformatted"; \
      exit 1; \
    fi
    nixpkgs-fmt --check $(find . -name '*.nix' -not -path './.git/*' -not -path './.worktrees/*' | sort)

lint:
    go vet ./...
    golangci-lint run ./...
    go run ./cmd/spore lint

test:
    go test ./...

coverage:
    mkdir -p coverage
    go test -covermode=atomic -coverprofile=coverage/coverage.out ./...
    go tool cover -func=coverage/coverage.out | tee coverage/coverage.txt

vuln:
    govulncheck ./...

nix-check:
    nix flake check

build: go-build nix-build

go-build:
    mkdir -p build
    go build -trimpath -o build/spore ./cmd/spore

nix-build:
    nix build .
