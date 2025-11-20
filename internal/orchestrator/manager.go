package orchestrator

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	KernelPath    = "/var/lib/qudata/images/vmlinux"
	InstancesDir  = "/var/lib/qudata/instances"
	BinaryPath    = "qemu-system-x86_64"
	BaseImagePath = "/var/lib/qudata/images/ubuntu.raw"
	StartPort     = 20000
)

type Config struct {
	Image        string
	CPU          int
	Memory       int
	SSHPublicKey string
}

type Instance struct {
	ID         string
	Cmd        *exec.Cmd
	IP         string
	TapDev     string
	HostPort   int
	MacSuffix  int
	LastConfig Config
}

type Manager struct {
	instances map[string]*Instance
	mu        sync.Mutex
	ipCounter int
	publicIP  string
}

func New() (*Manager, error) {
	if err := os.MkdirAll(InstancesDir, 0755); err != nil {
		return nil, err
	}

	pubIP := "YOUR_SERVER_IP"
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		pubIP = string(body)
	}

	exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()

	return &Manager{
		instances: make(map[string]*Instance),
		ipCounter: 2,
		publicIP:  pubIP,
	}, nil
}

func (m *Manager) CreateInstance(cfg Config) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sourceImagePath, err := EnsureImageReady(cfg.Image)
	if err != nil {
		return "", "", fmt.Errorf("image error: %w", err)
	}

	id := uuid.New().String()

	// Вычисляем IP и Порт
	m.ipCounter++
	currentSuffix := m.ipCounter
	vmIP := fmt.Sprintf("172.16.0.%d", currentSuffix)
	hostPort := StartPort + currentSuffix // Например, 20003
	tapName := fmt.Sprintf("tap%s", id[:8])

	instanceDir := fmt.Sprintf("%s/%s", InstancesDir, id)
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		return "", "", err
	}

	// 2. Настройка сети (TAP)
	if err := createTapInterface(tapName, vmIP); err != nil {
		return "", "", fmt.Errorf("network setup failed: %w", err)
	}

	// 3. Настройка NAT (Port Forwarding)
	// Пробрасываем Host:Port -> VM:22
	if err := setupPortForwarding(hostPort, vmIP, 22); err != nil {
		return "", "", fmt.Errorf("iptables failed: %w", err)
	}

	// 4. Копируем диск
	diskPath := fmt.Sprintf("%s/disk.raw", instanceDir)
	if err := copyFile(sourceImagePath, diskPath); err != nil {
		return "", "", fmt.Errorf("disk copy failed: %w", err)
	}

	// 5. Генерируем Cloud-Init ISO
	isoPath, err := GenerateCloudInitISO(instanceDir, cfg.SSHPublicKey, vmIP)
	if err != nil {
		return "", "", fmt.Errorf("cloud-init failed: %w", err)
	}

	// 6. Запуск QEMU
	cmd, err := startQemu(id, instanceDir, diskPath, isoPath, tapName, currentSuffix, cfg)
	if err != nil {
		return "", "", err
	}

	m.instances[id] = &Instance{
		ID:         id,
		Cmd:        cmd,
		IP:         vmIP,
		TapDev:     tapName,
		HostPort:   hostPort,
		MacSuffix:  currentSuffix,
		LastConfig: cfg,
	}

	// Формируем строку подключения для пользователя
	sshConnectionCmd := fmt.Sprintf("ssh -p %d ubuntu@%s", hostPort, m.publicIP)

	return id, sshConnectionCmd, nil
}

func (m *Manager) DeleteInstance(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("instance not found")
	}

	stopProcess(inst.Cmd)

	cleanupPortForwarding(inst.HostPort, inst.IP, 22)

	exec.Command("ip", "link", "del", inst.TapDev).Run()

	os.RemoveAll(fmt.Sprintf("%s/%s", InstancesDir, id))

	delete(m.instances, id)
	return nil
}

func (m *Manager) ManageInstance(id string, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("instance not found")
	}

	switch action {
	case "stop":
		return stopProcess(inst.Cmd)

	case "start":
		if inst.Cmd != nil && inst.Cmd.ProcessState == nil {
			return nil
		}
		instanceDir := fmt.Sprintf("%s/%s", InstancesDir, id)
		diskPath := fmt.Sprintf("%s/disk.raw", instanceDir)
		isoPath := fmt.Sprintf("%s/cloud-init.disk", instanceDir)

		cmd, err := startQemu(id, instanceDir, diskPath, isoPath, inst.TapDev, inst.MacSuffix, inst.LastConfig)
		if err != nil {
			return err
		}
		inst.Cmd = cmd
		return nil

	case "reboot":
		stopProcess(inst.Cmd)
		time.Sleep(1 * time.Second)

		instanceDir := fmt.Sprintf("%s/%s", InstancesDir, id)
		diskPath := fmt.Sprintf("%s/disk.raw", instanceDir)
		isoPath := fmt.Sprintf("%s/cloud-init.disk", instanceDir)

		cmd, err := startQemu(id, instanceDir, diskPath, isoPath, inst.TapDev, inst.MacSuffix, inst.LastConfig)
		if err != nil {
			return err
		}
		inst.Cmd = cmd
		return nil

	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func startQemu(id, dir, disk, iso, tap string, macSuffix int, cfg Config) (*exec.Cmd, error) {
	logPath := fmt.Sprintf("%s/vm.log", dir)

	args := []string{
		"-nographic",
		"-smp", fmt.Sprintf("%d", cfg.CPU),
		"-m", fmt.Sprintf("%d", cfg.Memory),
		"-accel", "kvm:tcg",
		"-cpu", "host",
		"-kernel", KernelPath,
		"-append", "console=ttyS0 root=/dev/vda rw panic=1",
		"-drive", fmt.Sprintf("file=%s,format=raw,if=virtio", disk),
		"-drive", fmt.Sprintf("file=%s,format=raw,if=virtio,readonly=on", iso),
		"-netdev", fmt.Sprintf("tap,id=mynet0,ifname=%s,script=no,downscript=no", tap),
		"-device", fmt.Sprintf("virtio-net-pci,netdev=mynet0,mac=AA:BB:CC:DD:EE:%02x", macSuffix),
	}

	cmd := exec.Command(BinaryPath, args...)

	outfile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		cmd.Stdout = outfile
		cmd.Stderr = outfile
	}

	log.Printf("Starting VM %s on port %d", id, StartPort+macSuffix)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start qemu: %w", err)
	}

	return cmd, nil
}

func setupPortForwarding(hostPort int, vmIP string, vmPort int) error {
	if err := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", "172.16.0.0/24", "-j", "MASQUERADE").Run(); err != nil {
		exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "172.16.0.0/24", "-j", "MASQUERADE").Run()
	}

	cmd := exec.Command("iptables", "-t", "nat", "-A", "PREROUTING",
		"-p", "tcp", "--dport", fmt.Sprintf("%d", hostPort),
		"-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", vmIP, vmPort))
	return cmd.Run()
}

func cleanupPortForwarding(hostPort int, vmIP string, vmPort int) {
	exec.Command("iptables", "-t", "nat", "-D", "PREROUTING",
		"-p", "tcp", "--dport", fmt.Sprintf("%d", hostPort),
		"-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", vmIP, vmPort)).Run()
}

func createTapInterface(tapName string, vmIP string) error {
	if err := exec.Command("ip", "tuntap", "add", "dev", tapName, "mode", "tap").Run(); err != nil {
		return err
	}
	if err := exec.Command("ip", "link", "set", "dev", tapName, "up").Run(); err != nil {
		return err
	}
	exec.Command("ip", "addr", "add", "172.16.0.1/32", "dev", tapName).Run()
	if err := exec.Command("ip", "route", "add", vmIP+"/32", "dev", tapName).Run(); err != nil {
	}
	return nil
}

func stopProcess(cmd *exec.Cmd) error {
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
		case <-done:
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
