. /b/prelude.sh
java -version 2>&1 | head -1
for v in $VARIANTS; do setup $v
  r javahttp-$v; java /b/Fetch.java $U/javahttp-$v/plain 2>&1 | tail -2
done
