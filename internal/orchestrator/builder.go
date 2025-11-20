package orchestrator

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

const ImageCacheDir = "/var/lib/qudata/images"

const VmDockerTemplate = `
ARG BASE_IMAGE
FROM ${BASE_IMAGE}

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y \
    systemd \
    cloud-init \
    openssh-server \
    sudo \
    iproute2 \
    net-tools \
    udev \
    && apt-get clean

RUN echo 'datasource_list: [ NoCloud, None ]' > /etc/cloud/cloud.cfg.d/90_dpkg.cfg

RUN mkdir -p /var/run/sshd

CMD ["/lib/systemd/systemd"]
`

func EnsureImageReady(userImage string) (string, error) {
	safeName := strings.ReplaceAll(userImage, "/", "_")
	safeName = strings.ReplaceAll(safeName, ":", "_")
	rawPath := fmt.Sprintf("%s/%s.raw", ImageCacheDir, safeName)

	if _, err := os.Stat(rawPath); err == nil {
		log.Printf("[Builder] Image found in cache: %s", rawPath)
		return rawPath, nil
	}

	log.Printf("[Builder] Converting '%s' to bootable MicroVM disk...", userImage)

	buildDir := fmt.Sprintf("/tmp/build_%s", safeName)
	os.MkdirAll(buildDir, 0755)
	defer os.RemoveAll(buildDir)

	dockerfilePath := fmt.Sprintf("%s/Dockerfile", buildDir)
	if err := os.WriteFile(dockerfilePath, []byte(VmDockerTemplate), 0644); err != nil {
		return "", err
	}

	vmImageTag := fmt.Sprintf("qudata-vm:%s", safeName)
	log.Println(">>> Step 1/5: Injecting VM tools (SSH/Systemd)...")

	buildCmd := exec.Command("docker", "build",
		"--build-arg", fmt.Sprintf("BASE_IMAGE=%s", userImage),
		"-t", vmImageTag,
		buildDir)

	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr

	if err := buildCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build bootable image: %w. (Make sure base image is Ubuntu/Debian)", err)
	}

	containerName := fmt.Sprintf("export_%s", safeName)
	exec.Command("docker", "rm", "-f", containerName).Run()

	if err := exec.Command("docker", "create", "--name", containerName, vmImageTag).Run(); err != nil {
		return "", fmt.Errorf("docker create failed: %w", err)
	}
	defer exec.Command("docker", "rm", "-f", containerName).Run()

	log.Println(">>> Step 2/5: Allocating disk (10GB)...")
	if err := exec.Command("truncate", "-s", "10G", rawPath).Run(); err != nil {
		return "", err
	}

	log.Println(">>> Step 3/5: Formatting ext4...")
	if err := exec.Command("mkfs.ext4", "-F", rawPath).Run(); err != nil {
		os.Remove(rawPath)
		return "", err
	}

	log.Println(">>> Step 4/5: Mounting...")
	mountPoint := fmt.Sprintf("/mnt/%s", safeName)
	os.MkdirAll(mountPoint, 0755)
	defer os.RemoveAll(mountPoint)

	if err := exec.Command("mount", "-o", "loop", rawPath, mountPoint).Run(); err != nil {
		return "", err
	}
	defer exec.Command("umount", mountPoint).Run()

	log.Println(">>> Step 5/5: Extracting rootfs...")
	cmdStr := fmt.Sprintf("docker export %s | tar -x -C %s", containerName, mountPoint)
	if output, err := exec.Command("bash", "-c", cmdStr).CombinedOutput(); err != nil {
		return "", fmt.Errorf("export failed: %s", string(output))
	}

	log.Printf("[Builder] Success! Bootable disk ready: %s", rawPath)
	return rawPath, nil
}
