#!/bin/bash
INTERLINK_INSTALL_PATH="${INTERLINK_INSTALL_PATH:-/etc/interlink}"
INTERLINK_INSTALL_PATH_ESCAPED=${INTERLINK_INSTALL_PATH//\//\\\/}
INTERLINKPORT="${INTERLINKPORT:-30444}"
INTERLINKURL="${INTERLINKURL:-http://0.0.0.0}"

#OAUTH2 related variables. Needed to update oauth2 envs file
# PROXY_CLIENT_ID="${PROXY_CLIENT_ID:-test}"
# PROXY_CLIENT_SECRET="${PROXY_CLIENT_SECRET:-test}"
# OIDC_ISSUER="${OIDC_ISSUER:-https://dodas-iam.cloud.cnaf.infn.it/}"
# AUTHORIZED_GROUPS="${AUTHORIZED_GROUPS:-intw}"
# AUTHORIZED_AUD="${AUTHORIZED_AUD:-intertw-vk}"
# EMAIL_DOMAINS="${EMAIL_DOMAINS:-*}"
# API_HTTP_PORT="${API_HTTP_PORT:-8080}"
# API_HTTPS_PORT="${API_HTTPS_PORT:-30443}"
# HOSTCERT="${HOSTCERT:-/home/ciangottinid/EasyRSA-3.1.5/pki/issued/intertwin.crt}"
# HOSTKEY="${HOSTKEY:-/home/ciangottinid/EasyRSA-3.1.5/pki/private/InterTwin.key}"

#Escaped variables to avoid sed errors
INTERLINKURL_ESCAPED=${INTERLINKURL//\//\\\/}
# OIDC_ISSUER_ESCAPED=${OIDC_ISSUER//\//\\\/}
# EMAIL_DOMAINS_ESCAPED=${EMAIL_DOMAINS//\//\\\/}
# HOSTCERT_ESCAPED=${HOSTCERT//\//\\\/}
# HOSTKEY_ESCAPED=${HOSTKEY//\//\\\/}


OSARCH=$(uname -m)
case "$OSARCH" in
    x86_64)
        OSARCH=amd64
        ;;
esac

install(){
    echo "Copying configs to $INTERLINK_INSTALL_PATH/configs"
    if ! stat $INTERLINK_INSTALL_PATH/configs &> /dev/null; then
        sudo mkdir -p $INTERLINK_INSTALL_PATH/configs | exit 1
    fi

    if stat $INTERLINK_INSTALL_PATH/configs/InterLinkConfig.yaml &> /dev/null; then
        echo "InterLinkConfig.yaml already exists, skipping it's copying!"
    else
        {
            sudo cp "../examples/interlink-slurm/vk/InterLinkConfig.yaml" $INTERLINK_INSTALL_PATH/configs/
        } || {
            echo "Error copying InterLinkConfig.yaml. Exiting..."
            exit 1
        }
    fi


    echo "Building binaries"

    cd ..
    make interlink
    make sidecars

    echo "Copying Binaries to /etc/interlink"
    #copying binaries for interlink and sidecars
    if ! stat $INTERLINK_INSTALL_PATH/bin &> /dev/null; then
        sudo mkdir -p $INTERLINK_INSTALL_PATH/bin | exit 1
    fi
    {
        sudo cp bin/* $INTERLINK_INSTALL_PATH/bin
    } || {
        echo "Error copying binaries to $INTERLINK_INSTALL_PATH/bin. Exiting..."
        exit 1
    }

    sudo rm $INTERLINK_INSTALL_PATH/bin/vk

    echo "Copying Systemd Units to /etc/systemd/system"

    #copying envs for InterLink components
    {
        sudo cp Systemd\ Units/.envs $INTERLINK_INSTALL_PATH 
    } || {
        echo "Error copying .envs to $INTERLINK_INSTALL_PATH. Exiting..."
        exit 1
    }

    #copying envs for Oauth2-proxy
    {
        # sed -i 's/OAUTH2_PROXY_CLIENT_ID=.*/OAUTH2_PROXY_CLIENT_ID='$PROXY_CLIENT_ID'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_CLIENT_SECRET=.*/OAUTH2_PROXY_CLIENT_SECRET='$PROXY_CLIENT_SECRET'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_HTTP_ADDRESS=.*/OAUTH2_PROXY_HTTP_ADDRESS=0.0.0.0:'$API_HTTP_PORT'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_OIDC_ISSUER_URL=.*/OAUTH2_PROXY_OIDC_ISSUER_URL='$OIDC_ISSUER_ESCAPED'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_UPSTREAM=.*/OAUTH2_PROXY_UPSTREAM='$INTERLINKURL_ESCAPED':'$INTERLINKPORT'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_ALLOWED_GROUP=.*/OAUTH2_PROXY_ALLOWED_GROUP='$AUTHORIZED_GROUPS'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_VALIDATE_URL=.*/OAUTH2_PROXY_VALIDATE_URL='$OIDC_ISSUER_ESCAPED'token/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_EMAIL_DOMAINS=.*/OAUTH2_PROXY_EMAIL_DOMAINS='$EMAIL_DOMAINS_ESCAPED'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_HTTPS_ADDRESS=.*/OAUTH2_PROXY_HTTPS_ADDRESS=0.0.0.0:'$API_HTTPS_PORT'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_TLS_CERT_FILE=.*/OAUTH2_PROXY_TLS_CERT_FILE='$HOSTCERT_ESCAPED'/g' "Systemd Units/.envs_oauth" && \
        # sed -i 's/OAUTH2_PROXY_TLS_KEY_FILE=.*/OAUTH2_PROXY_TLS_KEY_FILE='$HOSTKEY_ESCAPED'/g' "Systemd Units/.envs_oauth" && \

        sed -i 's/WorkingDirectory=.*/WorkingDirectory='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin/g' "Systemd Units/.envs_oauth" && \
        sudo cp Systemd\ Units/.envs_oauth $INTERLINK_INSTALL_PATH 
    } || {
        echo "Error copying .envs-oauth to $INTERLINK_INSTALL_PATH. Exiting..."
        exit 1
    }

    #copying Interlink system unit
    {
        sed -i 's/WorkingDirectory=.*/WorkingDirectory='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin/g' "Systemd Units/interlink.service" && \
        sed -i 's/ExecStart=.*/ExecStart='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin\/interlink/g' "Systemd Units/interlink.service" && \
        sed -i 's/EnvironmentFile=.*/EnvironmentFile='$INTERLINK_INSTALL_PATH_ESCAPED'\/.envs/g' "Systemd Units/interlink.service" && \
        sudo cp Systemd\ Units/interlink.service /etc/systemd/system/ 
    } || {
        echo "Error copying interlink.service to /etc/systemd/system/. Exiting..."
        exit 1
    }

    #copying oauth2 system unit
    {
        sed -i 's/WorkingDirectory=.*/WorkingDirectory='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin/g' "Systemd Units/oauth2-proxy.service" && \
        sed -i 's/ExecStart=.*/ExecStart='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin\/oauth2-proxy/g' "Systemd Units/oauth2-proxy.service" && \
        sudo cp Systemd\ Units/oauth2-proxy.service /etc/systemd/system/ 
    } || {
        echo "Error copying oauth2-proxy.service to /etc/systemd/system/. Exiting..."
        exit 1
    }
    
    #copying appropriate sidecar system unit
    case "$1" in
        docker)
            {
                sed -i 's/WorkingDirectory=.*/WorkingDirectory='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin/g' "Systemd Units/docker-sidecar.service" && \
                sed -i 's/ExecStart=.*/ExecStart='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin\/docker-sd/g' "Systemd Units/docker-sidecar.service" && \
                sed -i 's/EnvironmentFile=.*/EnvironmentFile='$INTERLINK_INSTALL_PATH_ESCAPED'\/.envs/g' "Systemd Units/docker-sidecar.service" && \
                sudo cp "Systemd Units/docker-sidecar.service" /etc/systemd/system/ 
            } || {
                echo "Error copying docker-sidecar.service to /etc/systemd/system/. Exiting..."
                exit 1
            }
            ;;
        slurm) 
            {
                sed -i 's/WorkingDirectory=.*/WorkingDirectory='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin/g' "Systemd Units/slurm-sidecar.service" && \
                sed -i 's/ExecStart=.*/ExecStart='$INTERLINK_INSTALL_PATH_ESCAPED'\/bin\/slurm-sd/g' "Systemd Units/slurm-sidecar.service" && \
                sed -i 's/EnvironmentFile=.*/EnvironmentFile='$INTERLINK_INSTALL_PATH_ESCAPED'\/.envs/g' "Systemd Units/slurm-sidecar.service" && \
                sudo cp "Systemd Units/slurm-sidecar.service" /etc/systemd/system/ 
            } || {
                echo "Error copying docker-sidecar.service to /etc/systemd/system/. Exiting..."
                exit 1
            }
            ;;
        htcondor)
            {
                sudo cp "Systemd Units/docker-sidecar.service" /etc/systemd/system/
            } || {
                echo "Error copying docker-sidecar.service to /etc/systemd/system/. Exiting..."
                exit
            }
            ;;
        help)
            help
            ;;
        *)
            echo -e "You need to specify one of the following commands:"
            help
            ;;
    esac

    echo "Downloading OAuth binaries"
    cd Systemd\ Units
    {
        {
            ## Downloading oauth2 binaries to the local Systemd Units dircectory
            curl --fail -L -o oauth2-proxy-v7.5.1.linux-$OSARCH.tar.gz https://github.com/oauth2-proxy/oauth2-proxy/releases/download/v7.5.1/oauth2-proxy-v7.5.1.linux-$OSARCH.tar.gz
        } || {
            echo "Error downloading OAuth binaries, exiting..."
            exit 1
        }
    } && {
        {
            tar -xzvf oauth2-proxy-v7.5.1.linux-$OSARCH.tar.gz 
        } || {
            echo "Error extracting OAuth binaries, exiting..."
            rm oauth2-proxy-v7.5.1.linux-$OSARCH.tar.gz
            exit 1
        }
        sudo cp oauth2-proxy-v7.5.1.linux-$OSARCH/oauth2-proxy /etc/interlink/bin/
    }
    rm -r oauth2-proxy-v7.5.1.linux-$OSARCH oauth2-proxy-v7.5.1.linux-$OSARCH.tar.gz
    sudo systemctl daemon-reload
}

enable(){
    sudo systemctl enable interlink.service
    sudo systemctl enable oauth2-proxy.service

    case "$1" in
        docker)
            sudo systemctl enable docker-sidecar.service 
            ;;
        slurm) 
            sudo systemctl enable slurm-sidecar.service
            ;;
        htcondor)
            sudo systemctl enable htcondor-sidecar.service
            ;;
        help)
            help
            ;;
        *)
            echo -e "You need to specify one of the following commands:"
            help
            ;;
    esac

}

disable(){
    stop $1
    (sudo systemctl disable interlink.service && echo "Disabling interlink.service") || echo "Error disabling interlink.service: $?"
    (sudo systemctl disable oauth2-proxy.service && echo "Disabling oauth2-proxy.service") || echo "Error disabling oauth2-proxy.service: $?"
    
    case "$1" in
        docker)
            sudo systemctl disable docker-sidecar.service && echo "Disabling docker-sidecar.service" || echo "Error disabling docker-sidecar.service: $?"
            ;;
        slurm) 
            sudo systemctl disable slurm-sidecar.service && echo "Disabling slurm-sidecar.service" || echo "Error disabling slurm-sidecar.service: $?"
            ;;
        htcondor)
            sudo systemctl disable htcondor-sidecar.service && echo "Disabling htcondor-sidecar.service" || echo "Error disabling htcondor-sidecar.service: $?"
            ;;
        help)
            help
            ;;
        *)
            echo -e "You need to specify one of the following commands:"
            help
            ;;
    esac
}

start(){
    (sudo systemctl start interlink.service && echo "Starting interlink.service") || echo "Error Starting interlink.service: $?"
    (sudo systemctl start oauth2-proxy.service && echo "Starting oauth2-proxy.service") || echo "Error Starting oauth2-proxy.service: $?"

    case "$1" in
        docker)
            sudo systemctl start docker-sidecar.service && echo "Starting docker-sidecar.service" || echo "Error Starting docker-sidecar.service: $?"
            ;;
        slurm) 
            sudo systemctl start slurm-sidecar.service && echo "Starting slurm-sidecar.service" || echo "Error Starting slurm-sidecar.service: $?"
            ;;
        htcondor)
           sudo systemctl start htcondor-sidecar.service && echo "Starting htcondor-sidecar.service" || echo "Error Starting htcondor-sidecar.service: $?"
            ;;
        help)
            help
            ;;
        *)
            echo -e "You need to specify one of the following commands:"
            help
            ;;
    esac
}

stop(){
    (sudo systemctl stop interlink.service && echo "Stopping interlink.service") || echo "Error Stopping interlink.service: $?"
    (sudo systemctl stop oauth2-proxy.service && echo "Stopping oauth2-proxy.service") || echo "Error Stopping oauth2-proxy.service: $?"

    case "$1" in
        docker)
            sudo systemctl stop docker-sidecar.service && echo "Stopping docker-sidecar.service" || echo "Error Stopping docker-sidecar.service: $?"
            ;;
        slurm) 
            sudo systemctl stop slurm-sidecar.service && echo "Stopping slurm-sidecar.service" || echo "Error Stopping slurm-sidecar.service: $?"
            ;;
        htcondor)
           sudo systemctl stop htcondor-sidecar.service && echo "Stopping htcondor-sidecar.service" || echo "Error Stopping htcondor-sidecar.service: $?"
            ;;
        help)
            help
            ;;
        *)
            echo -e "You need to specify one of the following commands:"
            help
            ;;
    esac
}

help(){
    echo -e "\nUsage: install-script.sh option sidecar\n"
    echo -e "install:      Builds binaries from scratch, copies configuration and binaries to /etc/interlink and copies System Units to /etc/systemd/system"
    echo -e "uninstall:    Deletes binaries, configs and system units from install directories"
    echo -e "enable:       Enables the InterLink/Sidecars and Oauth2-proxy services to be run at startup"
    echo -e "disable:      Disables the InterLink/Sidecars and Oauth2-proxy services to be run at startup"
    echo -e "start:        Starts the InterLink/Sidecars and Oauth2-proxy services"
    echo -e "stop:         Stops the InterLink/Sidecars and Oauth2-proxy services"
    echo -e "restart:      Restarts the InterLink/Sidecars and Oauth2-proxy services"
    echo -e "help:         Shows this command list\n"
    echo -e "Available Sidecars: docker, slurm, htcondor\n"
    echo -e "Example: install-script.sh install slurm\n"
}

uninstall(){
    disable $1
    sudo rm -rf $INTERLINK_INSTALL_PATH
    sudo rm /etc/systemd/system/interlink.service
    sudo rm /etc/systemd/system/oauth2-proxy.service
    case "$1" in
        docker)
            sudo rm /etc/systemd/system/docker-sidecar.service
            ;;
        slurm) 
            sudo rm /etc/systemd/system/slurm-sidecar.service
            ;;
        htcondor)
           sudo rm /etc/systemd/system/htcondor-sidecar.service
            ;;
        help)
            help
            ;;
        *)
            echo -e "You need to specify one of the following commands:"
            help
            ;;
    esac
    sudo systemctl daemon-reload
}

if sudo -v &> /dev/null; then
    case "$1" in
        install)
            install $2
            ;;
        start) 
            start $2
            ;;
        stop)
            stop $2
            ;;
        enable) 
            enable $2
            ;;
        disable)
            disable $2
            ;;
        restart)
            stop $2
            start $2
            ;;
        uninstall)
            uninstall $2
            ;;
        help)
            help
            ;;
        *)
            echo -e "You need to specify one of the following commands:"
            help
            ;;
    esac
else
    echo "You don't have access. Run with privileges to properly set up the Systemd Units."
    exit
fi