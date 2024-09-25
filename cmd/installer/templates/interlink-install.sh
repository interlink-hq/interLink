#!/bin/bash

OS=$(uname -s)

case "$OS" in
    Darwin)
        OS=MacOS
        ;;
esac

OSARCH=$(uname -m)
case "$OSARCH" in
    x86_64)
        OSARCH=amd64
        ;;
esac


#echo $OS

OS_LOWER=$(uname -s  | tr '[:upper:]' '[:lower:]')

install () {
  mkdir -p $HOME/.interlink/logs || exit 1
  mkdir -p $HOME/.interlink/bin || exit 1
  mkdir -p $HOME/.interlink/config || exit 1
  # set $HOME/.interlink/config/InterLinkConfig.yaml

  cat <<EOF >>$HOME/.interlink/config/InterLinkConfig.yaml
InterlinkAddress: "unix://${HOME}/.interlink/interlink.sock"
InterlinkPort: "0"
SidecarURL: "http://localhost"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
ExportPodData: true
DataRootFolder: "~/.interlink"
EOF

  echo "=== Configured to reach sidecar service on http://localhost:4000 . You can edit this behavior changing $HOME/.interlink/config/InterLinkConfig.yaml file. ==="

  ## Download binaries to $HOME/.local/interlink/
  echo "curl --fail -L -o ${HOME}/.interlink/bin/interlink https://github.com/interTwin-eu/interLink/releases/download/{{.InterLinkVersion}}/interlink_$(uname -s)_$(uname -m)"

  {
      {
          curl --fail -L -o ${HOME}/.interlink/bin/interlink https://github.com/interTwin-eu/interLink/releases/download/{{.InterLinkVersion}}/interlink_$(uname -s)_$(uname -m) 
          chmod +x ${HOME}/.interlink/bin/interlink
      } || {
          echo "Error downloading InterLink binaries, exiting..."
          exit 1
      }
  } 

  ## Download oauth2 proxy
  case "$OS" in
  Darwin)
      go install github.com/oauth2-proxy/oauth2-proxy/v7@latest
      ;;
  Linux)
      echo "https://github.com/oauth2-proxy/oauth2-proxy/releases/download/v7.6.0/oauth2-proxy-v7.6.0.${OS_LOWER}-$OSARCH.tar.gz"
      {
          {
              curl --fail -L -o ${HOME}/.interlink/bin/oauth2-proxy https://github.com/dciangot/oauth2-proxy/releases/download/v0.0.3/oauth2-proxy_${OS}_$OSARCH
              chmod +x ${HOME}/.interlink/bin/oauth2-proxy
          } || {
              echo "Error downloading OAuth binaries, exiting..."
              exit 1
          }
      }

      ;;
  esac

  if [[ ! -f ${HOME}/.interlink/config/tls.key || ! -f ${HOME}/.interlink/config/tls.crt ]]; then

    openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
      -keyout ${HOME}/.interlink/config/tls.key \
      -out ${HOME}/.interlink/config/tls.crt \
      -subj "/CN=interlink.demo"  -addext "subjectAltName=IP:{{.InterLinkIP}}"

  fi

}

start() {
  case "{{.OAUTH.Provider}}" in 
    oidc)
      $HOME/.interlink/bin/oauth2-proxy \
          --client-id "{{.OAUTH.ClientID}}" \
          --client-secret "\"{{.OAUTH.ClientSecret}}\"" \
          --http-address unix://${HOME}/.interlink/interlink.sock \
          --oidc-issuer-url "{{.OAUTH.Issuer}}" \
          --pass-authorization-header true \
          --provider oidc \
          --redirect-url http://localhost:8081 \
          --oidc-extra-audience {{.OAUTH.Audience}} \
          --upstream http://localhost:30080 \
          --allowed-group {{.OAUTH.Group}} \
          --validate-url {{.OAUTH.TokenURL}} \
          --oidc-groups-claim {{.OAUTH.GroupClaim}} \
          --email-domain=* \
          --cookie-secret 2ISpxtx19fm7kJlhbgC4qnkuTlkGrshY82L3nfCSKy4= \
          --skip-auth-route="*='*'" \
          --force-https \
          --https-address 0.0.0.0:{{.InterLinkPort}} \
          --tls-cert-file ${HOME}/.interlink/config/tls.crt \
          --tls-key-file ${HOME}/.interlink/config/tls.key \
          --tls-cipher-suite=TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,TLS_RSA_WITH_AES_128_CBC_SHA,TLS_RSA_WITH_AES_128_GCM_SHA256,TLS_RSA_WITH_AES_256_CBC_SHA,TLS_RSA_WITH_AES_256_GCM_SHA384 \
          --skip-jwt-bearer-tokens true > $HOME/.interlink/logs/oauth2-proxy.log 2>&1 &

      echo $! > $HOME/.interlink/oauth2-proxy.pid
      ;;
    github)
      $HOME/.interlink/bin/oauth2-proxy \
          --client-id {{.OAUTH.ClientID}} \
          --client-secret {{.OAUTH.ClientSecret}} \
          --http-address 0.0.0.0:{{.InterLinkPort}} \
          --pass-authorization-header true \
          --provider github \
          --redirect-url http://localhost:8081 \
          --upstream http://localhost:30080 \
          --email-domain="*" \
          --github-user="{{.OAUTH.GitHUBUser}}" \
          --cookie-secret 2ISpxtx19fm7kJlhbgC4qnkuTlkGrshY82L3nfCSKy4= \
          --skip-auth-route="*='*'" \
          --force-https \
          --https-address 0.0.0.0:{{.InterLinkPort}} \
          --tls-cert-file ${HOME}/.interlink/config/tls.crt \
          --tls-key-file ${HOME}/.interlink/config/tls.key \
          --tls-cipher-suite=TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,TLS_RSA_WITH_AES_128_CBC_SHA,TLS_RSA_WITH_AES_128_GCM_SHA256,TLS_RSA_WITH_AES_256_CBC_SHA,TLS_RSA_WITH_AES_256_GCM_SHA384 \
          --skip-jwt-bearer-tokens true > $HOME/.interlink/logs/oauth2-proxy.log 2>&1 &

      echo $! > $HOME/.interlink/oauth2-proxy.pid
      ;;

  esac

  ## start interLink 
  export INTERLINKCONFIGPATH=$HOME/.interlink/config/InterLinkConfig.yaml
  $HOME/.interlink/bin/interlink &> $HOME/.interlink/logs/interlink.log &
  echo $! > $HOME/.interlink/interlink.pid

}

stop () {
    kill $(cat $HOME/.interlink/oauth2-proxy.pid)
    kill $(cat $HOME/.interlink/interlink.pid)
}

help () {
    echo -e "\n\ninstall:      Downloads InterLink and OAuth binaries, as well as InterLink configuration. Files are stored in $HOME/.interlink\n\n"
    echo -e "start:        Starts the OAuth proxy, the InterLink API.\n"
    echo -e "stop:         Kills all the previously started processes\n\n"
    echo -e "restart:      Kills all started processes and start them again\n\n"
    echo -e "help:         Shows this command list"
}

case "$1" in
    install)
        install
        ;;
    start) 
        start
        ;;
    stop)
        stop
        ;;
    restart)
        stop
        start
        ;;
    help)
        help
        ;;
    *)
        echo -e "You need to specify one of the following commands:"
        help
        ;;
esac
