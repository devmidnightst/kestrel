# Builder image for kestrel programs: the kestrel compiler plus gcc and musl.
#
#   docker build -t kestrel .
#   docker run --rm -v "$PWD":/app kestrel build --release --static main.ks -o server
#
# See docs/deploy.md for a two-stage app image that ships only the binary.
FROM golang:1.24-alpine3.21 AS compiler
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/devmidnightst/kestrel/internal/cli.Version=${VERSION}" \
    -o /out/kestrel ./cmd/kestrel

FROM alpine:3.21
RUN apk add --no-cache gcc musl-dev
COPY --from=compiler /out/kestrel /usr/local/bin/kestrel
WORKDIR /app
ENTRYPOINT ["kestrel"]
CMD ["help"]
