#!/bin/bash


install () {
  mkdir -p $HOME/.interlink/logs || exit 1
  mkdir -p $HOME/.interlink/bin || exit 1
  mkdir -p $HOME/.interlink/config || exit 1
  # set $HOME/.interlink/config/InterLinkConfig.yaml

  cat <<EOF >>$HOME/.interlink/config/InterLinkConfig.yaml
InterlinkURL: "http://localhost"
InterlinkPort: "30080"
SidecarURL: "http://localhost"
SidecarPort: "4000"
VerboseLogging: true
ErrorsOnlyLogging: false
SbatchPath: "NOT NEEDED"
ScancelPath: "NOT NEEDED"
SqueuePath: "NOT NEEDED"
CommandPrefix: "NOT NEEDED"
ExportPodData: true
DataRootFolder: "NOT NEEDED"
ServiceAccount: "NOT NEEDED"
Namespace: "NOT NEEDED"
Tsocks: false
TsocksPath: "NOT NEEDED"
TsocksLoginNode: "NOT NEEDED"
BashPath: "NOT NEEDED"
EOF

  echo "=== Configured to reach sidecar service on http://localhost:4000 . You can edit this behavior changing $HOME/.interlink/config/InterLinkConfig.yaml file. ==="

  ## Download binaries to $HOME/.local/interlink/
  echo "curl --fail -L -o interlink.tar.gz https://github.com/intertwin-eu/interLink/releases/download/{{.InterLinkVersion}}/interLink_$(uname -s)_$(uname -m).tar.gz \
      && tar -xzvf interlink.tar.gz -C $HOME/.interlink/bin/"

  {
      {
          export INTERLINKCONFIGPATH=$HOME/interlink/config/InterLinkConfig.yaml
          curl --fail -L -o interlink.tar.gz https://github.com/intertwin-eu/interLink/releases/download/${VERSION}/interLink_$(uname -s)_$(uname -m).tar.gz
      } || {
          echo "Error downloading InterLink binaries, exiting..."
          exit 1
      }
  } && {
      {
          tar -xzvf interlink.tar.gz -C $HOME/.interlink/bin/
          mv $HOME/.interlink/bin/examples $HOME/.interlink/
      } || {
          echo "Error extracting InterLink binaries, exiting..."
          rm interlink.tar.gz
          exit 1
      }
  }
  rm interlink.tar.gz

  ## Download oauth2 proxy
  case "$OS" in
  Darwin)
      go install github.com/oauth2-proxy/oauth2-proxy/v7@latest
      ;;
  Linux)
      echo "https://github.com/oauth2-proxy/oauth2-proxy/releases/download/v7.4.0/oauth2-proxy-v7.4.0.${OS_LOWER}-$OSARCH.tar.gz"
      {
          {
              curl --fail -L -o oauth2-proxy-v7.4.0.$OS_LOWER-$OSARCH.tar.gz https://github.com/oauth2-proxy/oauth2-proxy/releases/download/v7.4.0/oauth2-proxy-v7.4.0.${OS_LOWER}-$OSARCH.tar.gz
          } || {
              echo "Error downloading OAuth binaries, exiting..."
              exit 1
          }
      } && {
          {
              tar -xzvf oauth2-proxy-v7.4.0.$OS_LOWER-$OSARCH.tar.gz -C $HOME/.local/interlink/bin/
          } || {
              echo "Error extracting OAuth binaries, exiting..."
              rm oauth2-proxy-v7.4.0.$OS_LOWER-$OSARCH.tar.gz
              exit 1
          }
      }
      
      rm oauth2-proxy-v7.4.0.$OS_LOWER-$OSARCH.tar.gz
      ;;
  esac

  if [ -f ${HOME}/.interlink/config/tls.key || -f ${HOME}/.interlink/config/tls.crt ]; then

    openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
      -keyout ${HOME}/.interlink/config/tls.key \
      -out ${HOME}/.interlink/config/tls.crt \
      -subj "/CN=interlink.demo"  -addext "subjectAltName=IP:{{.InterLinkURL}}"

  fi

}

start() {
  case "{{.OAUTH.Provider}}" in 
    oidc)
      $HOME/.local/interlink/bin/oauth2-proxy-v7.4.0.linux-$OSARCH/oauth2-proxy \
          --client-id DUMMY \
          --client-secret DUMMY \
          --http-address 0.0.0.0:{{.InterLinkPort}} \
          --oidc-issuer-url {{.OAUTH.Issuer}} \
          --pass-authorization-header true \
          --provider oidc \
          --redirect-url http://localhost:8081 \
          --oidc-extra-audience {{.OAUTH.Audience}} \
          --upstream localhost:30080 \
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
          --skip-jwt-bearer-tokens true > $HOME/.interlink/logs/oauth2-proxy.log 2>&1 &

      echo $! > $HOME/.local/interlink/oauth2-proxy.pid
      ;;
    github)
      $HOME/.local/interlink/bin/oauth2-proxy-v7.4.0.linux-$OSARCH/oauth2-proxy \
          --client-id {{.OAUTH.ClientID}} \
          --client-secret {{.OAUTH.ClientSecret}} \
          --http-address 0.0.0.0:{{.InterLinkPort}} \
          --pass-authorization-header true \
          --provider github \
          --redirect-url http://localhost:8081 \
          --upstream localhost:30080 \
          --validate-url {{.OAUTH.TokenURL}} \
          --email-domain=* \
          --github-org={{.OAUTH.GitHUBOrg}} \
          --cookie-secret 2ISpxtx19fm7kJlhbgC4qnkuTlkGrshY82L3nfCSKy4= \
          --skip-auth-route="*='*'" \
          --force-https \
          --https-address 0.0.0.0:{{.InterLinkPort}} \
          --tls-cert-file ${HOME}/.interlink/config/tls.crt \
          --tls-key-file ${HOME}/.interlink/config/tls.key \
          --skip-jwt-bearer-tokens true > $HOME/.interlink/logs/oauth2-proxy.log 2>&1 &

      echo $! > $HOME/.interlink/oauth2-proxy.pid
      ;;

  esac

  ## start interLink 
  $HOME/.interlink/bin/interlink &> $HOME/.interlink/logs/interlink.log &
  echo $! > $HOME/.interlink/interlink.pid

}

stop () {
    kill $(cat $HOME/.interlink/oauth2-proxy.pid)
    kill $(cat $HOME/.interlink/interlink.pid)
}

help () {
    echo -e "\n\ninstall:      Downloads InterLink and OAuth binaries, as well as InterLink configuration. Files are stored in $HOME/.local/interlink\n\n"
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
