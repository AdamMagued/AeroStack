package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	socketPath := flag.String("socket", "/tmp/orb_docker.sock", "Path to host Unix domain socket")
	backendMode := flag.String("backend-mode", "unix", "Backend transport: unix, tcp, vsock")
	backendSocket := flag.String("backend-socket", "/var/run/docker.sock", "Backend Unix socket path (for unix mode)")
	backendTCP := flag.String("backend-tcp", "127.0.0.1:2375", "Backend TCP host:port (for tcp mode)")
	vsockCID := flag.Uint("vsock-cid", 3, "Vsock CID (for vsock mode)")
	vsockPort := flag.Uint("vsock-port", 2375, "Vsock port (for vsock mode)")
	guestMountPrefix := flag.String("mount-prefix", "/mnt/mac", "VirtioFS guest mount prefix (/ for 1:1)")
	flag.Parse()

	cfg := DockerProxyConfig{
		ListenSocketPath: *socketPath,
		GuestMountPrefix: *guestMountPrefix,
		BackendTransport: TransportConfig{
			Mode:        *backendMode,
			SocketPath:  *backendSocket,
			TCPAddr:     *backendTCP,
			VsockCID:    uint32(*vsockCID),
			VsockPort:   uint32(*vsockPort),
		},
	}

	proxy, err := NewDockerProxy(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize Docker proxy: %v", err)
	}

	if err := proxy.Start(); err != nil {
		log.Fatalf("Failed to start Docker proxy: %v", err)
	}

	fmt.Printf("Docker API Bridge active: %s -> %s (Prefix: %s)\n", *socketPath, *backendMode, *guestMountPrefix)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down Docker API Bridge...")
	if err := proxy.Stop(); err != nil {
		log.Fatalf("Error stopping proxy: %v", err)
	}
}
