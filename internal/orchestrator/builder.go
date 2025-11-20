package orchestrator

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

const ImageCacheDir = "/var/lib/qudata/images"

func EnsureImageReady(dockerImage string) (string, error) {
	safeName := strings.ReplaceAll(dockerImage, "/", "_")
	safeName = strings.ReplaceAll(safeName, ":", "_")
	rawPath := fmt.Sprintf("%s/%s.raw", ImageCacheDir, safeName)

	if _, err := os.Stat(rawPath); err == nil {
		log.Printf("[Builder] Image found in cache: %s", rawPath)
		return rawPath, nil
	}

	log.Printf("[Builder] Converting Docker image '%s' to MicroVM disk...", dockerImage)

	log.Println(">>> Step 1/5: Pulling image...")
	if err := exec.Command("docker", "pull", dockerImage).Run(); err != nil {
		return "", fmt.Errorf("docker pull failed (check internet or image name): %w", err)
	}

	containerName := fmt.Sprintf("builder_%s", safeName)
	exec.Command("docker", "rm", "-f", containerName).Run()

	if err := exec.Command("docker", "create", "--name", containerName, dockerImage).Run(); err != nil {
		return "", fmt.Errorf("docker create failed: %w", err)
	}
	defer exec.Command("docker", "rm", "-f", containerName).Run()

	log.Println(">>> Step 2/5: Allocating disk space...")
	if err := exec.Command("truncate", "-s", "10G", rawPath).Run(); err != nil {
		return "", fmt.Errorf("truncate failed: %w", err)
	}

	log.Println(">>> Step 3/5: Formatting filesystem...")
	if err := exec.Command("mkfs.ext4", "-F", rawPath).Run(); err != nil {
		os.Remove(rawPath) // Чистим за собой при ошибке
		return "", fmt.Errorf("mkfs failed: %w", err)
	}

	log.Println(">>> Step 4/5: Mounting...")
	mountPoint := fmt.Sprintf("/mnt/%s", safeName)
	os.MkdirAll(mountPoint, 0755)
	defer os.RemoveAll(mountPoint)

	if err := exec.Command("mount", "-o", "loop", rawPath, mountPoint).Run(); err != nil {
		return "", fmt.Errorf("mount failed: %w", err)
	}
	defer exec.Command("umount", mountPoint).Run()

	log.Println(">>> Step 5/5: Extracting rootfs...")
	cmdStr := fmt.Sprintf("docker export %s | tar -x -C %s", containerName, mountPoint)
	if output, err := exec.Command("bash", "-c", cmdStr).CombinedOutput(); err != nil {
		return "", fmt.Errorf("export failed: %s, %v", string(output), err)
	}

	log.Printf("[Builder] Success! Image ready: %s", rawPath)
	return rawPath, nil
}
