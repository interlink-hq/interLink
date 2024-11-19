package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
)

func runTunnel(local, remote net.Conn) {
	defer local.Close()
	defer remote.Close()
	done := make(chan struct{}, 2)

	go func() {
		_, err := io.Copy(local, remote)
		if err != nil {
			log.Fatal(err)
			return
		}
		done <- struct{}{}
	}()

	go func() {
		_, err := io.Copy(remote, local)
		if err != nil {
			log.Fatal(err)
			return
		}
		done <- struct{}{}
	}()

	<-done
}

// https://stackoverflow.com/questions/44269142/golang-ssh-getting-must-specify-hoskeycallback-error-despite-setting-it-to-n
// create human-readable SSH-key strings
func keyString(k ssh.PublicKey) string {
	return k.Type() + " " + base64.StdEncoding.EncodeToString(k.Marshal()) // e.g. "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY...."
}

func trustedHostKeyCallback(trustedKey ssh.PublicKey) ssh.HostKeyCallback {

	if trustedKey == nil {
		return func(_ string, _ net.Addr, k ssh.PublicKey) error {
			log.Printf("WARNING: SSH-key verification is *NOT* in effect: to fix, add this trustedKey: %q", keyString(k))
			return nil
		}
	}

	return ssh.FixedHostKey(trustedKey)
}

func main() {
	addr := flag.String("addr", "", "ssh server address to dial as <hostname>:<port>")
	username := flag.String("user", "", "username for ssh")
	keyFile := flag.String("keyfile", "", "file with private key for SSH authentication")
	remotePort := flag.String("rport", "", "remote port for tunnel")
	localSocket := flag.String("lsock", "", "local socket for tunnel")
	hostkeyFile := flag.String("hostkeyfile", "", "file with public key for SSH host check")
	flag.Parse()

	var hostKeyCallback ssh.HostKeyCallback

	if *hostkeyFile == "" {
		log.Print("No hostkey passed, proceeding with insecure hostkey callback mode")
		hostKeyCallback = ssh.HostKeyCallback(
			func(_ string, _ net.Addr, _ ssh.PublicKey) error {
				return nil
			})
	} else {
		pubkey, err := os.ReadFile(*hostkeyFile)
		if err != nil {
			log.Fatalf("unable to hostkeyFile: %v", err)
		}
		hostkey, err := ssh.ParsePublicKey(pubkey)
		if err != nil {
			log.Fatalf("unable to parse private key: %v", err)
		}

		hostKeyCallback = trustedHostKeyCallback(hostkey)
	}

	key, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatalf("unable to read private key: %v", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		log.Fatalf("unable to parse private key: %v", err)
	}
	// An SSH client is represented with a ClientConn.
	//
	// To authenticate with the remote server you must pass at least one
	// implementation of AuthMethod via the Auth field in ClientConfig,
	// and provide a HostKeyCallback.
	config := &ssh.ClientConfig{
		User: *username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
	}

	client, err := ssh.Dial("tcp", *addr, config)
	if err != nil {
		log.Panicf("Failed to dial: %v", err)
	}
	defer client.Close()

	listener, err := client.Listen("tcp", "localhost:"+*remotePort)
	if err != nil {
		client.Close()
		log.Panicf("Failed to listen on remote socket %s: %v", *remotePort, err)
	}
	defer listener.Close()

	log.Printf("Listening on remote socket %s", *remotePort)
	for {
		remote, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection on remote socket %s: %v", *remotePort, err)
			continue
		}
		log.Printf("Accepted connection on remote socket %s", *remotePort)
		go func() {
			local, err := net.Dial("unix", *localSocket)
			if err != nil {
				log.Printf("Failed to dial local socket %s: %v", *localSocket, err)
				remote.Close()
				return
			}
			log.Printf("Connected to local socket %s", *localSocket)
			fmt.Println("tunnel established with", local.LocalAddr())
			runTunnel(local, remote)
		}()
	}
}
