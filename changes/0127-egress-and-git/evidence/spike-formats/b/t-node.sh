. /b/prelude.sh
node --version; npm --version
for v in $VARIANTS; do setup $v
  r npm-$v; npm view spikepkg --registry $U/npm-$v/ --fetch-retries=0 2>&1 | grep -E 'ERR!.*(code|40)' | head -2
  r nodefetch-$v; node -e 'fetch(process.argv[1]).then(r=>console.log(r.status)).catch(e=>console.log("EXC",e.message))' $U/nodefetch-$v/plain
done
