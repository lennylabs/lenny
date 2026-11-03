FROM alpine:3.22
RUN apk add --no-cache ruby ruby-dev build-base yaml-dev && gem install --no-document rack -v "~> 2.2.9" && gem install --no-document grape -v "~> 3.2"
