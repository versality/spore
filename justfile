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

# release X.Y.Z: bump VERSION, commit, and tag vX.Y.Z. Aborts on a
# dirty tree, a failing `just check`, or an existing tag. Does NOT
# push -- inspect the commit + tag, then `git push origin main vX.Y.Z`.
release VERSION:
    @if [ -z "$(echo {{VERSION}} | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$')" ]; then \
      echo "release: version must be X.Y.Z, got {{VERSION}}"; exit 2; \
    fi
    @if [ -n "$(git status --porcelain)" ]; then \
      echo "release: tree is dirty; commit or stash first"; \
      git status --short; exit 2; \
    fi
    @if git rev-parse --verify --quiet "v{{VERSION}}" >/dev/null; then \
      echo "release: tag v{{VERSION}} already exists"; exit 2; \
    fi
    just check
    echo "{{VERSION}}" > VERSION
    git add VERSION
    git commit -m "release: v{{VERSION}}"
    git tag -a "v{{VERSION}}" -m "v{{VERSION}}"
    @echo
    @echo "release: committed VERSION={{VERSION}} and tagged v{{VERSION}}"
    @echo "next:    git push origin main v{{VERSION}}"
