FROM golang:1.26
WORKDIR /src
COPY . .
RUN go mod tidy && go build ./... && go list -m all | grep -E 'aws-sdk-go-v2 |aws-sdk-go-v2/config|credentials |service/sts|service/dynamodb |oauth2|compute/metadata|azidentity|azcore |microsoft-authentication' > /versions.txt && go version >> /versions.txt
ENV GOFLAGS=-mod=mod GOPROXY=off
