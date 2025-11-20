package orchestrator

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	KernelPath   = "/var/lib/qudata/images/vmlinux"
	InstancesDir = "/var/lib/qudata/instances"
	BinaryPath   = "/usr/local/bin/cloud-hypervisor"
)

// Config - параметры запуска
type Config struct {
	Image        string // <-- Новое поле (имя докер образа)
	CPU          int
	Memory       int
	SSHPublicKey string
}

type Instance struct {
	ID         string
	Cmd        *exec.Cmd
	IP         string
	TapDev     string
	MacSuffix  int
	LastConfig Config
}

type Manager struct {
	instances map[string]*Instance
	mu        sync.Mutex
	ipCounter int
}

func New() (*Manager, error) {
	if err := os.MkdirAll(InstancesDir, 0755); err != nil {
		return nil, err
	}
	return &Manager{
		instances: make(map[string]*Instance),
		ipCounter: 2,
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

	m.ipCounter++
	currentSuffix := m.ipCounter
	vmIP := fmt.Sprintf("172.16.0.%d", currentSuffix)
	tapName := fmt.Sprintf("tap%s", id[:8])

	instanceDir := fmt.Sprintf("%s/%s", InstancesDir, id)
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		return "", "", err
	}

	if err := createTapInterface(tapName, "172.16.0.1"); err != nil {
		return "", "", fmt.Errorf("network failed: %w", err)
	}

	diskPath := fmt.Sprintf("%s/disk.raw", instanceDir)
	if err := copyFile(sourceImagePath, diskPath); err != nil {
		return "", "", fmt.Errorf("disk copy failed: %w", err)
	}

	isoPath, err := GenerateCloudInitISO(instanceDir, cfg.SSHPublicKey, vmIP)
	if err != nil {
		return "", "", fmt.Errorf("cloud-init failed: %w", err)
	}

	cmd, err := startCloudHypervisor(id, instanceDir, diskPath, isoPath, tapName, currentSuffix, cfg)
	if err != nil {
		return "", "", err
	}

	m.instances[id] = &Instance{
		ID:         id,
		Cmd:        cmd,
		IP:         vmIP,
		TapDev:     tapName,
		MacSuffix:  currentSuffix,
		LastConfig: cfg,
	}

	return id, vmIP, nil
}

func (m *Manager) DeleteInstance(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("instance not found")
	}

	stopProcess(inst.Cmd)
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

		cmd, err := startCloudHypervisor(id, instanceDir, diskPath, isoPath, inst.TapDev, inst.MacSuffix, inst.LastConfig)
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
		cmd, err := startCloudHypervisor(id, instanceDir, diskPath, isoPath, inst.TapDev, inst.MacSuffix, inst.LastConfig)
		if err != nil {
			return err
		}
		inst.Cmd = cmd
		return nil
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func startCloudHypervisor(id, dir, disk, iso, tap string, macSuffix int, cfg Config) (*exec.Cmd, error) {
	socketPath := fmt.Sprintf("%s/ch.sock", dir)
	logPath := fmt.Sprintf("%s/vm.log", dir)

	args := []string{
		"--api-socket", socketPath,
		"--kernel", KernelPath,
		"--disk", fmt.Sprintf("path=%s", disk), fmt.Sprintf("path=%s,readonly=on", iso),
		"--cpus", fmt.Sprintf("boot=%d", cfg.CPU),
		"--memory", fmt.Sprintf("size=%dM", cfg.Memory),
		"--net", fmt.Sprintf("tap=%s,mac=AA:BB:CC:DD:EE:%02x", tap, macSuffix),
		"--cmdline", "console=ttyS0 root=/dev/vda1 rw",
	}

	cmd := exec.Command(BinaryPath, args...)
	outfile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		cmd.Stdout = outfile
		cmd.Stderr = outfile
	}

	log.Printf("Starting VM %s (Image: %s)", id, cfg.Image)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start binary: %w", err)
	}
	return cmd, nil
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

func createTapInterface(name, gateway string) error {
	if err := exec.Command("ip", "tuntap", "add", "dev", name, "mode", "tap").Run(); err != nil {
		return err
	}
	if err := exec.Command("ip", "link", "set", "dev", name, "up").Run(); err != nil {
		return err
	}
	exec.Command("ip", "addr", "add", gateway+"/32", "dev", name).Run()
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
