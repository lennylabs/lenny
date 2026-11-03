. /b/prelude.sh
export SSL_CERT_FILE=/b/tls/ca.crt
go version; cd /tmp
for v in $VARIANTS; do setup $v
  r gotls-$v; GOPROXY=https://fake.test/t/gotls-$v GOSUMDB=off go mod download -x example.com/foo@v1.0.0 2>&1 | tail -2
done
setup home600
r gotls-goauthoff; GOAUTH=off GOPROXY=https://fake.test/t/gotls-goauthoff GOSUMDB=off go mod download example.com/foo@v1.0.0 2>&1 | tail -1
r gohttp-goinsecure; GOINSECURE=fake.test GOPROXY=http://fake.test/t/gohttp-goinsecure GOSUMDB=off go mod download example.com/foo@v1.0.0 2>&1 | tail -1
