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
COPY docker/test/slurm.conf /etc/slurm/slurm.conf
RUN mkdir -p /var/spool/slurmctld \
    && chown slurm:slurm /var/spool/slurmctld

# Startup configuration
COPY docker/test/startup.sh /etc/startup.sh
RUN chmod 555 /etc/startup.sh

WORKDIR /root

RUN apt install -y software-properties-common \
 && add-apt-repository -y ppa:apptainer/ppa \
 && apt install -y apptainer

CMD ["/bin/sh", "-c", "/etc/startup.sh && bash"]
