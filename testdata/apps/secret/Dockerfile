FROM golang:1-alpine AS build
WORKDIR /src
RUN --mount=type=secret,id=TOKEN sh -c 'test "$(cat /run/secrets/TOKEN)" = s3cr3t-build-value'
COPY main.go .
RUN go mod init hello && CGO_ENABLED=0 go build -o /hello .

FROM alpine:3
WORKDIR /app
COPY --from=build /hello /app/hello
COPY MODE VERSION /app/
CMD ["/app/hello"]
