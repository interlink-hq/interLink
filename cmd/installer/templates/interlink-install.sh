#!/bin/bash


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

# TODO: generate certificates for {{.InterLinkURL}} in $HOME/.interlink/config/

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

    echo $! > $HOME/.local/interlink/oauth2-proxy.pid
    ;;

esac

## start interLink 
$HOME/.local/interlink/bin/interlink &> $HOME/.local/interlink/logs/interlink.log &
echo $! > $HOME/.local/interlink/interlink.pid

