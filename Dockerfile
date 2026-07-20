# syntax=docker/dockerfile:1
FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/restayway/regbot/internal/cli.Version=${VERSION} -X github.com/restayway/regbot/internal/cli.Commit=${COMMIT} -X github.com/restayway/regbot/internal/cli.Date=${DATE}" \
    -o /regbot ./cmd/regbot

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /regbot /usr/local/bin/regbot
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/regbot"]
CMD ["--help"]
