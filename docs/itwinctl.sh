#!/bin/bash

#export INTERLINKCONFIGPATH="$PWD/kustomizations/InterLinkConfig.yaml"

VERSION="${VERSION:-0.0.5}"

SIDECAR="${SIDECAR:-slurm}"

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
#echo $OS_LOWER

OIDC_ISSUER="${OIDC_ISSUER:-https://dodas-iam.cloud.cnaf.infn.it/}"
AUTHORIZED_GROUPS="${AUTHORIZED_GROUPS:-intw}"
AUTHORIZED_AUD="${AUTHORIZED_AUD:-intertw-vk}"
API_HTTP_PORT="${API_HTTP_PORT:-8080}"
API_HTTPS_PORT="${API_HTTPS_PORT:-443}"
export INTERLINKPORT="${INTERLINKPORT:-3000}"
export INTERLINKURL="${INTERLINKURL:-http://0.0.0.0}"
export INTERLINKPORT="${INTERLINKPORT:-3000}"
export INTERLINKURL="${INTERLINKURL:-http://0.0.0.0}"
export INTERLINKCONFIGPATH="${INTERLINKCONFIGPATH:-$HOME/.config/interlink/InterLinkConfig.yaml}"
export SBATCHPATH="${SBATCHPATH:-/usr/bin/sbatch}"
export SCANCELPATH="${SCANCELPATH:-/usr/bin/scancel}"


install () {
    mkdir -p $HOME/.local/interlink/logs || exit 1
    mkdir -p $HOME/.local/interlink/bin || exit 1
    mkdir -p $HOME/.config/interlink/ || exit 1
    # download interlinkpath in $HOME/.config/interlink/InterLinkConfig.yaml
    curl -o $HOME/.config/interlink/InterLinkConfig.yaml https://raw.githubusercontent.com/intertwin-eu/interLink/main/kustomizations/InterLinkConfig.yaml

    ## Download binaries to $HOME/.local/interlink/bin
    curl -L -o interlink.tar.gz https://github.com/intertwin-eu/interLink/releases/download/v${VERSION}/interLink_${VERSION}_${OS}_$(uname -m).tar.gz \
        && tar -xzvf interlink.tar.gz -C $HOME/.local/interlink/bin/
    rm interlink.tar.gz

    ## Download oauth2 proxy
    case "$OS" in
    Darwin)
        go install github.com/oauth2-proxy/oauth2-proxy/v7@latest
        ;;
    Linux)
        echo "https://github.com/oauth2-proxy/oauth2-proxy/releases/download/v7.4.0/oauth2-proxy-v7.4.0.${OS_LOWER}-$OSARCH.tar.gz"
        curl -L -o oauth2-proxy-v7.4.0.$OS_LOWER-$OSARCH.tar.gz https://github.com/oauth2-proxy/oauth2-proxy/releases/download/v7.4.0/oauth2-proxy-v7.4.0.${OS_LOWER}-$OSARCH.tar.gz
        tar -xzvf oauth2-proxy-v7.4.0.$OS_LOWER-$OSARCH.tar.gz -C $HOME/.local/interlink/bin/
        rm oauth2-proxy-v7.4.0.$OS_LOWER-$OSARCH.tar.gz
        ;;
    esac

}

start () {
    ## Set oauth2 proxy config
    $HOME/.local/interlink/bin/oauth2-proxy-v7.4.0.linux-$OSARCH/oauth2-proxy \
        --client-id DUMMY \
        --client-secret DUMMY \
        --http-address http://0.0.0.0:$API_HTTP_PORT \
        --oidc-issuer-url $OIDC_ISSUER \
        --pass-authorization-header true \
        --provider oidc \
        --redirect-url http://localhost:8081 \
        --oidc-extra-audience intertw-vk \
        --upstream	$INTERLINKURL:$INTERLINKPORT \
        --allowed-group $AUTHORIZED_GROUPS \
        --validate-url ${OIDC_ISSUER}token \
        --oidc-groups-claim groups \
        --email-domain=* \
        --cookie-secret 2ISpxtx19fm7kJlhbgC4qnkuTlkGrshY82L3nfCSKy4= \
        --skip-auth-route="*='*'" \
        --skip-jwt-bearer-tokens true &> $HOME/.local/interlink/logs/oauth2-proxy.log &
        # --https-address http://0.0.0.0:$API_HTTPS_PORT \
        # --tls-cert-file $HOME/.local/interlink/cert.pem \
        # --tls-key-file $HOME/.local/interlink/key.pem \

    echo $! > $HOME/.local/interlink/oauth2-proxy.pid

    ## start link and sidecar

    $HOME/.local/interlink/bin/interlink &> $HOME/.local/interlink/logs/interlink.log &
    echo $! > $HOME/.local/interlink/interlink.pid

    case "$SIDECAR" in
    slurm)
        $HOME/.local/interlink/bin/interlink-sidecar-slurm  &> $HOME/.local/interlink/logs/sd.log &
        echo $! > $HOME/.local/interlink/sd.pid
        ;;
    docker)
        $HOME/.local/interlink/bin/interlink-sidecar-docker  &> $HOME/.local/interlink/logs/sd.log &
        echo $! > $HOME/.local/interlink/sd.pid
        ;;
    esac
}

stop () {
    kill $(cat $HOME/.local/interlink/oauth2-proxy.pid)
    kill $(cat $HOME/.local/interlink/interlink.pid)
    kill $(cat $HOME/.local/interlink/slurm-sd.pid)
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
    uninstall)
        rm -r $HOME/.local/interlink
esac