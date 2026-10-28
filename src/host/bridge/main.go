package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const BridgeVersion = "1.0.0"

func main() {
	homeDir, _ := os.UserHomeDir()
	aeroUserSock := filepath.Join(homeDir, ".aerostack", "run", "docker.sock")
	orbUserSock := filepath.Join(homeDir, ".orbstack", "run", "docker.sock")
	defaultSockets := fmt.Sprintf("%s,%s,/var/run/docker.sock", aeroUserSock, orbUserSock)
	defaultBackendSock := filepath.Join(homeDir, ".aerostack", "run", "docker_backend.sock")

	socketsFlag := flag.String("sockets", defaultSockets, "Comma-separated Unix socket paths to listen on")
	backendMode := flag.String("backend-mode", "unix", "Backend transport: unix, vsock, tcp")
	backendSocket := flag.String("backend-socket", defaultBackendSock, "Backend Unix socket (if unix mode)")
	backendTCP := flag.String("backend-tcp", "127.0.0.1:2375", "Backend TCP address (if tcp mode)")
	vsockCID := flag.Uint("vsock-cid", 3, "Guest VM vsock CID (if vsock mode)")
	vsockPort := flag.Uint("vsock-port", 2375, "Guest dockerd vsock port (if vsock mode)")
	mountPrefix := flag.String("mount-prefix", "/workspace", "VirtioFS guest mount prefix for host filesystem")
	versionFlag := flag.Bool("version", false, "Print bridge version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("orb-docker-bridge v%s (macOS Host Socket Bridge)\n", BridgeVersion)
		return
	}

	sockets := strings.Split(*socketsFlag, ",")
	for i := range sockets {
		sockets[i] = strings.TrimSpace(sockets[i])
	}

	cfg := DockerProxyConfig{
		ListenSocketPaths: sockets,
		GuestMountPrefix:  *mountPrefix,
		BackendTransport: TransportConfig{
			Mode:        *backendMode,
			SocketPath:  *backendSocket,
			TCPAddr:     *backendTCP,
			VsockCID:    uint32(*vsockCID),
			VsockPort:   uint32(*vsockPort),
		},
	}

	log.Printf("==================================================================")
	log.Printf(" Starting orb-docker-bridge v%s", BridgeVersion)
	log.Printf(" Backend: %s (Prefix: %s)", *backendMode, *mountPrefix)
	log.Printf("==================================================================")

	proxy, err := NewDockerProxy(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize Docker proxy: %v", err)
	}

	if err := proxy.Start(); err != nil {
		log.Fatalf("Failed to start Docker proxy: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Printf("[docker-bridge] Shutting down...")
	_ = proxy.Stop()
	log.Printf("[docker-bridge] Clean exit.")
}
