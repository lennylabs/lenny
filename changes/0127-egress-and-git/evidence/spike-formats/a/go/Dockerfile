FROM golang:1.26 AS b
WORKDIR /src
COPY main.go .
RUN go mod init spike && go get github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/service/s3 && CGO_ENABLED=0 go build -o /loop . && go list -m all | grep -E 'aws-sdk-go-v2 |/config |/service/s3 |credentials ' > /versions.txt
FROM alpine:3.22
COPY --from=b /loop /loop
COPY --from=b /versions.txt /versions.txt
