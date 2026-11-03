. /b/own-prep.sh
for m in home env; do
  if [ $m = env ]; then mv /home/u/.netrc /home/u/.netrc.off; X="NETRC=/cfg/netrc"; else X="NOTHING=1"; fi
  echo "--- gotls-own$m"; setpriv --reuid=1000 --regid=1000 --clear-groups env HOME=/home/u GOPATH=/tmp/gp$m GOCACHE=/tmp/gc$m SSL_CERT_FILE=/b/tls/ca.crt GOPROXY=https://fake.test/t/gotls-own$m GOSUMDB=off $X sh -c 'cd /tmp && go mod download example.com/foo@v1.0.0' 2>&1 | tail -1
done
