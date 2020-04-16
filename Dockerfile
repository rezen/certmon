FROM golang:1.14.2-buster
RUN mkdir -p /go/src/github.com/rezen/certmon/cmd/certmon
WORKDIR /go/src/github.com/rezen/certmon

COPY *.go ./

RUN go get -d -v .
COPY ./cmd/certmon/*.go ./cmd/certmon
RUN go get -d -v ./cmd/certmon

RUN go install github.com/rezen/certmon/cmd/certmon
CMD go run cmd/certmon/main.go