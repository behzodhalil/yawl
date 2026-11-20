FROM golang:1-alpine AS build
WORKDIR /src
COPY main.go .
RUN go mod init hello && CGO_ENABLED=0 go build -o /hello .

FROM alpine:3
WORKDIR /app
COPY --from=build /hello /app/hello
COPY MODE VERSION /app/
CMD ["/app/hello"]
