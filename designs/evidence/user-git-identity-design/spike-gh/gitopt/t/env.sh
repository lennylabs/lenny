# env.sh: the pod's profile, as the design has it, plus test identifiers.
export HOME=${HOME_OVERRIDE:-/tmp/h}; mkdir -p $HOME
export HTTPS_PROXY=http://127.0.0.1:3128 https_proxy=http://127.0.0.1:3128 HTTP_PROXY=http://127.0.0.1:3128 NO_PROXY= no_proxy=
export SSL_CERT_FILE=/s/bundle.pem GH_NO_UPDATE_NOTIFIER=1 GH_PROMPT_DISABLED=1 GH_PAGER=cat PAGER=cat GH_CONFIG_DIR=$HOME/.gh GLAB_CONFIG_DIR=$HOME/.glab GLAB_CHECK_UPDATE=false GLAB_SEND_TELEMETRY=false GLAB_NO_PROMPT=1 NO_COLOR=1 GIT_TERMINAL_PROMPT=0
export PATH=/g:$PATH
