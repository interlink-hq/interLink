#!/bin/bash
VK_VERSION="${VERSION:-v0.0.8}"
DOCKER_USER="${DOCKER_USER:-surax98}"
SIDECAR="${SIDECAR:-slurm}"
KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
ROOTDIR=$PWD
INTERLINK_IP_ADDRESS="${INTERLINK_IP_ADDRESS:-192.168.122.121}"

ROOTDIR_ESCAPED=${ROOTDIR//\//\\\/}

exit_script(){
    cd "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test
    docker compose down
    rm -r "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test 2&>/dev/null
    kubectl delete pod test-pod-cowsay -n vk --force 2&>/dev/null
    kubectl delete deployment test-vk test-deployment test-deployment-cfgmap test-deployment-secret test-deployment-cfgmap-secret -n vk --force 2&>/dev/null
    kubectl delete configmap my-configmap my-configmap2 -n vk 2&>/dev/null
    kubectl delete secret my-secret my-secret2 -n vk 2&>/dev/null
    exit "$1"
}

build_binaries(){
    {
        echo -e "Building binaries...\c"
        make all > /dev/null && \
        echo -e "\r\033[32mSuccessfully built binaries \u2714\033[0m" 
    } || {
        echo -e "\r\033[31mFailed to build binaries from sources. Check make logs \u274c\033[0m"
        exit_script 1
    }
}

build_vk_image(){
    # VK image using docker build
    {
        echo -e "Building Docker image for VK...\c"
        docker buildx build --progress=quiet --tag "$DOCKER_USER"/test-vk:"$VK_VERSION" -f "$ROOTDIR"/docker/Dockerfile.vk "$ROOTDIR" > /dev/null && \
        docker push "$DOCKER_USER"/test-vk:"$VK_VERSION" > /dev/null && \
        echo -e "\r\033[32mSuccesfully built a Docker image for VK \u2714\033[0m"
    } || {
        echo -e "\r\033[31mFailed to build a Docker Image for VK \u274c\033[0m"
        exit_script 1
    }
}

build_IL_sidecar_image(){
    # InterLink + Sidecar images using docker compose build
    {
        cd "$ROOTDIR"/examples/interlink-slurm/vk-test        
        echo -e "Building Docker images for InterLink and $SIDECAR sidecar...\c"
        docker compose build --quiet > /dev/null && \
        echo -e "\r\033[32mSuccesfully built Docker images for InterLink and $SIDECAR sidecar \u2714\033[0m"
        cd "$ROOTDIR"
    } || {
        echo -e "\r\033[31mFailed to build Docker Images for InterLink and $SIDECAR sidecar \u274c\033[0m"
        exit_script 1
    }
}

prepare_files(){
    rm -r "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test
    mkdir "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test
    cp "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk/* "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test/
    cp "$ROOTDIR"/examples/interlink-slurm/interlink/docker-compose.yaml "$ROOTDIR"/examples/interlink-slurm/vk-test
    sed -i 's/InterlinkURL:.*/InterlinkURL: "http:\/\/'"$INTERLINK_IP_ADDRESS"'"/g'  "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test/InterLinkConfig.yaml 
    sed -i 's/SidecarURL:.*/SidecarURL: "http:\/\/'"$INTERLINK_IP_ADDRESS"'"/g' "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test/InterLinkConfig.yaml
    sed -i 's/source:.*/source: ..\/vk-test/g' "$ROOTDIR"/examples/interlink-slurm/vk-test/docker-compose.yaml
    sed -i 's/ghcr.io\/intertwin-eu\/virtual-kubelet-inttw:latest/docker.io\/'"$DOCKER_USER"'\/test-vk:'"$VK_VERSION"'/g' "$ROOTDIR/examples/interlink-$SIDECAR/vk-test/deployment.yaml"
}

run_IL_Sidecar(){
    COUNTER=0
    cd "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test
    docker compose up --force-recreate --quiet-pull -d

    # Waiting for InterLink container to properly start
    while true; do
        echo -e "\rWaiting for InterLink initialization... $COUNTER\c"
        OUTPUT=$(curl -s "$INTERLINK_IP_ADDRESS":3000)
        if [[ "$OUTPUT" == "404 page not found" ]]; then
            echo -e "\r                                            \c"
            echo -e "\r\033[32mInterLink Up and Running \u2714\033[0m"
            break
        fi

        if [ $COUNTER -ge 300 ]; then
            echo -e "\r                                             \c"
            echo -e "\r\033[31mFailed to start InterLink \u274c\033[0m"
            exit_script 1
        fi
        ((COUNTER++))
        sleep 1
    done

    # Waiting for Sidecar container to properly start
    while true; do
        echo -e "\rWaiting for $SIDECAR Sidecar initialization... $COUNTER\c"
        OUTPUT=$(curl -s "$INTERLINK_IP_ADDRESS":4000)
        if [[ "$OUTPUT" == "404 page not found" ]]; then
            echo -e "\r                                                   \c"
            echo -e "\r\033[32m$SIDECAR Sidecar Up and Running \u2714\033[0m"
            break
        fi

        if [ $COUNTER -ge 300 ]; then
            echo -e "\r                                        \c"
            echo -e "\r\033[31mFailed to start $SIDECAR Sidecar \u274c\033[0m"
            exit_script 1
        fi
        ((COUNTER++))
        sleep 1
    done
}

run_VK(){
    COUNTER=0
    EXISTING_NS=$(kubectl get namespace 2>/dev/null | grep vk)
    if [[ "$EXISTING_NS" == "" ]]; then
        kubectl create namespace vk 
    fi

    #checking if already existing test vk are running
    ALREADYRUNNING=$(kubectl get pods -n vk 2>/dev/null | grep test-vk | grep Running)
    if [[ "$ALREADYRUNNING" != "" ]]; then
        kubectl delete deployment test-vk -n vk &> /dev/null
        sleep 1
    fi

    # Waiting for previous VK to terminate
    while true; do
        echo -e "\rWaiting for previous VK to terminate... $COUNTER\c"
        TERMINATING=$(kubectl get pods -n vk 2> /dev/null | grep test-vk | grep Terminating)
        if [[ "$TERMINATING" == "" ]]; then
            kubectl apply -n vk -k "$ROOTDIR"/examples/interlink-"$SIDECAR"/vk-test > /dev/null

            # Waiting for VK to properly start
            while true; do
                echo -e "\r                                                  \c"
                echo -e "\rWaiting for VK to be ready... $COUNTER\c"
                OUTPUT=$(kubectl get pods -n vk 2> /dev/null | grep test-vk | grep Running | grep "3/3")
                if [[ "$OUTPUT" != "" ]]; then
                    echo -e "\r                                                 \c"
                    echo -e "\r\033[32mVK Up and Running \u2714\033[0m"
                    break
                fi
                if [ $COUNTER -ge 300 ]; then
                    echo -e "\r                                                 \c"
                    echo -e "\r\033[31mFailed to start VK \u274c\033[0m"
                    exit_script 1
                fi
                ((COUNTER++))
                sleep 1
            done

            break
        fi

        if [ $COUNTER -ge 300 ]; then
            echo -e "\r                                             \c"
            echo -e "\r\033[31mFailed to start VK \u274c\033[0m"
            exit_script 1
        fi
        ((COUNTER++))
        sleep 1
    done
}

check_ping(){
    echo -e "\rChecking for Ping...\c"
    sleep 1 #waiting to be sure the VK had enough time to log the ping request
    POD_NAME=$(kubectl get pods -n vk -o go-template='{{range .items}}{{.metadata.name}}{{"\n"}}{{end}}' | grep test-vk | awk '{print}')
    ERR=$(kubectl logs $POD_NAME -n vk -c inttw-vk | grep error)
    if [[ $ERR != "" ]]; then
        echo -e "\r                                        \c"
        echo -e "\r\033[31mVK Failed to ping InterLink \u274c\033[0m"
        exit_script 1
    else 
        echo -e "\r                                        \c"
        echo -e "\r\033[32mVK successfully pinged InterLink API \u2714\033[0m"
    fi

    PING=$(docker logs vk-test-interlink-1 2>&1 | grep "received Ping call")
    if [[ $PING == "" ]]; then
        echo -e "\r                                        \c"
        echo -e "\r\033[31mNo Ping in InterLink logs \u274c\033[0m"
        exit_script 1
    else 
        echo -e "\r                                        \c"
        echo -e "\r\033[32mPing request received by InterLink \u2714\033[0m"
    fi    
}

apply_test_pod(){
    COUNTER=0

    ALREADYRUNNING=$(kubectl get pods -n vk | grep test-pod-cowsay)
    if [[ "$ALREADYRUNNING" != "" ]]; then
        kubectl delete pod test-pod-cowsay -n vk &> /dev/null
        sleep 1
    fi

    while true; do
        echo -e "\rWaiting for previous Pod to terminate... $COUNTER\c"
        TERMINATING=$(kubectl get pods -n vk 2> /dev/null | grep test-pod | grep Terminating)
        if [[ "$TERMINATING" == "" ]]; then
            OUTPUT=$(kubectl apply -f "$ROOTDIR"/examples/interlink-"$SIDECAR"/test-pod.yaml)
            while true; do
                echo -e "\r                                                  \c"
                echo -e "\rWaiting for Pod initialization... $COUNTER\c"
                OUTPUT=$(kubectl get pods -n vk | grep test-pod | grep Running)
                if [[ "$OUTPUT" != "" ]]; then
                    echo -e "\r                                             \c"
                    echo -e "\r\033[32mPod test-pod-cowsay is running \u2714\033[0m"
                    break
                fi

                if [ $COUNTER -ge 300 ]; then
                    echo -e "\r                                        \c"
                    echo -e "\r\033[31mPod test-pod-cowsay failed to run \u274c\033[0m"
                    exit_script 1
                fi
                ((COUNTER++))
                sleep 1
            done
            break
        fi

        if [ $COUNTER -ge 300 ]; then
            echo -e "\r                                             \c"
            echo -e "\r\033[31mFailed to start Pod \u274c\033[0m"
            exit_script 1
        fi
        ((COUNTER++))
        sleep 1
    done
}

check_pod_logs(){
    COUNTER=0
    while true; do
        echo -e "\rWaiting for Pod $1 to complete... $COUNTER\c"
        OUTPUT=$(kubectl get pods -n vk | grep $1 | grep Completed)
        if [[ $OUTPUT != "" ]]; then
            echo -e "\rRetrieving Pod's logs...\c"
            LOGS=$(kubectl logs $1 -n vk 2> /dev/null| grep "hello muu")
            if [[ $LOGS != "" ]]; then
                echo -e "\r\033[32mSuccessfully retrieved logs from $1 \u2714\033[0m"
            else
                echo -e "\r\033[31mFailed to retrieve logs from $1 \u274c\033[0m"
                exit_script 1
            fi
            break
        fi

        if [ $COUNTER -ge 300 ]; then
            echo -e "\r\033[31mFailed to retrieve logs from test-pod-cowsay \u274c\033[0m"
            exit_script 1
        fi

        ((COUNTER++))
        sleep 1
    done
    kubectl delete pod $1 -n vk 2&>/dev/null
}

apply_test_deployment(){
    COUNTER=0

    ALREADYRUNNING=$(kubectl get deployment -n vk | grep $1)
    if [[ "$ALREADYRUNNING" != "" ]]; then
        kubectl delete deployment $1 -n vk &> /dev/null
        sleep 1
    fi

    while true; do
        echo -e "\rWaiting for previous Deployment to terminate... $COUNTER\c"
        TERMINATING=$(kubectl get pods -n vk 2> /dev/null | grep $1)
        if [[ "$TERMINATING" == "" ]]; then
            OUTPUT=$(kubectl apply -f "$ROOTDIR"/examples/interlink-"$SIDECAR"/"$1".yaml)
            while true; do
                echo -e "\r                                                  \c"
                echo -e "\rWaiting for Pods initialization... $COUNTER\c"
                OUTPUT=$(kubectl get deployments -n vk | grep $1 | grep "5/5")
                if [[ "$OUTPUT" != "" ]]; then
                    echo -e "\r                                             \c"
                    echo -e "\r\033[32mDeployment $1 is set-up and Pods are running \u2714\033[0m"
                    break
                fi

                if [ $COUNTER -ge 300 ]; then
                    echo -e "\r                                        \c"
                    echo -e "\r\033[31mFailed to set-up Deployment $1 \u274c\033[0m"
                    exit_script 1
                fi
                ((COUNTER++))
                sleep 1
            done
            break
        fi

        if [ $COUNTER -ge 300 ]; then
            echo -e "\r                                             \c"
            echo -e "\r\033[31mFailed to set-up Deployment $1 \u274c\033[0m"
            exit_script 1
        fi
        ((COUNTER++))
        sleep 1
    done
}

check_deployment_logs(){
    POD_NUMBER=0
    POD_NAMES=( $(kubectl get pods -n vk | grep $1 | awk '{print $1}') )

    for pod_name in "${POD_NAMES[@]}"; do
        check_pod_logs $pod_name
        ((POD_NUMBER++))
    done

    if [[ $POD_NUMBER == 5 ]]; then
        echo -e "\r\033[32mSuccessfully retrieved logs from Deployment $1 \u2714\033[0m"
    else
        echo -e "\r\033[31mFailed to retrieve logs from Deployment $1 \u274c\033[0m"
        exit_script 1
    fi
    kubectl delete deployment $1 -n vk 2&>/dev/null
}

{   
    kubectl delete deployment test-deployment test-deployment-cfgmap test-deployment-secret test-deployment-cfgmap-secret test-vk -n vk 2&>/dev/null
    kubectl delete pod test-pod -n vk 2&>/dev/null
    kubectl delete configmap my-configmap my-configmap2 -n vk 2&>/dev/null
    kubectl delete secret my-secret my-secret2 -n vk 2&>/dev/null
    prepare_files

    case $1 in 
        test)
            build_binaries

            build_vk_image

            build_IL_sidecar_image

            run_IL_Sidecar

            run_VK

            check_ping

            apply_test_pod

            check_pod_logs "test-pod"

            apply_test_deployment "test-deployment"

            check_deployment_logs "test-deployment"

            apply_test_deployment "test-deployment-cfgmap"

            check_deployment_logs "test-deployment-cfgmap"

            apply_test_deployment "test-deployment-secret"

            check_deployment_logs "test-deployment-secret"

            apply_test_deployment "test-deployment-cfgmap-secret"

            check_deployment_logs "test-deployment-cfgmap-secret"

            exit_script 0
        ;;

        build)
            build_binaries

            build_vk_image

            build_IL_sidecar_image

            exit_script 0
        ;;

        build_run)
            build_binaries

            build_vk_image

            build_IL_sidecar_image

            run_IL_Sidecar

            run_VK
        ;;

        build_run_vk)
            build_vk_image

            run_VK
        ;;

        run)
            run_IL_Sidecar

            run_VK
        ;;

        *)
            echo -e "Specify one of the following arguments: test, build, build_run, build_run_vk, run\n"
            exit_script 0
        ;;

    esac
}