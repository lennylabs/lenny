# t9.py <candidate>: reproduces ugreview/t/test.sh with only its rendered git config replaced, and writes it to stdout.
import sys,re
s=open('/ug/test.sh').read(); c=sys.argv[1]
blocks={'T':'[http]\n    proxy = http://127.0.0.1:3128\n    sslCAInfo = /tmp/none.pem\n',
 'P':'[url "http://"]\n    insteadOf = https://\n[http]\n    proxy = http://127.0.0.1:3129\n'}
s2=re.sub(r'\[url "http://127\.0\.0\.1:3129/https/"\].*?insteadOf = http://127\.0\.0\.1:3129/\n',blocks[c],s,flags=re.S)
assert s2!=s
sys.stdout.write(s2)
