#!/bin/bash


VERSION="${VERSION:-0.0.4-pre5}"

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
API_HTTPS_PORT="${API_HTTPS_PORT:-30443}"
export HOSTCERT="${HOSTCERT:-/home/ciangottinid/EasyRSA-3.1.5/pki/issued/intertwin.crt}"
export HOSTKEY="${HOSTKEY:-/home/ciangottinid/EasyRSA-3.1.5/pki/private/InterTwin.key}"
export INTERLINKPORT="${INTERLINKPORT:-30444}"
export INTERLINKURL="${INTERLINKURL:-http://0.0.0.0}"
export INTERLINKCONFIGPATH="${INTERLINKCONFIGPATH:-$HOME/InterLinkConfig.yaml}"
export SBATCHPATH="${SBATCHPATH:-/usr/bin/sbatch}"
export SCANCELPATH="${SCANCELPATH:-/usr/bin/scancel}"


install () {
    mkdir -p $HOME/.local/interlink/logs || exit 1
    mkdir -p $HOME/.local/interlink/bin || exit 1
    mkdir -p $HOME/.local/interlink/config || exit 1
    # download interlinkpath in $HOME/.config/interlink/InterLinkConfig.yaml
    if test -f $HOME/.local/interlink/config/InterLinkConfig.yaml; then
        echo -e "The InterLink config already exists. Skipping its downloading\n"
    else 
        {
            {
                curl --fail -o $HOME/.local/interlink/config/InterLinkConfig.yaml https://raw.githubusercontent.com/interTwin-eu/interLink/main/examples/interlink-slurm/vk/InterLinkConfig.yaml
            } || {
                echo "Error downloading InterLink config, exiting..."
                exit 1
            }
        }
    fi

    ## Download binaries to $HOME/.local/interlink/
    echo "curl --fail -L -o interlink.tar.gz https://github.com/intertwin-eu/interLink/releases/download/${VERSION}/interLink_$(uname -s)_$(uname -m).tar.gz \
        && tar -xzvf interlink.tar.gz -C $HOME/.local/interlink/bin/"
    
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
            tar -xzvf interlink.tar.gz -C $HOME/.local/interlink/bin/
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

}

start () {
    ## Set oauth2 proxy config
    $HOME/.local/interlink/bin/oauth2-proxy-v7.4.0.linux-$OSARCH/oauth2-proxy \
        --client-id DUMMY \
        --client-secret DUMMY \
        --http-address 0.0.0.0:$API_HTTP_PORT \
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
	    --force-https \
        --https-address 0.0.0.0:$API_HTTPS_PORT \
        --tls-cert-file ${HOSTCERT} \
        --tls-key-file ${HOSTKEY} \
        --skip-jwt-bearer-tokens true > $HOME/.local/interlink/logs/oauth2-proxy.log 2>&1 &

    echo $! > $HOME/.local/interlink/oauth2-proxy.pid

    ## start link and sidecar

    $HOME/.local/interlink/bin/interlink &> $HOME/.local/interlink/logs/interlink.log &
    echo $! > $HOME/.local/interlink/interlink.pid

    case "$SIDECAR" in
    slurm)
        SHARED_FS=true $HOME/.local/interlink/bin/interlink-sidecar-slurm  &> $HOME/.local/interlink/logs/slurm-sidecar.log &
        echo $! > $HOME/.local/interlink/sd.pid
        ;;
    docker)
        $HOME/.local/interlink/bin/interlink-sidecar-docker  &> $HOME/.local/interlink/logs/docker-sidecar.log &
        echo $! > $HOME/.local/interlink/sd.pid
        ;;
    htcondor)
        $HOME/.local/interlink/bin/interlink-sidecar-htcondor  &> $HOME/.local/interlink/logs/htcondor-sidecar.log &
        echo $! > $HOME/.local/interlink/sd.pid
        ;;
    esac
}

stop () {
    kill $(cat $HOME/.local/interlink/oauth2-proxy.pid)
    kill $(cat $HOME/.local/interlink/interlink.pid)
    kill $(cat $HOME/.local/interlink/sd.pid)
}

help () {
    echo -e "\n\ninstall:      Downloads InterLink and OAuth binaries, as well as InterLink configuration. Files are stored in $HOME/.local/interlink\n\n"
    echo -e "uninstall:    Delete the $HOME/.local/interlink folder, removing all downloaded files\n\n"
    echo -e "start:        Starts the OAuth proxy, the InterLink API and a Sidecar by the ENV SIDECAR. Actually, valid values for SIDECAR are docker, slurm and htcondor\n\n"
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
    uninstall)
        rm -r $HOME/.local/interlink
        ;;
    help)
        help
        ;;
    *)
        echo -e "You need to specify one of the following commands:"
        help
        ;;
esac
