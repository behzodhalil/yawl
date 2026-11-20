FROM golang:1-alpine AS build
WORKDIR /src
COPY main.go .
RUN go mod init pgnote && go get github.com/jackc/pgx/v5@v5.11.0 && CGO_ENABLED=0 go build -o /pgnote .

FROM alpine:3
COPY --from=build /pgnote /pgnote
CMD ["/pgnote"]
