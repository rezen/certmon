FROM golang:1.14.2-buster
RUN mkdir -p $GOPATH/rezen/monitor-certs
COPY main.go $GOPATH/rezen/monitor-certs/main.go
RUN cd $GOPATH/rezen/monitor-certs/ && go get -d -v .
WORKDIR $GOPATH/rezen/monitor-certs
CMD go run main.go