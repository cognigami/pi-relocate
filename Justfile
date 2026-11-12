build:
    go build -o bin/relo ./cmd/relo

lint *paths:
    gofmt -w {{ if paths == "" { "cmd internal" } else { paths } }}
    go vet ./...

test *paths:
    go test -count=1 -v {{ if paths == "" { "./..." } else { "./" + replace(paths, " ", " ./") } }}
