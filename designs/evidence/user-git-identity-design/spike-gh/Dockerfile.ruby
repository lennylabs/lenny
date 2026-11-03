FROM alpine:3.22
RUN apk add --no-cache ruby ruby-dev build-base && gem install --no-document graphql graphql-c_parser
