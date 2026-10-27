package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const AgentVersion = "1.0.0"

func main() {
	memPort := flag.Uint("mem-port", 1024, "Vsock port for host memory telemetry & ballooning")
	fsPort := flag.Uint("fs-port", 9922, "Vsock port for binary inotify event bridge")
	execPort := flag.Uint("exec-port", 8022, "Vsock port for direct PTY execution server")
	dockerPort := flag.Uint("docker-port", 2375, "Vsock port for Docker API forwarder")
	dockerSock := flag.String("docker-sock", "/var/run/docker.sock", "Path to guest dockerd Unix socket")
	mountRoot := flag.String("mount-root", "/mnt/mac", "VirtioFS host root mount path in guest")
	hostCID := flag.Uint("host-cid", 2, "Vsock CID for host hypervisor")
	reportInterval := flag.Duration("report-interval", 250*time.Millisecond, "Cadence for memory telemetry reports")
	versionFlag := flag.Bool("version", false, "Print agent version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("orb-agent v%s (Linux ARM64 Static)\n", AgentVersion)
		return
	}

	log.Printf("==================================================================")
	log.Printf(" Starting orb-agent v%s (Linux ARM64 Guest Control Plane)", AgentVersion)
	log.Printf("==================================================================")

	// 1. Initialize Memory Reclaimer
	memCfg := MemoryReclaimerConfig{
		HostCID:        uint32(*hostCID),
		HostPort:       uint32(*memPort),
		ReportInterval: *reportInterval,
	}
	reclaimer := NewMemoryReclaimer(memCfg)
	if err := reclaimer.Start(); err != nil {
		log.Printf("[WARN] Failed to start memory reclaimer: %v", err)
	}

	// 2. Initialize Inotify Synthesizer
	fsCfg := InotifySynthesizerConfig{
		ListenPort: uint32(*fsPort),
		MountRoot:  *mountRoot,
	}
	synthesizer := NewInotifySynthesizer(fsCfg)
	if err := synthesizer.Start(); err != nil {
		log.Printf("[WARN] Failed to start inotify synthesizer: %v", err)
	}

	// 3. Initialize Direct PTY Exec Server
	execCfg := ExecServerConfig{
		ListenPort: uint32(*execPort),
	}
	execServer := NewExecServer(execCfg)
	if err := execServer.Start(); err != nil {
		log.Printf("[WARN] Failed to start exec server: %v", err)
	}

	// 4. Initialize Docker Forwarder
	dockerCfg := DockerForwarderConfig{
		VsockPort:      uint32(*dockerPort),
		DockerSockPath: *dockerSock,
	}
	dockerForwarder := NewDockerForwarder(dockerCfg)
	if err := dockerForwarder.Start(); err != nil {
		log.Printf("[WARN] Failed to start Docker forwarder: %v", err)
	}

	log.Printf("[orb-agent] All guest subsystems successfully initialized.")

	// Listen for shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("[orb-agent] Received signal %v, terminating subsystems...", sig)

	dockerForwarder.Stop()
	execServer.Stop()
	synthesizer.Stop()
	reclaimer.Stop()

	log.Printf("[orb-agent] Graceful shutdown complete.")
}
