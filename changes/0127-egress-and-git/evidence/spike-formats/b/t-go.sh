. /b/prelude.sh
go version; echo "GOAUTH=$(go env GOAUTH)"
cd /tmp
for v in $VARIANTS; do setup $v
  r go-$v; GOPROXY=$U/go-$v GOSUMDB=off GOFLAGS=-mod=mod go mod download -x example.com/foo@v1.0.0 2>&1 | tail -3
done
