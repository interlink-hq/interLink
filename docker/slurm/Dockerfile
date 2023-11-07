FROM golang:1.21 as build-stage

WORKDIR /app

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod vendor -o bin/slurm-sidecar cmd/sidecars/slurm/main.go


# Deploy the application binary into a lean image
#FROM ubuntu:latest AS build-release-stage
FROM ubuntu:22.04 

# Settings for all images
ENV TIMEZONE=America/New_York

# Run system updates
RUN apt update && apt -y upgrade

# Set time zone
RUN ln -sn /usr/share/zoneinfo/${TIMEZONE} /etc/localtime \
    && echo ${TIMEZONE} > /etc/timezone \
    && apt -y install tzdata

# Set locale
RUN apt -y install locales \
    && locale-gen en_US.UTF-8 \
    && update-locale LANG=en_US.UTF-8 LC_MESSAGES=POSIX

# Install system packages
RUN DEBIAN_FRONTEND=noninteractive \
    apt -y install munge \
                   slurm-wlm

# Slurm configuration
COPY docker/slurm/slurm.conf /etc/slurm/slurm.conf
RUN mkdir -p /var/spool/slurmctld \
    && chown slurm:slurm /var/spool/slurmctld

# Startup configuration
COPY docker/slurm/startup.sh /etc/startup.sh
RUN chmod 555 /etc/startup.sh

WORKDIR /root

COPY --from=build-stage /app/bin/slurm-sidecar /sidecar/slurm-sidecar

ENV INTERLINKCONFIGPATH=/root/InterLinkConfig.yaml

COPY docker/slurm/InterLinkConfig.yaml .

RUN apt install -y software-properties-common \
 && add-apt-repository -y ppa:apptainer/ppa \
 && apt install -y apptainer

RUN mkdir -p /cvmfs/grid.cern.ch/etc/grid-security

CMD ["/bin/sh", "-c", "/etc/startup.sh && /sidecar/slurm-sidecar"]
