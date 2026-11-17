U=http://fake.test/t
setup() {
  rm -f "$HOME/.netrc" /cfg/netrc; unset NETRC; mkdir -p /cfg
  L='machine fake.test login alice password s3cret'
  case $1 in
    home600) echo "$L" > "$HOME/.netrc"; chmod 600 "$HOME/.netrc";;
    home644) echo "$L" > "$HOME/.netrc"; chmod 644 "$HOME/.netrc";;
    env600)  echo "$L" > /cfg/netrc; chmod 600 /cfg/netrc; export NETRC=/cfg/netrc;;
    env644)  echo "$L" > /cfg/netrc; chmod 644 /cfg/netrc; export NETRC=/cfg/netrc;;
  esac
}
VARIANTS="home600 home644 env600 env644"
r() { echo "--- $*"; }
