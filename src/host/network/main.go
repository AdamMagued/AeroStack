package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const NetdVersion = "1.0.0"

func main() {
	homeDir, _ := os.UserHomeDir()
	defaultDockerSock := filepath.Join(homeDir, ".aerostack", "run", "docker.sock")
	dockerSock := flag.String("docker-sock", defaultDockerSock, "Path to Docker unix domain socket to watch for container published ports")
	dnsBind := flag.String("dns-bind", "127.0.0.1:5354", "Address and port for embedded RFC 1035 DNS server")
	upstreamDNS := flag.String("upstream-dns", "1.1.1.1:53", "Upstream DNS resolver for non-.orb.local queries")
	routerIP := flag.String("router-ip", "192.168.128.1", "IPv4 address for userspace router gateway")
	enableMDNS := flag.Bool("enable-mdns", true, "Enable Bonjour / mDNS local advertiser")
	versionFlag := flag.Bool("version", false, "Print network daemon version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("orb-netd v%s (Userspace Network & DNS Engine)\n", NetdVersion)
		return
	}

	log.Printf("==================================================================")
	log.Printf(" Starting orb-netd v%s (Userspace Network & DNS Engine)", NetdVersion)
	log.Printf(" Router IP: %s | DNS Bind: %s | Upstream: %s", *routerIP, *dnsBind, *upstreamDNS)
	log.Printf("==================================================================")

	// 1. Initialize Userspace Router
	parsedRouterIP := net.ParseIP(*routerIP)
	if parsedRouterIP == nil {
		parsedRouterIP = net.ParseIP("192.168.128.1")
	}

	routerCfg := RouterConfig{
		RouterMAC:  [6]byte{0x52, 0x54, 0x00, 0x12, 0x34, 0x56},
		RouterIPv4: IPTo4(parsedRouterIP),
		RouterIPv6: IPTo16(net.ParseIP("fd00:1234:5678::1")),
	}

	router := NewUserspaceRouter(routerCfg, func(frame []byte) error {
		return nil
	})
	log.Printf("[orb-netd:router] Userspace router initialized (MAC: 52:54:00:12:34:56)")

	// 2. Initialize Embedded DNS Server
	dnsServer := NewEmbeddedDNSServer(*upstreamDNS)
	// Register default gateway and host domains (.orb.local and .aerostack.local)
	dnsServer.RegisterRecord("host.orb.local", parsedRouterIP, net.ParseIP("fd00:1234:5678::1"), 60)
	dnsServer.RegisterRecord("host.aerostack.local", parsedRouterIP, net.ParseIP("fd00:1234:5678::1"), 60)
	dnsServer.RegisterRecord("orb.local", parsedRouterIP, net.ParseIP("fd00:1234:5678::1"), 60)
	dnsServer.RegisterRecord("aerostack.local", parsedRouterIP, net.ParseIP("fd00:1234:5678::1"), 60)

	// Discover local machines and register their DNS records
	home, _ := os.UserHomeDir()
	machinesDir := filepath.Join(home, ".aerostack", "data", "machines")
	if entries, err := os.ReadDir(machinesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				mName := strings.ToLower(entry.Name())
				machIP := net.ParseIP("192.168.128.2")
				dnsServer.RegisterRecord(mName+".orb.local", machIP, nil, 60)
				dnsServer.RegisterRecord(mName+".aerostack.local", machIP, nil, 60)
				log.Printf("[orb-netd:dns] Registered machine DNS: %s.orb.local / %s.aerostack.local -> %s", mName, mName, machIP)
			}
		}
	}

	if err := dnsServer.Start(*dnsBind); err != nil {
		log.Printf("[orb-netd:warn] Failed to bind DNS to %s: %v (falling back to unprivileged port)", *dnsBind, err)
		_ = dnsServer.Start("127.0.0.1:0")
	} else {
		log.Printf("[orb-netd:dns] RFC 1035 DNS server active on %s (handling *.orb.local and *.aerostack.local)", *dnsBind)
	}

	// 3. Initialize Userspace Port Forwarder and Docker Event Watcher
	portForwarder := NewPortForwarder()
	portForwarder.Start()
	log.Printf("[orb-netd:forward] Userspace dynamic port forwarding active")

	dockerWatcher := NewDockerPortWatcher(*dockerSock, portForwarder, *routerIP)
	dockerWatcher.Start()

	// 4. Initialize Bonjour mDNS Advertiser
	var mdns *MDNSAdvertiser
	if *enableMDNS {
		var err error
		mdns, err = NewMDNSAdvertiser()
		if err != nil {
			log.Printf("[orb-netd:warn] Failed to connect to Bonjour mDNS: %v", err)
		} else {
			_ = mdns.RegisterHostname("host.local", parsedRouterIP, nil)
			log.Printf("[orb-netd:mdns] Native Bonjour advertiser active")
		}
	}

	log.Printf("[orb-netd] Engine ready and running.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("[orb-netd] Received signal %v, terminating engine...", sig)

	dockerWatcher.Stop()
	portForwarder.Stop()

	if mdns != nil {
		mdns.Close()
	}
	dnsServer.Stop()
	stats := router.Stats()
	log.Printf("[orb-netd] Router stats: Rx: %d pkts, Tx: %d pkts, ARP: %d, ICMP: %d",
		stats.RxPackets, stats.TxPackets, stats.ArpHandled, stats.IcmpHandled)
	log.Printf("[orb-netd] Graceful shutdown complete.")
}
