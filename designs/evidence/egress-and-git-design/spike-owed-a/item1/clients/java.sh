#!/bin/bash
. /i/clients/urls.sh
cd /tmp; for n in nc ncx ctl; do keytool -importcert -noprompt -alias $n -file /c/$n.pem -keystore /tmp/ts.p12 -storetype PKCS12 -storepass changeit 2>&1 | tail -1 >&2; done
cp /i/clients/Nc.java . ; javac Nc.java && java -Djavax.net.ssl.trustStore=/tmp/ts.p12 -Djavax.net.ssl.trustStorePassword=changeit -Djavax.net.ssl.trustStoreType=PKCS12 -cp . Nc $URLS
