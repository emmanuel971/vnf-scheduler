FROM golang:1.23 as builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV GOOS=linux GOARCH=amd64 CGO_ENABLED=0
RUN go build -o scheduler ./cmd

# Final image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/scheduler /usr/local/bin/scheduler

ENTRYPOINT ["/usr/local/bin/scheduler"]

