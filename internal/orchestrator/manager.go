package orchestrator

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
	HostPort   int
	LastConfig Config
}

type Manager struct {
	instances   map[string]*Instance
	mu          sync.Mutex
	portCounter int
	publicIP    string
}

func New() (*Manager, error) {
	if err := os.MkdirAll(InstancesDir, 0755); err != nil {
		return nil, err
	}

	pubIP := "127.0.0.1"
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		pubIP = string(body)
	}

	return &Manager{
		instances:   make(map[string]*Instance),
		portCounter: 0,
		publicIP:    pubIP,
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
	m.portCounter++
	currentPort := StartPort + m.portCounter

	instanceDir := fmt.Sprintf("%s/%s", InstancesDir, id)
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		return "", "", err
	}

	diskPath := fmt.Sprintf("%s/disk.raw", instanceDir)
	if err := copyFile(sourceImagePath, diskPath); err != nil {
		return "", "", fmt.Errorf("disk copy failed: %w", err)
	}

	if err := InjectKeyDirectly(diskPath, cfg.SSHPublicKey); err != nil {
		log.Printf("WARNING: Direct injection failed: %v", err)
	}

	allowPort(currentPort)

	cmd, err := startQemu(id, instanceDir, diskPath, currentPort, cfg)
	if err != nil {
		return "", "", err
	}

	m.instances[id] = &Instance{
		ID:         id,
		Cmd:        cmd,
		HostPort:   currentPort,
		LastConfig: cfg,
	}

	sshCmd := fmt.Sprintf("ssh -p %d root@%s (Pass: 12345)", currentPort, m.publicIP)
	return id, sshCmd, nil
}

func (m *Manager) DeleteInstance(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("instance not found")
	}

	stopProcess(inst.Cmd)

	denyPort(inst.HostPort)

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

	instanceDir := fmt.Sprintf("%s/%s", InstancesDir, id)
	diskPath := fmt.Sprintf("%s/disk.raw", instanceDir)

	switch action {
	case "stop":
		err := stopProcess(inst.Cmd)
		denyPort(inst.HostPort)
		return err

	case "start":
		if inst.Cmd != nil && inst.Cmd.ProcessState == nil {
			return nil
		}
		allowPort(inst.HostPort)

		cmd, err := startQemu(id, instanceDir, diskPath, inst.HostPort, inst.LastConfig)
		if err != nil {
			return err
		}
		inst.Cmd = cmd
		return nil

	case "reboot":
		stopProcess(inst.Cmd)
		time.Sleep(1 * time.Second)
		cmd, err := startQemu(id, instanceDir, diskPath, inst.HostPort, inst.LastConfig)
		if err != nil {
			return err
		}
		inst.Cmd = cmd
		return nil

	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func allowPort(port int) {
	portStr := fmt.Sprintf("%d", port)
	exec.Command("iptables", "-I", "INPUT", "-p", "tcp", "--dport", portStr, "-j", "ACCEPT").Run()
}

func denyPort(port int) {
	portStr := fmt.Sprintf("%d", port)
	exec.Command("iptables", "-D", "INPUT", "-p", "tcp", "--dport", portStr, "-j", "ACCEPT").Run()
}

func InjectKeyDirectly(diskPath, pubKey string) error {
	log.Printf("Injecting credentials into: %s", diskPath)

	out, err := exec.Command("losetup", "-fP", "--show", diskPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("losetup failed: %v", err)
	}
	loopDev := strings.TrimSpace(string(out))
	defer exec.Command("losetup", "-d", loopDev).Run()

	time.Sleep(500 * time.Millisecond)

	mountPoint := fmt.Sprintf("%s_mount", diskPath)
	os.MkdirAll(mountPoint, 0755)
	defer os.RemoveAll(mountPoint)

	mounted := false
	if err := exec.Command("mount", loopDev, mountPoint).Run(); err == nil {
		if _, err := os.Stat(fmt.Sprintf("%s/etc", mountPoint)); err == nil {
			mounted = true
		} else {
			exec.Command("umount", mountPoint).Run()
		}
	}

	if !mounted {
		for i := 1; i <= 5; i++ {
			partDev := fmt.Sprintf("%sp%d", loopDev, i)
			if _, err := os.Stat(partDev); os.IsNotExist(err) {
				continue
			}
			if err := exec.Command("mount", partDev, mountPoint).Run(); err == nil {
				if _, err := os.Stat(fmt.Sprintf("%s/etc", mountPoint)); err == nil {
					mounted = true
					break
				}
				exec.Command("umount", mountPoint).Run()
			}
		}
	}

	if !mounted {
		return fmt.Errorf("failed to mount root fs")
	}
	defer exec.Command("umount", mountPoint).Run()

	sshDir := fmt.Sprintf("%s/root/.ssh", mountPoint)
	os.MkdirAll(sshDir, 0700)
	os.WriteFile(fmt.Sprintf("%s/authorized_keys", sshDir), []byte(pubKey), 0600)
	exec.Command("chown", "-R", "0:0", sshDir).Run()

	sshConfig := fmt.Sprintf("%s/etc/ssh/sshd_config", mountPoint)
	f, err := os.OpenFile(sshConfig, os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		f.WriteString("\nPermitRootLogin yes\nPasswordAuthentication yes\nPubkeyAuthentication yes\n")
		f.Close()
	}

	exec.Command("chroot", mountPoint, "/bin/sh", "-c", "echo 'root:12345' | chpasswd").Run()

	return nil
}

func startQemu(id, dir, disk string, port int, cfg Config) (*exec.Cmd, error) {
	logPath := fmt.Sprintf("%s/vm.log", dir)
	args := []string{
		"-nographic",
		"-smp", fmt.Sprintf("%d", cfg.CPU),
		"-m", fmt.Sprintf("%d", cfg.Memory),
		"-accel", "tcg",
		"-cpu", "max",
		"-kernel", KernelPath,
		"-append", "console=ttyS0 root=/dev/vda rw panic=1", // vda без 1 для raw дисков
		"-drive", fmt.Sprintf("file=%s,format=raw,if=virtio", disk),
		// SLIRP Сеть с пробросом порта
		"-netdev", fmt.Sprintf("user,id=mynet0,hostfwd=tcp::%d-:22", port),
		"-device", "virtio-net-pci,netdev=mynet0",

		// "-device", "vfio-pci,host=00:06.0", // GPU отключена до лучших времен
	}

	cmd := exec.Command(BinaryPath, args...)
	outfile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		cmd.Stdout = outfile
		cmd.Stderr = outfile
	}

	log.Printf("Starting VM %s on port %d", id, port)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start qemu: %w", err)
	}
	return cmd, nil
}

func stopProcess(cmd *exec.Cmd) error {
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGTERM)
		go func() {
			time.Sleep(3 * time.Second)
			cmd.Process.Kill()
			cmd.Wait()
		}()
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
