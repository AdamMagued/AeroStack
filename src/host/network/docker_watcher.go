package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DockerEvent represents a stream event from /events
type DockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// ContainerInspectResult captures network port mappings from /containers/{id}/json
type ContainerInspectResult struct {
	ID              string `json:"Id"`
	NetworkSettings struct {
		IPAddress string `json:"IPAddress"`
		Ports     map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
	HostConfig struct {
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
	} `json:"HostConfig"`
}

// DockerPortWatcher subscribes to Docker events and dynamically registers published ports
type DockerPortWatcher struct {
	dockerSockPath string
	forwarder      *PortForwarder
	gatewayIP      string
	mu             sync.Mutex
	containerRules map[string][]ForwardRule
	stopCh         chan struct{}
	running        bool
	conn           net.Conn
	wg             sync.WaitGroup
}

// NewDockerPortWatcher initializes a watcher instance
func NewDockerPortWatcher(dockerSockPath string, forwarder *PortForwarder, gatewayIP string) *DockerPortWatcher {
	if dockerSockPath == "" {
		home, _ := os.UserHomeDir()
		dockerSockPath = filepath.Join(home, ".aerostack", "run", "docker.sock")
	}
	if gatewayIP == "" || gatewayIP == "192.168.128.1" {
		if envIP := os.Getenv("AEROSTACK_GUEST_IP"); envIP != "" {
			gatewayIP = envIP
		} else {
			gatewayIP = "192.168.64.2"
		}
	}

	return &DockerPortWatcher{
		dockerSockPath: dockerSockPath,
		forwarder:      forwarder,
		gatewayIP:      gatewayIP,
		containerRules: make(map[string][]ForwardRule),
		stopCh:         make(chan struct{}),
	}
}

// Start begins the event subscriber loop in the background
func (w *DockerPortWatcher) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.mu.Unlock()

	w.wg.Add(1)
	go w.eventLoop()
	log.Printf("[orb-netd:docker] Docker event port forwarder started watching: %s", w.dockerSockPath)
}

// flushRules removes all active container forwarding rules and clears tracking
func (w *DockerPortWatcher) flushRules() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, rules := range w.containerRules {
		for _, r := range rules {
			_ = w.forwarder.RemoveForward(r.Protocol, r.HostIP, r.HostPort)
		}
	}
	w.containerRules = make(map[string][]ForwardRule)
}

// Stop cleanly unregisters all container forwards and terminates subscriber
func (w *DockerPortWatcher) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	close(w.stopCh)
	if w.conn != nil {
		_ = w.conn.SetReadDeadline(time.Now())
		_ = w.conn.Close()
	}
	w.mu.Unlock()

	w.wg.Wait()
	w.flushRules()
	log.Printf("[orb-netd:docker] Docker event port forwarder stopped cleanly.")
}

func (w *DockerPortWatcher) eventLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		if _, err := os.Stat(w.dockerSockPath); err != nil {
			select {
			case <-w.stopCh:
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		conn, err := net.DialTimeout("unix", w.dockerSockPath, 2*time.Second)
		if err != nil {
			select {
			case <-w.stopCh:
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		w.mu.Lock()
		w.conn = conn
		w.mu.Unlock()

		// Initial synchronization of currently active containers
		w.syncExistingContainers()

		// Stream Docker container events
		w.streamEvents(conn)
		_ = conn.Close()

		w.mu.Lock()
		w.conn = nil
		w.mu.Unlock()

		// Flush zombie rules on disconnect
		w.flushRules()

		select {
		case <-w.stopCh:
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func (w *DockerPortWatcher) syncExistingContainers() {
	conn, err := net.DialTimeout("unix", w.dockerSockPath, 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	req, err := http.NewRequest("GET", "http://localhost/containers/json", nil)
	if err != nil {
		return
	}
	if err := req.Write(conn); err != nil {
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	defer resp.Body.Close()

	var containers []struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err == nil {
		for _, c := range containers {
			w.handleContainerStart(c.ID)
		}
	}
}

func (w *DockerPortWatcher) streamEvents(conn net.Conn) {
	req, err := http.NewRequest("GET", "http://localhost/events?filters=%7B%22type%22%3A%5B%22container%22%5D%7D", nil)
	if err != nil {
		return
	}
	if err := req.Write(conn); err != nil {
		return
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		var ev DockerEvent
		if err := dec.Decode(&ev); err != nil {
			return
		}

		if ev.Type == "container" || ev.Type == "" {
			switch ev.Action {
			case "start":
				w.handleContainerStart(ev.Actor.ID)
			case "stop", "die", "kill", "destroy":
				w.handleContainerStop(ev.Actor.ID)
			}
		}
	}
}

func (w *DockerPortWatcher) handleContainerStart(containerID string) {
	if containerID == "" {
		return
	}

	conn, err := net.DialTimeout("unix", w.dockerSockPath, 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	inspectURL := fmt.Sprintf("http://localhost/containers/%s/json", containerID)
	req, err := http.NewRequest("GET", inspectURL, nil)
	if err != nil {
		return
	}
	if err := req.Write(conn); err != nil {
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	defer resp.Body.Close()

	var inspect ContainerInspectResult
	if err := json.NewDecoder(resp.Body).Decode(&inspect); err != nil {
		return
	}

	// Port mappings
	ports := inspect.NetworkSettings.Ports
	if len(ports) == 0 {
		ports = inspect.HostConfig.PortBindings
	}

	var registered []ForwardRule
	for portKey, bindings := range ports {
		parts := strings.Split(portKey, "/")
		if len(parts) == 0 {
			continue
		}
		proto := "tcp"
		if len(parts) > 1 {
			proto = strings.ToLower(parts[1])
		}
		if proto != "tcp" {
			continue
		}

		for _, b := range bindings {
			hostPort, err := strconv.Atoi(b.HostPort)
			if err != nil || hostPort <= 0 {
				continue
			}
			hostIP := b.HostIP
			if hostIP == "" || hostIP == "0.0.0.0" {
				hostIP = "127.0.0.1"
			}

			// Destination must be the Linux guest VM IP at binding.HostPort
			targetIP := w.gatewayIP
			targetPort := hostPort

			if err := w.forwarder.ForwardTCP(hostIP, hostPort, targetIP, targetPort); err == nil {
				rule := ForwardRule{
					Protocol:   proto,
					HostIP:     hostIP,
					HostPort:   hostPort,
					TargetIP:   targetIP,
					TargetPort: targetPort,
				}
				registered = append(registered, rule)
			}
		}
	}

	if len(registered) > 0 {
		w.mu.Lock()
		w.containerRules[containerID] = append(w.containerRules[containerID], registered...)
		w.mu.Unlock()
		log.Printf("[orb-netd:docker] Forwarded %d ports for container %s (target: %s)",
			len(registered), containerID[:min(12, len(containerID))], w.gatewayIP)
	}
}

func (w *DockerPortWatcher) handleContainerStop(containerID string) {
	w.mu.Lock()
	rules, exists := w.containerRules[containerID]
	delete(w.containerRules, containerID)
	w.mu.Unlock()

	if !exists {
		return
	}

	for _, r := range rules {
		_ = w.forwarder.RemoveForward(r.Protocol, r.HostIP, r.HostPort)
	}
	log.Printf("[orb-netd:docker] Unforwarded %d ports for stopped container %s",
		len(rules), containerID[:min(12, len(containerID))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
