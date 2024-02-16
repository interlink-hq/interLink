These are handful Systemd Units needed to automatically start InterLink and Sidecars at system startup

Just edit the .envs and .envs-oauth files with the ENVS you want to use within the InterLink API, your Sidecar and your Oauth2 Proxy; after that ```cd Systemd\ Units``` and 
```bash
./install-script.sh install slurm
```

The install comand will proceed to build binaries from scratch and to move them and configs to /etc/interlink. Also the Systemd Units will be copied to /etc/systemd/system.

After that, you can run both interlink and the sidecar by running

```bash
./install-script.sh start slurm
```

and you will be able to monitor your service directly from systemctl by

```systemctl status interlink.service``` or ```systemctl status slurm-sidecar.service```

If you want to automatically start these service on startup, just use the enable argument: 

```bash
./install-script.sh enable slurm
```

N.B.: you will need for user to have sudo access!